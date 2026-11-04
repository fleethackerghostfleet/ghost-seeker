package store

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

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
	directive := data.AddDirective("birds", []command.Rule{
		{Type: "suppress", Classes: []string{"bird"}, Attrs: []string{}},
	})
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

	directive := data.AddDirective("approaching boats", []command.Rule{
		{Type: "boost", Classes: []string{"small boat"}},
	})
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
