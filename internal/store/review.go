package store

import (
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

type ReviewPatch struct {
	ReviewedAt                time.Time
	Manual                    *int
	Reviewed, Dismissed, Sent *bool
}

// CommitReview holds the store lock through validation and durable persistence.
// prepare must not call back into the store.
func (s *Store) CommitReview(id string, prepare func(command.Item, State) (ReviewPatch, error)) (command.Item, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.ID != id {
			continue
		}
		patch, err := prepare(cloneItem(item), State{Model: s.model, Directives: cloneDirectives(s.directives)})
		if err != nil {
			return command.Item{}, s.cdr, err
		}
		updated, cdr, _ := s.patchItemLocked(id, patch.Manual, patch.Reviewed, patch.Dismissed, patch.Sent, nil, patch.ReviewedAt)
		return updated, cdr, nil
	}
	return command.Item{}, s.cdr, ErrNotFound
}
