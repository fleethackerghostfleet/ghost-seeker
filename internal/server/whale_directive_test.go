package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestSimWhaleDirectiveCompileAndApply(t *testing.T) {
	for _, text := range []string{"whale", "prioritize whales", "prioritize orca", "prioritize orcas", "prioritize dolphins", "prioritize porpoises", "prioritize cetaceans", "prioritize humpback whales", "prioritize marine mammals", "ignore whales"} {
		t.Run(text, func(t *testing.T) {
			data := store.New(10)
			app := New(data)
			app.SetCompiler(&command.Compiler{Locks: app.PolicyLocks()})
			response := request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": text})
			var compiled command.Compiled
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &compiled) != nil {
				t.Fatalf("compile: %d %s", response.Code, response.Body.String())
			}
			kind, readback := "boost", "Boost: marine mammal. Until cleared."
			if text == "ignore whales" {
				kind, readback = "suppress", "Suppress: marine mammal. Until cleared."
			}
			if compiled.Blocked || len(compiled.Unknown) != 0 || len(compiled.Rules) != 1 || compiled.Rules[0].Type != kind || !reflect.DeepEqual(compiled.Rules[0].Classes, []string{"marine mammal"}) || compiled.Readback != readback {
				t.Fatalf("compile %q = %+v", text, compiled)
			}
			response = request(app, http.MethodPost, "/api/directives", map[string]any{"text": text, "rules": compiled.Rules})
			if response.Code != http.StatusCreated {
				t.Fatalf("apply: %d %s", response.Code, response.Body.String())
			}
			var added command.Directive
			if err := json.Unmarshal(response.Body.Bytes(), &added); err != nil {
				t.Fatal(err)
			}
			stored, ok := data.Directive(added.ID)
			if !ok || len(stored.Rules) != 1 || stored.Rules[0].Type != kind || !reflect.DeepEqual(stored.Rules[0].Classes, []string{"marine mammal"}) || stored.Rules[0].DirectiveID != added.ID {
				t.Fatalf("stored directive = %+v, found=%v", stored, ok)
			}
			response = request(app, http.MethodGet, "/api/config", nil)
			var config struct {
				Classes map[string]catalog.Class `json:"classes"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &config) != nil || config.Classes["marine mammal"].Label == "" {
				t.Fatalf("config missing marine mammal: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSimWhaleDirectiveEmptyAndUnknown(t *testing.T) {
	for _, text := range []string{"", "prioritize unicorns"} {
		t.Run(text, func(t *testing.T) {
			app := New(store.New(10))
			response := request(app, http.MethodPost, "/api/directives/compile", map[string]string{"text": text})
			var compiled command.Compiled
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &compiled) != nil || len(compiled.Rules) != 0 {
				t.Fatalf("unexpected compile: %d %s", response.Code, response.Body.String())
			}
			response = request(app, http.MethodPost, "/api/directives", map[string]string{"text": text})
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid directive applied: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
