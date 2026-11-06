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
			if override == "hold" {
				u.Active = false
			}
			u.NextOrder = 0
			if override == "next" {
				s.uploadOrder++
				u.NextOrder = s.uploadOrder
				if !item.Sent {
					s.cdr++
				}
				item.Sent, item.AutoSent = true, false
				item.SentAt = &now
			}
		}
		return cloneItem(*item), s.cdr, nil
	}
	return command.Item{}, s.cdr, ErrNotFound
}

// SeedUploaded marks n images as already on shore (Commander sends first, then
// the highest-scoring P1s), so a fresh simulation opens with a queue instead of
// a fully sent backlog.
func (s *Store) SeedUploaded(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var top []int
	for i, item := range s.items {
		if (item.Tier == 1 || item.Sent) && item.Filtered == "" && !item.Background {
			top = append(top, i)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		a, b := s.items[top[i]], s.items[top[j]]
		if a.Sent != b.Sent {
			return a.Sent
		}
		return a.Central > b.Central
	})
	for _, i := range top[:min(n, len(top))] {
		u := &s.items[i].Upload
		now := s.clock
		u.MetaSent, u.MetaDone, u.MetaDoneAt = u.MetaBytes, true, &now
		s.stats.Bytes += int(u.Bytes - u.Sent)
		u.Sent, u.Done, u.Received, u.Active = u.Bytes, true, true, false
	}
}

// SetLinkFixed records that -sim-link-bps overrides the automatic simulated rate.
func (s *Store) SetLinkFixed(fixed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LinkFixed = fixed
}

func (s *Store) SetLinkBPS(bps int64) []command.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linkBPS = max(0, bps)
	s.stats.LinkBPS = s.linkBPS
	s.observeLink()
	s.uploadRemainder = 0
	var changed []command.Item
	for i := range s.items {
		u := &s.items[i].Upload
		previous := u.BPS
		u.BPS = s.linkBPS
		if s.linkBPS == 0 {
			u.Active = false
		}
		if previous != u.BPS && u.Sent > 0 && !u.Done && u.Override != "hold" {
			changed = append(changed, cloneItem(s.items[i]))
		}
	}
	return changed
}

// AdvanceUploads spends one shared link budget in priority order.
func (s *Store) AdvanceUploads(elapsed time.Duration) (changed []command.Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if elapsed <= 0 {
		return nil
	}
	s.clock = s.clock.Add(elapsed)
	s.observeLink()
	if s.linkBPS == 0 {
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
	if budget == 0 {
		return nil // sub-byte tick: keep Active so the UI does not flicker
	}
	metadataQueue := make([]int, 0, len(s.items))
	imageQueue := make([]int, 0, len(s.items))
	for i := range s.items {
		item := s.items[i]
		if !item.Upload.MetaDone && item.Upload.MetaBytes > item.Upload.MetaSent {
			metadataQueue = append(metadataQueue, i)
		}
		// Background/scene images stay aboard until explicitly pulled. Their
		// metadata is still eligible for the first pass above.
		if item.Background && item.Manual == 0 && !item.Sent && !item.FullRes && item.Upload.Override != "next" {
			continue
		}
		if !item.Upload.Done && item.Upload.Override != "hold" && (!s.stats.MetaOnly || item.Upload.Override == "next" || item.FullRes) {
			imageQueue = append(imageQueue, i)
		}
	}
	queueOrder := func(queue []int) {
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
	}
	queueOrder(metadataQueue)
	queueOrder(imageQueue)
	wasActive := map[int]bool{}
	for i := range s.items {
		if s.items[i].Upload.Active {
			wasActive[i] = true
			s.items[i].Upload.Active = false
		}
	}
	defer func() {
		for i := range s.items {
			if wasActive[i] {
				changed = append(changed, cloneItem(s.items[i]))
			}
		}
	}()
	// Metadata gets the complete first pass. No image bytes are spent until
	// every eligible item's metadata is complete, even if that leaves budget.
	for _, i := range metadataQueue {
		if budget == 0 {
			break
		}
		delete(wasActive, i)
		u := &s.items[i].Upload
		var n int64
		if !u.MetaDone && u.MetaBytes > 0 {
			n = min(budget, u.MetaBytes-u.MetaSent)
			u.MetaSent += n
			if u.MetaSent == u.MetaBytes {
				u.MetaDone = true
				now := s.clock
				u.MetaDoneAt = &now
			}
		}
		s.stats.Bytes += int(n)
		budget -= n
		// Active describes image bytes on the wire. Metadata delivery must not
		// make the UI render the image as uploading.
		changed = append(changed, cloneItem(s.items[i]))
	}
	for _, item := range s.items {
		if !item.Upload.MetaDone && item.Upload.MetaBytes > item.Upload.MetaSent {
			return changed
		}
	}
	for _, i := range imageQueue {
		if budget == 0 {
			break
		}
		delete(wasActive, i)
		u := &s.items[i].Upload
		n := min(budget, u.Bytes-u.Sent)
		u.Sent += n
		s.stats.Bytes += int(n)
		budget -= n
		u.Done = u.Sent == u.Bytes
		u.Active = n > 0 && !u.Done
		if u.Done {
			u.Received = true
			u.Override = ""
			u.NextOrder = 0
		}
		changed = append(changed, cloneItem(s.items[i]))
	}
	return changed
}

func (s *Store) observeLink() {
	mode := s.lowband.Observe(s.linkBPS, s.clock)
	s.stats.MetaOnly = mode || s.emconBurst
	if s.emconBurst {
		s.stats.MetaOnlyReason = "emcon"
	} else if mode {
		s.stats.MetaOnlyReason = "link"
	} else {
		s.stats.MetaOnlyReason = ""
	}
	if s.stats.MetaOnly {
		since := s.lowband.Since()
		if s.emconBurst && since.IsZero() {
			since = s.clock
		}
		s.stats.MetaOnlySince = &since
	} else {
		s.stats.MetaOnlySince = nil
	}
}

// SetEMCON reports the policy's BURST state to the simulated uplink.
func (s *Store) SetEMCON(burst bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emconBurst = burst
	s.observeLink()
}
