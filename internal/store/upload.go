package store

import (
	"math"
	"math/big"
	"sort"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

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
		if !s.items[i].Upload.Done {
			queue = append(queue, i)
		}
	}
	sort.SliceStable(queue, func(i, j int) bool {
		a, b := s.items[queue[i]], s.items[queue[j]]
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
		changed = append(changed, cloneItem(s.items[i]))
	}
	return changed
}
