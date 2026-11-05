package policy

import (
	"errors"
	"testing"
	"time"
)

func TestNextVersion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		last    int64
		want    int64
		wantErr error
	}{
		{name: "normal", last: 0, want: now.UnixMilli()},
		{name: "negative last", last: -5, want: now.UnixMilli()},
		{name: "last above clock", last: now.UnixMilli() + 1, want: now.UnixMilli() + 2},
		{name: "max version", last: MaxVersion, wantErr: ErrVersionRange},
		{name: "max int64", last: int64(^uint64(0) >> 1), wantErr: ErrVersionRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NextVersion(test.last, now)
			if !errors.Is(err, test.wantErr) || got != test.want {
				t.Fatalf("NextVersion(%d)=(%d,%v), want (%d,%v)", test.last, got, err, test.want, test.wantErr)
			}
		})
	}
}
