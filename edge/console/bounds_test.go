package console

import (
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestPage(t *testing.T) {
	for _, tc := range []struct {
		token   string
		limit   int32
		off     int
		lim     int
		wantErr bool
	}{
		{"", 0, 0, DefaultPageSize, false},
		{"40", 20, 40, 20, false},
		{"", 10_000, 0, MaxPageSize, false},
		{"-1", 0, 0, 0, true},
		{"abc", 0, 0, 0, true},
		{"99999999999999999999", 0, 0, 0, true},
		{"", -5, 0, 0, true},
	} {
		off, lim, err := Page(tc.token, tc.limit)
		if (err != nil) != tc.wantErr || off != tc.off || lim != tc.lim {
			t.Errorf("Page(%q,%d) = %d,%d,%v want %d,%d,err=%v", tc.token, tc.limit, off, lim, err, tc.off, tc.lim, tc.wantErr)
		}
	}
	if QueueLimit(0) != MaxQueueEntries || QueueLimit(5) != 5 || QueueLimit(1<<20) != MaxQueueEntries {
		t.Fatal("QueueLimit")
	}
}

func TestValidateInterest(t *testing.T) {
	if err := ValidateInterest(nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInterest(&pb.Interest{BoostLabels: []string{"submarine"}, SuppressLabels: []string{"cargo ship"}}); err != nil {
		t.Fatal(err)
	}
	many := make([]string, MaxLabels+1)
	for i := range many {
		many[i] = "x"
	}
	if err := ValidateInterest(&pb.Interest{BoostLabels: many}); err == nil {
		t.Fatal("too many labels accepted")
	}
	if err := ValidateInterest(&pb.Interest{SuppressLabels: []string{strings.Repeat("y", MaxLabelChars+1)}}); err == nil {
		t.Fatal("long label accepted")
	}
	if err := ValidateInterest(&pb.Interest{BoostLabels: []string{""}}); err == nil {
		t.Fatal("empty label accepted")
	}
}
