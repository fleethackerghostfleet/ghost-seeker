package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestReset(t *testing.T) {
	s := New(4)
	s.SetModel("maritime")
	s.AddDirective("test", []command.Rule{{Type: "boost", Classes: []string{"tanker"}, Tier: 1}})
	s.SetLinkBPS(12345)
	s.SetLinkFixed(true)
	first, _ := s.Add(&command.Item{ID: "first", KB: 10}, 3)
	s.SetUploadOverride(first.ID, "next", "test")
	s.AdvanceUploads(time.Millisecond)
	_, _, _, before := s.SnapshotWithDirectives()
	order := s.uploadOrder
	if epoch := s.Reset(); epoch != 1 || s.Epoch() != epoch {
		t.Fatalf("epoch = %d", epoch)
	}
	items, stats, cdr, after := s.SnapshotWithDirectives()
	if len(items) != 0 || cdr != 0 || stats != (command.Stats{LinkBPS: 12345, LinkFixed: true}) || s.uploadRemainder != 0 || s.uploadOrder != order || !reflect.DeepEqual(before, after) || s.Has(first.ID) {
		t.Fatalf("reset: items=%v stats=%+v cdr=%d state=%+v", items, stats, cdr, after)
	}
	if updates := s.AdvanceUploads(time.Second); len(updates) != 0 {
		t.Fatal(updates)
	}
	second, _ := s.Add(&command.Item{ID: "second"}, 0)
	if second.DBID <= first.DBID || !s.Has(second.ID) {
		t.Fatalf("DBIDs: %d %d", first.DBID, second.DBID)
	}
	s.Reset()
	if s.Epoch() != 2 {
		t.Fatal(s.Epoch())
	}
}

func TestAddEvictsOldestAtRetentionLimit(t *testing.T) {
	data := New(2)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"oldest", "middle", "newest"} {
		item := command.Item{ID: id, TS: start.Add(time.Duration(i) * time.Minute)}
		_, evicted := data.Add(&item, 0)
		if i < 2 && evicted != "" {
			t.Fatalf("unexpected eviction after item %q: %s", id, evicted)
		}
		if i == 2 && evicted != "oldest" {
			t.Fatalf("evicted %q, want oldest", evicted)
		}
	}

	items := data.Items()
	if len(items) > 2 {
		t.Fatalf("retained %d items, maximum is 2", len(items))
	}
	if len(items) != 2 || items[0].ID != "middle" || items[1].ID != "newest" {
		t.Fatalf("retained items = %#v, want middle and newest", items)
	}
}

func TestDirectiveClonePreservesEmptySlices(t *testing.T) {
	data := New(2)
	directive, err := data.AddDirective("birds", []command.Rule{
		{Type: "suppress", Classes: []string{"bird"}, Attrs: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if directive.Rules[0].Attrs == nil {
		t.Fatal("directive attrs is nil, want an empty array")
	}
	_, _, _, state := data.Snapshot()
	if state.Directives[0].Rules[0].Attrs == nil {
		t.Fatal("snapshot attrs is nil, want an empty array")
	}
}

func TestDirectiveIDIsPresentOnRulesAndRescoredNotes(t *testing.T) {
	data := New(2)
	item := command.Item{
		ID: "small-boat",
		Dets: []command.Detection{{
			Class: "small boat",
			Conf:  .9,
			Attrs: command.Attributes{Approaching: true, Moving: true},
		}},
	}
	data.Add(&item, 0)

	directive, err := data.AddDirective("approaching boats", []command.Rule{
		{Type: "boost", Classes: []string{"small boat"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if directive.Rules[0].DirectiveID != directive.ID {
		t.Fatalf("directive rule id = %q, want %q", directive.Rules[0].DirectiveID, directive.ID)
	}
	stored, ok := data.GetItem(item.ID)
	if !ok {
		t.Fatalf("item %q not found", item.ID)
	}
	for _, rule := range stored.Det[0].Notes {
		if rule.DirectiveID == directive.ID {
			return
		}
	}
	t.Fatalf("rescored notes %#v do not contain directive id %q", stored.Det[0].Notes, directive.ID)
}

func TestPartialPolicyConflictBlocksOnlyOneRule(t *testing.T) {
	data := New(2)
	data.RestoreDirectives([]command.Directive{{
		ID: "operator", Rules: []command.Rule{
			{Type: "suppress", Classes: []string{"submarine"}},
			{Type: "boost", Classes: []string{"tanker"}},
		},
	}})
	data.SetPolicyDirectives(nil, map[string]map[int]string{"operator": {0: "Locked by CTF-72"}})
	directives := data.Directives()
	if len(directives) != 1 || directives[0].Blocked != "" ||
		directives[0].Rules[0].Blocked == "" || directives[0].Rules[1].Blocked != "" {
		t.Fatalf("partial blocked directive=%+v", directives)
	}
	weights := command.EffectiveWeights("maritime", directives)
	if weights["tanker"].Effective <= weights["tanker"].Base {
		t.Fatalf("unblocked tanker rule did not affect weights: %+v", weights["tanker"])
	}
}

func TestSourceDirectivesAreProtectedAndBlockedRulesAreNotScored(t *testing.T) {
	data := New(2)
	item := command.Item{
		ID: "tanker",
		Dets: []command.Detection{{
			Class: "tanker",
			Conf:  .9,
		}},
	}
	data.Add(&item, 0)
	directive, err := data.AddDirective("prioritize tankers", []command.Rule{
		{Type: "boost", Classes: []string{"tanker"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	data.SetPolicyDirectives([]command.Directive{{
		ID: "command-rule", Text: "command rule", Source: "CTF-72", Locked: true, LockedBy: "CTF-72",
	}}, map[string]map[int]string{directive.ID: {0: "Locked by CTF-72"}})
	if data.DeleteDirective("command-rule") {
		t.Fatal("DeleteDirective removed a source directive")
	}
	data.ClearDirectives()

	directives := data.Directives()
	if len(directives) != 1 || directives[0].ID != "command-rule" {
		t.Fatalf("directives after clear = %#v, want only retained command directive", directives)
	}
	stored, ok := data.GetItem(item.ID)
	if !ok {
		t.Fatalf("item %q not found", item.ID)
	}
	for _, note := range stored.Det[0].Notes {
		if note.DirectiveID == directive.ID {
			t.Fatalf("blocked directive %q was still scored: %#v", directive.ID, stored.Det[0].Notes)
		}
	}
}

func TestSourceDirectivesDoNotCountTowardOperatorLimit(t *testing.T) {
	data := New(2)
	data.SetPolicyDirectives([]command.Directive{{ID: "command", Source: "CTF-72"}}, nil)
	for i := 0; i < MaxActiveDirectives; i++ {
		if _, err := data.AddDirective("birds", []command.Rule{{Type: "boost", Classes: []string{"bird"}}}); err != nil {
			t.Fatalf("AddDirective %d: %v", i+1, err)
		}
	}
}
