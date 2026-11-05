package reviews

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ReadEffective prefers versions from files containing correction events over
// exports. Ties prefer populated attribution fields, then canonical JSON order.
func ReadEffective(paths []string) ([]Record, int, error) {
	l := &Log{}
	type version struct {
		record    Record
		raw       bool
		canonical string
	}
	seen := map[string]version{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, 0, err
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 4096), 4<<20)
		line := 0
		fileRecords := []Record{}
		raw := false
		for s.Scan() {
			line++
			var r Record
			if err = json.Unmarshal(s.Bytes(), &r); err != nil {
				f.Close()
				return nil, 0, fmt.Errorf("%s:%d: %w", p, line, err)
			}
			if r.ID == "" {
				f.Close()
				return nil, 0, fmt.Errorf("%s:%d: missing record id", p, line)
			}
			fileRecords = append(fileRecords, r)
			raw = raw || r.Ref != ""
		}
		err = s.Err()
		f.Close()
		if err != nil {
			return nil, 0, err
		}
		for _, r := range fileRecords {
			b, _ := json.Marshal(r)
			old, ok := seen[r.ID]
			fields := func(r Record) int {
				n := 0
				if r.Reason != "" {
					n++
				}
				if r.Note != "" {
					n++
				}
				return n
			}
			if !ok || (raw && !old.raw) || (raw == old.raw && (fields(r) > fields(old.record) || (fields(r) == fields(old.record) && string(b) > old.canonical))) {
				seen[r.ID] = version{r, raw, string(b)}
			}
		}
	}
	for _, v := range seen {
		l.events = append(l.events, v.record)
	}
	sort.SliceStable(l.events, func(i, j int) bool {
		a, b := l.events[i], l.events[j]
		if a.ReviewedAt.Equal(b.ReviewedAt) {
			if (a.Ref == "") != (b.Ref == "") {
				return a.Ref == ""
			}
			return a.ID < b.ID
		}
		return a.ReviewedAt.Before(b.ReviewedAt)
	})
	return l.effective(), len(l.events), nil
}
