package api_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	apidocs "github.com/fleethackerghostfleet/ghost-seeker/docs/api"
	"gopkg.in/yaml.v3"
)

// This deliberately small validator covers the JSON Schema subset used by the
// checked-in 3.1 contract. It is independent of the API's Go response structs,
// so renaming/removing fields cannot silently change the asserted contract.
type contract map[string]any

func loadContract(t *testing.T) contract {
	t.Helper()
	var raw map[string]any
	if err := yaml.Unmarshal(apidocs.Spec, &raw); err != nil {
		t.Fatal(err)
	}
	c := contract(raw)
	if c["openapi"] != "3.1.0" {
		t.Fatal("expected OpenAPI 3.1")
	}
	return c
}

func (c contract) resolve(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		var v any = map[string]any(c)
		for _, key := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			v = v.(map[string]any)[key]
		}
		return c.resolve(v.(map[string]any))
	}
	return schema
}

func (c contract) validate(s map[string]any, v any) error {
	s = c.resolve(s)
	if choices, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, choice := range choices {
			if c.validate(choice.(map[string]any), v) == nil {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("oneOf matched %d alternatives", n)
		}
		return nil
	}
	switch s["type"] {
	case "null":
		if v != nil {
			return fmt.Errorf("expected null")
		}
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object, got %T", v)
		}
		if n, ok := s["maxProperties"].(int); ok && len(obj) > n {
			return fmt.Errorf("too many properties")
		}
		if req, ok := s["required"].([]any); ok {
			for _, key := range req {
				if _, ok := obj[key.(string)]; !ok {
					return fmt.Errorf("missing %s", key)
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		for key, value := range obj {
			p, ok := props[key]
			if !ok {
				switch extra := s["additionalProperties"].(type) {
				case bool:
					if !extra {
						return fmt.Errorf("unexpected %s", key)
					}
				case map[string]any:
					p = extra
				}
			}
			if p != nil {
				if err := c.validate(p.(map[string]any), value); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("expected array, got %T", v)
		}
		for i, value := range arr {
			if err := c.validate(s["items"].(map[string]any), value); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case "string":
		x, ok := v.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", v)
		}
		if pattern, ok := s["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(x) {
			return fmt.Errorf("pattern mismatch")
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, x); err != nil {
				return err
			}
		}
		if n, ok := s["maxLength"].(int); ok && len([]rune(x)) > n {
			return fmt.Errorf("string too long")
		}
	case "number", "integer":
		x, ok := v.(float64)
		if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("expected finite number")
		}
		if s["type"] == "integer" && math.Trunc(x) != x {
			return fmt.Errorf("expected integer")
		}
		for _, key := range []string{"minimum", "maximum", "exclusiveMaximum"} {
			if bound, ok := s[key]; ok {
				n, _ := strconv.ParseFloat(fmt.Sprint(bound), 64)
				if key == "minimum" && x < n || key == "maximum" && x > n || key == "exclusiveMaximum" && x >= n {
					return fmt.Errorf("violated %s", key)
				}
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	default:
		return fmt.Errorf("unsupported contract type %v", s["type"])
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, allowed := range enum {
			if reflect.DeepEqual(v, allowed) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("enum mismatch: %v", v)
		}
	}
	return nil
}

func (c contract) check(t *testing.T, name string, body []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err, string(body))
	}
	if err := c.validate(map[string]any{"$ref": "#/components/schemas/" + name}, v); err != nil {
		t.Fatalf("%s contract: %v\n%s", name, err, body)
	}
}

func (c contract) response(t *testing.T, path, method string, status int, typ string, body []byte) {
	t.Helper()
	paths := c["paths"].(map[string]any)
	operation := paths[path].(map[string]any)[method].(map[string]any)
	res, ok := operation["responses"].(map[string]any)[strconv.Itoa(status)]
	if !ok {
		t.Fatalf("undocumented response: %s %d", path, status)
	}
	r := c.resolve(res.(map[string]any))
	content := r["content"].(map[string]any)
	m, ok := content[typ].(map[string]any)
	if !ok {
		t.Fatalf("undocumented content type %s", typ)
	}
	if typ == "application/json" {
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatal(err)
		}
		if err := c.validate(m["schema"].(map[string]any), v); err != nil {
			t.Fatalf("response contract: %v\n%s", err, body)
		}
	}
}

func TestProdContractValidatorRejectsDrift(t *testing.T) {
	c := loadContract(t)
	s := map[string]any{"$ref": "#/components/schemas/IngestResult"}
	for _, bad := range []string{`{}`, `{"id":"bad id","duplicate":false}`, `{"id":"safe","duplicate":"false"}`, `{"id":"safe","duplicate":false,"new_key":true}`} {
		var v any
		_ = json.Unmarshal([]byte(bad), &v)
		if c.validate(s, v) == nil {
			t.Fatalf("validator accepted %s", bad)
		}
	}
}
