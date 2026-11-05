package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestCommitReview(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := New(1)
		s.Add(&command.Item{ID: "target"}, 0)
		before, _ := s.GetItem("target")
		rejected := errors.New("persistence failed")
		item, _, err := s.CommitReview("target", func(old command.Item, state State) (ReviewPatch, error) {
			if s.mu.TryLock() {
				s.mu.Unlock()
				t.Fatal("prepare ran without the eviction lock")
			}
			if !reflect.DeepEqual(old, before) || state.Model != s.model {
				t.Fatal("prepare did not receive current item and state")
			}
			if fail {
				return ReviewPatch{}, rejected
			}
			tier, yes := 1, true
			return ReviewPatch{Manual: &tier, Reviewed: &yes}, nil
		})
		after, _ := s.GetItem("target")
		if fail {
			if !errors.Is(err, rejected) || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed review mutated item: %+v err=%v", after, err)
			}
		} else if err != nil || !item.Reviewed || item.Manual != 1 || !reflect.DeepEqual(item, after) {
			t.Fatalf("review not committed: %+v err=%v", item, err)
		}
		s.Add(&command.Item{ID: "replacement"}, 0)
		_, _, err = s.CommitReview("target", func(command.Item, State) (ReviewPatch, error) {
			t.Fatal("prepare called for evicted item")
			return ReviewPatch{}, nil
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing item: %v", err)
		}
	}
}
