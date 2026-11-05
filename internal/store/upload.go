package store

import (
	"errors"
	"math"
	"math/big"
	"sort"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

var ErrInvalidUploadOverride = errors.New("upload override must be empty, next, or hold")

func (s *Store) SetUploadOverride(id, override, by string) (command.Item, int, error) {
	if override != "" && override != "next" && override != "hold" {
		return command.Item{}, 0, ErrInvalidUploadOverride
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		item := &s.items[i]
		if item.ID != id {
			continue
		}
		u := &item.Upload
		if !u.Done && u.Override != override {
			now := time.Now()
			u.Override, u.OverrideBy, u.OverrideAt = override, by, &now
			u.NextOrder = 0
			if override == "next" {
				s.uploadOrder++
				u.NextOrder = s.uploadOrder
			}
		}
		return cloneItem(*item), s.cdr, nil
	}
	return command.Item{}, s.cdr, ErrNotFound
}

func (s *Store) SetLinkBPS(bps int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linkBPS = max(0, bps)
	s.uploadRemainder = 0
	for i := range s.items {
		s.items[i].Upload.BPS = s.linkBPS
	}
}

// AdvanceUploads spends one shared link budget in priority order.
func (s *Store) AdvanceUploads(elapsed time.Duration) []command.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if elapsed <= 0 || s.linkBPS == 0 {
		return nil
	}
	var numerator, quotient, remainder big.Int
	numerator.Mul(big.NewInt(s.linkBPS), big.NewInt(int64(elapsed)))
	numerator.Add(&numerator, big.NewInt(s.uploadRemainder))
	quotient.QuoRem(&numerator, big.NewInt(8*int64(time.Second)), &remainder)
	s.uploadRemainder = remainder.Int64()
	budget := int64(math.MaxInt64)
	if quotient.IsInt64() {
		budget = quotient.Int64()
	}
	queue := make([]int, 0, len(s.items))
	for i := range s.items {
		if !s.items[i].Upload.Done && s.items[i].Upload.Override != "hold" {
			queue = append(queue, i)
		}
	}
	sort.SliceStable(queue, func(i, j int) bool {
		a, b := s.items[queue[i]], s.items[queue[j]]
		if (a.Upload.Override == "next") != (b.Upload.Override == "next") {
			return a.Upload.Override == "next"
		}
		if a.Upload.Override == "next" {
			return a.Upload.NextOrder < b.Upload.NextOrder
		}
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		return a.Central > b.Central
	})
	var changed []command.Item
	for _, i := range queue {
		if budget == 0 {
			break
		}
		u := &s.items[i].Upload
		n := min(budget, u.Bytes-u.Sent)
		u.Sent += n
		s.stats.Bytes += int(n)
		budget -= n
		u.Done = u.Sent == u.Bytes
		if u.Done {
			u.Override = ""
			u.NextOrder = 0
		}
		changed = append(changed, cloneItem(s.items[i]))
	}
	return changed
}
