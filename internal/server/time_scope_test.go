package server

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestTimeScopedRefreshExpiresBoostAndPublishes(t *testing.T) {
	now := time.Now()
	data := store.New(2)
	for _, id := range []string{"boat", "pinned"} {
		item := command.Item{
			ID: id, TS: now.Add(-29 * time.Minute),
			Dets: []command.Detection{{Class: "small boat", Conf: .9}},
		}
		if id == "pinned" {
			item.Manual = 4
		}
		data.Add(&item, 0)
	}
	base := data.Items()
	if _, err := data.AddDirective("boats last 30 min", []command.Rule{
		{Type: "boost", Classes: []string{"small boat"}, WithinMin: 30},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(data)
	client := app.hub.add()
	defer app.hub.remove(client)

	for _, tc := range []struct {
		at      time.Time
		boosted bool
	}{{now, true}, {now.Add(2 * time.Minute), false}} {
		app.RescoreTimeScoped(tc.at)
		select {
		case event := <-client.events:
			if event.name != "snapshot" {
				t.Fatalf("event = %q, want snapshot", event.name)
			}
			items := event.data.(map[string]any)["items"].([]command.Item)
			if len(items) != 2 {
				t.Fatalf("snapshot contains %d items, want 2", len(items))
			}
			for i, item := range items {
				if tc.boosted {
					if item.Central <= base[i].Central || item.CentralTier >= base[i].CentralTier {
						t.Fatalf("item was not boosted: %+v", item)
					}
				} else if item.Central != base[i].Central || item.CentralTier != base[i].CentralTier {
					t.Fatalf("expired item retained boost: %+v, base %+v", item, base[i])
				}
				if item.ID == "pinned" && item.Tier != 4 {
					t.Fatalf("manual tier lost: %d", item.Tier)
				}
				stored, ok := data.GetItem(item.ID)
				if !ok || stored.Central != item.Central || stored.Tier != item.Tier {
					t.Fatalf("published item differs from stored item: %+v", stored)
				}
			}
		default:
			t.Fatal("refresh did not publish a snapshot")
		}
	}
}

func TestTimeScopedRefreshWithoutWindowDoesNotPublish(t *testing.T) {
	for _, active := range []bool{false, true} {
		data := store.New(1)
		if active {
			if _, err := data.AddDirective("boats", []command.Rule{
				{Type: "boost", Classes: []string{"small boat"}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		app := New(data)
		client := app.hub.add()
		app.RescoreTimeScoped(time.Now())
		select {
		case event := <-client.events:
			t.Fatalf("unexpected event without time scopes: %q", event.name)
		default:
		}
		app.hub.remove(client)
	}
}
