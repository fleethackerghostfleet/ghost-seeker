//go:build !prod

package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestStoreMinimumRetentionAndEmptyLookups(t *testing.T) {
	s := New(0)
	if s.RetainLimit() != 1 || s.Model() != "maritime" {
		t.Fatalf("defaults: retain=%d model=%q", s.RetainLimit(), s.Model())
	}
	if _, ok := s.GetItem("absent"); ok {
		t.Fatal("missing item found")
	}
	if _, ok := s.Directive("absent"); ok || s.DeleteDirective("absent") {
		t.Fatal("missing directive found")
	}
	if _, evicted := s.Add(nil, 2); evicted != "" {
		t.Fatalf("nil input evicted=%q", evicted)
	}
	if stats, _ := s.Stats(); stats.Cap != 3 || stats.Drop != 3 || stats.DB != 0 || stats.Keep != 0 {
		t.Fatalf("nil input stats=%+v", stats)
	}
	s.Add(&command.Item{ID: "first"}, 0)
	if _, evicted := s.Add(&command.Item{ID: "second"}, 0); evicted != "first" {
		t.Fatalf("evicted=%q", evicted)
	}
	if items := s.Items(); len(items) != 1 || items[0].ID != "second" {
		t.Fatalf("items=%+v", items)
	}
}

func TestDirectiveLookupCopiesAndPolicyLocksSurviveRestoration(t *testing.T) {
	s := New(2)
	local, err := s.AddDirective("watch floor", []command.Rule{{Type: "boost", Classes: []string{"small boat"}, Attrs: []string{"moving"}, Sources: []string{"EO"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.Directive(local.ID)
	if !ok {
		t.Fatal("new directive missing")
	}
	got.Rules[0].Classes[0] = "bird"
	got.Rules[0].Attrs[0] = "dark"
	got.Rules[0].Sources[0] = "IR"
	again, ok := s.Directive(local.ID)
	if !ok || !reflect.DeepEqual(again, local) {
		t.Fatalf("lookup mutated directive=%+v", again)
	}
	policy := command.Directive{ID: "fleet-policy", Source: "shore", Locked: true, Rules: []command.Rule{{Type: "boost", Classes: []string{"warship"}}}}
	s.SetPolicyDirectives([]command.Directive{policy}, map[string]map[int]string{local.ID: {-1: "ignore", 99: "ignore"}})
	if s.DeleteDirective(policy.ID) {
		t.Fatal("deleted policy directive")
	}
	saved := s.Directives()
	if len(saved) != 2 {
		t.Fatalf("directives=%+v", saved)
	}
	s.ClearDirectives()
	if len(s.Directives()) != 1 {
		t.Fatal("clear removed policy or kept local")
	}
	s.RestoreDirectives([]command.Directive{local})
	if len(s.Directives()) != 2 {
		t.Fatal("restore discarded policy")
	}
	local.Rules[0].Classes[0] = "changed after restore"
	if got, _ := s.Directive(local.ID); got.Rules[0].Classes[0] != "small boat" {
		t.Fatal("restore retained caller slices")
	}
	if !s.DeleteDirective(local.ID) || s.DeleteDirective(local.ID) {
		t.Fatal("delete was not idempotent")
	}
	if got, ok := s.Directive(policy.ID); !ok || !got.Locked {
		t.Fatal("policy lost after deleting local")
	}
}

func TestTimeScopedRescoreEntersAndLeavesRuleWindow(t *testing.T) {
	s := New(2)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.Add(&command.Item{ID: "boat", TS: now, Dets: []command.Detection{{Class: "small boat", Conf: .9}}}, 0)
	if s.RescoreTimeScoped(now) {
		t.Fatal("no rules reported a rescore")
	}
	if _, err := s.AddDirective("persistent birds", []command.Rule{{Type: "boost", Classes: []string{"bird"}}}); err != nil {
		t.Fatal(err)
	}
	if s.RescoreTimeScoped(now) {
		t.Fatal("untimed rule reported a timed rescore")
	}
	if _, err := s.AddDirective("recent boats", []command.Rule{{Type: "boost", Classes: []string{"small boat"}, WithinMin: 10, Tier: 1}}); err != nil {
		t.Fatal(err)
	}
	if !s.RescoreTimeScoped(now.Add(10 * time.Minute)) {
		t.Fatal("timed rule skipped")
	}
	inside, _ := s.GetItem("boat")
	if inside.Tier != 1 || !inside.Det[0].Matched {
		t.Fatalf("within inclusive window=%+v", inside)
	}
	if !s.RescoreTimeScoped(now.Add(10*time.Minute + time.Nanosecond)) {
		t.Fatal("expired rule did not trigger rescore")
	}
	outside, _ := s.GetItem("boat")
	if outside.Det[0].Matched || outside.Tier == 1 {
		t.Fatalf("expired rule still applied=%+v", outside)
	}
	empty := New(1)
	empty.RestoreDirectives(s.Directives())
	if !empty.RescoreTimeScoped(now) {
		t.Fatal("empty store skipped timed directives")
	}
}

func TestFullResolutionOperatorNameBoundaryAndRepeatedRequests(t *testing.T) {
	s := New(2)
	s.Add(&command.Item{ID: "frame", KB: 1}, 0)
	if _, _, err := s.RequestFullRes("frame", " \t "); !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("blank operator=%v", err)
	}
	if _, _, err := s.RequestFullRes("absent", "watch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing frame=%v", err)
	}
	name := strings.Repeat("界", maxNoteRunes+1)
	first, _, err := s.RequestFullRes("frame", "  "+name+"  ")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(first.FullResBy)) != maxNoteRunes || first.Upload.OverrideBy != first.FullResBy || first.Upload.Bytes < 4*1024*1024 {
		t.Fatalf("full-size=%+v", first)
	}
	second, _, err := s.RequestFullRes("frame", "different operator")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeat changed override: %+v %v", second, err)
	}
}
