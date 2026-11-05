package console

import (
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// Request bounds shared by the shore and edge consoles.
const (
	DefaultPageSize = 100
	MaxPageSize     = 500
	MaxQueueEntries = 1000
	MaxLabels       = 64
	MaxLabelChars   = 128
	MaxOffset       = 1 << 31
)

// Page validates a page token (a non-negative decimal offset) and clamps the
// page size to [1, MaxPageSize]; 0 means DefaultPageSize.
func Page(token string, limit int32) (off, lim int, err error) {
	if token != "" {
		n, perr := strconv.ParseInt(token, 10, 64)
		if perr != nil || n < 0 || n > MaxOffset {
			return 0, 0, fmt.Errorf("invalid page_token %q", token)
		}
		off = int(n)
	}
	switch {
	case limit < 0:
		return 0, 0, fmt.Errorf("limit must be non-negative")
	case limit == 0:
		lim = DefaultPageSize
	case limit > MaxPageSize:
		lim = MaxPageSize
	default:
		lim = int(limit)
	}
	return off, lim, nil
}

// QueueLimit clamps GetQueue's limit; 0 means MaxQueueEntries.
func QueueLimit(limit int32) int {
	if limit <= 0 || limit > MaxQueueEntries {
		return MaxQueueEntries
	}
	return int(limit)
}

// ValidateLabel bounds a free-text label filter or override.
func ValidateLabel(s string) error {
	if utf8.RuneCountInString(s) > MaxLabelChars {
		return fmt.Errorf("label longer than %d characters", MaxLabelChars)
	}
	return nil
}

// ValidateInterest bounds the boost/suppress lists an operator may submit,
// which the shore turns into text embeddings.
func ValidateInterest(in *pb.Interest) error {
	if in == nil {
		return nil
	}
	for name, list := range map[string][]string{"boost_labels": in.BoostLabels, "suppress_labels": in.SuppressLabels} {
		if len(list) > MaxLabels {
			return fmt.Errorf("%s: at most %d labels", name, MaxLabels)
		}
		for _, l := range list {
			if l == "" {
				return errors.New(name + ": empty label")
			}
			if err := ValidateLabel(l); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}
