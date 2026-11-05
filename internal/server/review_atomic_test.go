package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

type hookedReviewLog struct {
	*reviews.Log
	append func(reviews.Record) (reviews.Record, error)
}

func (l hookedReviewLog) Append(rec reviews.Record) (reviews.Record, error) {
	return l.append(rec)
}

func TestReviewEvictionDuringAppend(t *testing.T) {
	l, err := reviews.Open(filepath.Join(t.TempDir(), "reviews"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	d := store.New(1)
	d.Add(&command.Item{ID: "target"}, 0)
	s := New(d)
	client, ok := s.hub.add()
	if !ok {
		t.Fatal("cannot subscribe")
	}
	defer s.hub.remove(client)
	started, evicted := make(chan struct{}), make(chan struct{})
	s.reviews = hookedReviewLog{Log: l, append: func(rec reviews.Record) (reviews.Record, error) {
		go func() {
			close(started)
			d.Add(&command.Item{ID: "replacement"}, 0)
			close(evicted)
		}()
		<-started
		return l.Append(rec)
	}}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/target/review", strings.NewReader(`{"reviewer":"A","action":"tier","tier":1}`)))
	<-evicted
	if w.Code != 200 || l.Summary().Count != 1 {
		t.Fatalf("review/eviction not atomic: status=%d records=%d body=%s", w.Code, l.Summary().Count, w.Body.String())
	}
	var response struct {
		Item   command.Item
		Record reviews.Record
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Item.Reviewed || response.Item.Manual != 1 || response.Record.Before.Reviewed {
		t.Fatalf("review not committed: %+v", response)
	}
	select {
	case event := <-client.events:
		if event.name != "update" {
			t.Fatalf("event=%s", event.name)
		}
	default:
		t.Fatal("review update not published")
	}
	items, _, _, _ := d.Snapshot()
	if len(items) != 1 {
		t.Fatalf("retention exceeded: %d", len(items))
	}
}

func TestReviewMissingAndPersistenceFailure(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reviews")
			l, err := reviews.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			d := store.New(1)
			d.Add(&command.Item{ID: "target"}, 0)
			before, _ := d.GetItem("target")
			s := New(d)
			s.SetReviewLog(l)
			want := 500
			if missing {
				d.Add(&command.Item{ID: "replacement"}, 0)
				want = 404
				defer l.Close()
			} else {
				l.Close()
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/target/review", strings.NewReader(`{"reviewer":"A","action":"tier","tier":1}`)))
			if w.Code != want || l.Summary().Count != 0 {
				t.Fatalf("status=%d records=%d body=%s", w.Code, l.Summary().Count, w.Body.String())
			}
			if !missing {
				after, _ := d.GetItem("target")
				if !reflect.DeepEqual(before, after) || !strings.Contains(w.Body.String(), "cannot persist review") {
					t.Fatalf("failed persistence changed state: %s", w.Body.String())
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) != 0 {
				t.Fatalf("log=%q err=%v", data, err)
			}
		})
	}
}
