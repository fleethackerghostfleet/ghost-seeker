package store

import (
	"errors"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func addItem(data *Store, id, class string, conf float64, ts time.Time) {
	data.Add(&command.Item{ID: id, TS: ts, Dets: []command.Detection{{Class: class, Conf: conf}}}, 0)
}

func TestDecideLifecycle(t *testing.T) {
	data := New(10)
	addItem(data, "a", "warship", .95, time.Now())
	if _, _, err := data.Decide("a", "track", ""); !errors.Is(err, ErrNotEscalated) {
		t.Fatalf("decide before escalation: err = %v, want ErrNotEscalated", err)
	}
	if _, _, err := data.Decide("missing", "track", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decide unknown item: err = %v, want ErrNotFound", err)
	}
	sent := true
	if item, _, _ := data.PatchItem("a", nil, nil, nil, &sent); item.SentAt == nil {
		t.Fatal("sending to commander did not set sentAt")
	}
	if _, _, err := data.Decide("a", "nuke", ""); !errors.Is(err, ErrUnknownDecision) {
		t.Fatalf("unknown decision: err = %v, want ErrUnknownDecision", err)
	}
	item, cdr, err := data.Decide("a", "track", "  keep eyes on it  ")
	if err != nil || item.Decision != "track" || item.DecisionNote != "keep eyes on it" || item.DecidedAt == nil || cdr != 1 {
		t.Fatalf("track: item=%+v cdr=%d err=%v", item, cdr, err)
	}
	item, cdr, err = data.Decide("a", DecisionReturn, "")
	if err != nil || item.Sent || item.Reviewed || cdr != 0 {
		t.Fatalf("return: sent=%v reviewed=%v cdr=%d err=%v", item.Sent, item.Reviewed, cdr, err)
	}
	item, cdr, err = data.Decide("a", "", "")
	if err != nil || !item.Sent || item.Decision != "" || item.DecidedAt != nil || cdr != 1 {
		t.Fatalf("reopen after return: item=%+v cdr=%d err=%v", item, cdr, err)
	}
}

func TestEvictionKeepsEscalatedItems(t *testing.T) {
	data := New(2)
	start := time.Now().Add(-time.Hour)
	addItem(data, "old", "warship", .95, start)
	sent := true
	data.PatchItem("old", nil, nil, nil, &sent)
	addItem(data, "mid", "bird", .5, start.Add(time.Minute))
	if _, evicted := data.Add(&command.Item{ID: "new", TS: start.Add(2 * time.Minute)}, 0); evicted != "mid" {
		t.Fatalf("evicted %q, want mid (escalated items are kept)", evicted)
	}
}

func TestEvictionWhenBacklogIsAllEscalated(t *testing.T) {
	data := New(1)
	start := time.Now().Add(-time.Hour)
	addItem(data, "old", "warship", .95, start)
	sent := true
	data.PatchItem("old", nil, nil, nil, &sent)
	if _, evicted := data.Add(&command.Item{ID: "new", TS: start.Add(time.Minute)}, 0); evicted != "old" {
		t.Fatalf("evicted %q, want old (new captures must still be kept)", evicted)
	}
}

func TestAutoEscalatePicksTopPending(t *testing.T) {
	data := New(10)
	addItem(data, "bird", "bird", .9, time.Now())
	addItem(data, "ship", "warship", .95, time.Now())
	item, cdr, ok := data.AutoEscalate()
	if !ok || item.ID != "ship" || !item.Sent || !item.Reviewed || item.Manual != item.Tier || cdr != 1 {
		t.Fatalf("auto-escalate: ok=%v item=%+v cdr=%d", ok, item, cdr)
	}
	if _, _, ok := data.AutoEscalate(); ok {
		t.Fatal("auto-escalated a low-priority item")
	}
}
