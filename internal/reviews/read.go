package reviews

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ReadEffective reads logs without opening them for append. Duplicate record IDs
// (for example overlapping exports) are counted once.
func ReadEffective(paths []string) ([]Record, int, error) {
	l := &Log{}
	seen := map[string]string{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, 0, err
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 4096), 4<<20)
		line := 0
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
			b, _ := json.Marshal(r)
			if old, ok := seen[r.ID]; ok {
				if old != string(b) {
					f.Close()
					return nil, 0, fmt.Errorf("conflicting record id %s", r.ID)
				}
				continue
			}
			seen[r.ID] = string(b)
			l.events = append(l.events, r)
		}
		err = s.Err()
		f.Close()
		if err != nil {
			return nil, 0, err
		}
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
