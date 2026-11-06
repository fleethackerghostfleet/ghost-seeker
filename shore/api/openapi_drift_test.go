package api_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
)

func TestProdContractShoreRouteInventory(t *testing.T) {
	c := loadContract(t)
	file, err := parser.ParseFile(token.NewFileSet(), "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Fatal("route inventory requires a literal route pattern")
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		registered[pattern] = true
		if strings.HasPrefix(pattern, "GET ") {
			registered["HEAD "+strings.TrimPrefix(pattern, "GET ")] = true
		}
		return true
	})
	documented := map[string]bool{}
	security := []any{map[string]any{"bearerAuth": []any{}}}
	for path, value := range c["paths"].(map[string]any) {
		if path == "/api/v1/ingest/frames" {
			continue
		}
		for method, value := range value.(map[string]any) {
			switch method {
			case "get", "head", "post", "put", "patch", "delete", "options", "trace":
			default:
				continue
			}
			operation := value.(map[string]any)
			auth, ok := operation["security"]
			if !ok {
				auth = c["security"]
			}
			if !reflect.DeepEqual(auth, security) {
				t.Fatalf("%s %s must document bearer authentication", method, path)
			}
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}
	if !reflect.DeepEqual(registered, documented) {
		t.Fatalf("route drift: registered=%v documented=%v", registered, documented)
	}
}

func (c contract) headResponse(t *testing.T, path string, status int, response *http.Response, body []byte) {
	t.Helper()
	operation, ok := c["paths"].(map[string]any)[path].(map[string]any)["head"].(map[string]any)
	if !ok {
		t.Fatalf("undocumented HEAD %s", path)
	}
	documented, ok := operation["responses"].(map[string]any)[strconv.Itoa(status)].(map[string]any)
	if !ok {
		t.Fatalf("undocumented HEAD %s status %d", path, status)
	}
	documented = c.resolve(documented)
	if _, ok := documented["content"]; ok {
		t.Fatal("HEAD must not document a response body")
	}
	header := documented["headers"].(map[string]any)["Content-Type"].(map[string]any)
	if err := c.validate(header["schema"].(map[string]any), response.Header.Get("Content-Type")); err != nil {
		t.Fatalf("HEAD %s content type: %v", path, err)
	}
	if response.StatusCode != status || len(body) != 0 {
		t.Fatalf("HEAD %s: status %d, body %q", path, response.StatusCode, body)
	}
}

func TestProdContractShoreHEAD(t *testing.T) {
	s := newRig(t)
	r, body := s.request(t, http.MethodHead, s.shore.URL+"/api/v1/items", operator, "", nil)
	s.contract.headResponse(t, "/api/v1/items", http.StatusOK, r, body)
	id := s.push(t, 4, false)
	s.waitImage(t, id)
	for _, tc := range []struct{ path, request string }{
		{"/api/v1/items", "/api/v1/items?limit=1"},
		{"/api/v1/items/{id}", "/api/v1/items/" + id},
		{"/api/v1/items/{id}/image", "/api/v1/items/" + id + "/image"},
		{"/api/v1/stream", "/api/v1/stream"},
		{"/api/v1/health", "/api/v1/health"},
		{"/api/v1/openapi.yaml", "/api/v1/openapi.yaml"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			operations := s.contract["paths"].(map[string]any)[tc.path].(map[string]any)
			get := operations["get"].(map[string]any)
			head, ok := operations["head"].(map[string]any)
			if !ok {
				t.Fatalf("missing HEAD %s", tc.path)
			}
			if !reflect.DeepEqual(get["parameters"], head["parameters"]) {
				t.Fatal("GET and HEAD parameters differ")
			}
			getResponses := get["responses"].(map[string]any)
			headResponses := head["responses"].(map[string]any)
			if len(getResponses) != len(headResponses) {
				t.Fatal("GET and HEAD response status counts differ")
			}
			for status := range getResponses {
				if _, ok := headResponses[status]; !ok {
					t.Fatalf("HEAD is missing GET status %s", status)
				}
			}
			for _, token := range []string{operator, "", "wrong"} {
				req, err := http.NewRequestWithContext(s.ctx, http.MethodHead, s.shore.URL+tc.request, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Close = true
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				if err := response.Body.Close(); err != nil {
					t.Fatal(err)
				}
				status := http.StatusOK
				if token != operator {
					status = http.StatusUnauthorized
				}
				if response.Body != http.NoBody {
					t.Fatal("HEAD returned a response body")
				}
				s.contract.headResponse(t, tc.path, status, response, nil)
			}
		})
	}
}

func TestProdContractShoreStoreFailureResponses(t *testing.T) {
	s := newRig(t)
	if err := s.st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, request string
		status        int
	}{
		{"/api/v1/items", "/api/v1/items", http.StatusInternalServerError},
		{"/api/v1/items/{id}", "/api/v1/items/safe", http.StatusInternalServerError},
		{"/api/v1/items/{id}/image", "/api/v1/items/safe/image", http.StatusInternalServerError},
		{"/api/v1/stream", "/api/v1/stream", http.StatusServiceUnavailable},
		{"/api/v1/health", "/api/v1/health", http.StatusServiceUnavailable},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				r, body := s.request(t, method, s.shore.URL+tc.request, operator, "", nil)
				if r.StatusCode != tc.status {
					t.Fatalf("%s %s: %d %s", method, tc.request, r.StatusCode, body)
				}
				if method == http.MethodHead {
					s.contract.headResponse(t, tc.path, tc.status, r, body)
				} else {
					s.contract.response(t, tc.path, "get", tc.status, r.Header.Get("Content-Type"), body)
				}
			}
		})
	}
}

func TestProdContractShoreForbiddenResponses(t *testing.T) {
	s := newRig(t)
	handler := httpx.CORSAllow(nil, httpx.NewHostAllowlist("127.0.0.1:0", nil), opauth.RequireBearer(operator, s.api.Handler()))
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, tc := range []struct{ path, request string }{
		{"/api/v1/items", "/api/v1/items"},
		{"/api/v1/items/{id}", "/api/v1/items/safe"},
		{"/api/v1/items/{id}/image", "/api/v1/items/safe/image"},
		{"/api/v1/stream", "/api/v1/stream"},
		{"/api/v1/health", "/api/v1/health"},
		{"/api/v1/openapi.yaml", "/api/v1/openapi.yaml"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, guard := range []struct {
					name   string
					status int
				}{{"host", http.StatusForbidden}, {"origin", http.StatusUnauthorized}} {
					req, err := http.NewRequestWithContext(s.ctx, method, server.URL+tc.request, nil)
					if err != nil {
						t.Fatal(err)
					}
					if guard.name == "host" {
						req.Host = "untrusted.invalid"
					} else {
						req.Header.Set("Origin", "https://untrusted.invalid")
					}
					response, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != guard.status {
						t.Fatalf("%s %s %s: status %d, want %d", method, guard.name, tc.path, response.StatusCode, guard.status)
					}
					if method == http.MethodHead {
						s.contract.headResponse(t, tc.path, guard.status, response, body)
					} else {
						typ, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
						if err != nil {
							t.Fatal(err)
						}
						s.contract.response(t, tc.path, "get", guard.status, typ, body)
						if len(body) == 0 {
							t.Fatal("GET rejection must include an error body")
						}
					}
				}
			}
		})
	}
}
