package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestAPI(t *testing.T) {
	data := store.New(10)
	app := New(data)

	response := request(app, http.MethodGet, "/api/config", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d, want %d", response.Code, http.StatusOK)
	}

	response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": "small boats"})
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/directives status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": "hello world"})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST invalid directive status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}

	response = request(app, http.MethodPut, "/api/model", map[string]string{"id": "unknown"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid model status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	item := command.Item{
		ID: "test-item",
		TS: time.Now(),
		Dets: []command.Detection{
			{Class: "bird", Conf: .8},
		},
	}
	data.Add(&item, 0)

	response = request(app, http.MethodPatch, "/api/items/missing", map[string]int{"manual": 2})
	if response.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown item status = %d, want %d", response.Code, http.StatusNotFound)
	}

	response = request(app, http.MethodPatch, "/api/items/test-item", map[string]int{"manual": 2})
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH manual=2 status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var patched struct {
		Item command.Item `json:"item"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Item.Tier != 2 {
		t.Fatalf("tier after manual=2 is %d, want 2", patched.Item.Tier)
	}

	response = request(app, http.MethodPatch, "/api/items/test-item", map[string]int{"manual": 0})
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH manual=0 status = %d, want %d", response.Code, http.StatusOK)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Item.Tier != command.TierFor(patched.Item.Central) {
		t.Fatalf("tier after clearing manual override is %d, want model tier %d", patched.Item.Tier, command.TierFor(patched.Item.Central))
	}

	response = request(app, http.MethodDelete, "/api/directives", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/directives status = %d, want %d", response.Code, http.StatusNoContent)
	}
	_, _, _, state := data.Snapshot()
	if len(state.Directives) != 0 {
		t.Fatalf("remaining directives = %d, want 0", len(state.Directives))
	}
}

func request(handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			panic(err)
		}
	}
	request := httptest.NewRequest(method, target, &encoded)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
