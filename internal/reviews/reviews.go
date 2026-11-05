package reviews

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/google/uuid"
)

type Record struct {
	ID         string                   `json:"id"`
	Ref        string                   `json:"ref,omitempty"`
	Action     string                   `json:"action"`
	ItemID     string                   `json:"item_id"`
	Img        string                   `json:"img"`
	SourceID   string                   `json:"source_id"`
	TS         time.Time                `json:"item_ts"`
	ReviewedAt time.Time                `json:"reviewed_at"`
	Reviewer   string                   `json:"reviewer"`
	Model      string                   `json:"model"`
	ModelKey   string                   `json:"model_key"`
	ModelTier  int                      `json:"model_tier"`
	ModelScore float64                  `json:"model_score"`
	Edge       float64                  `json:"edge_score"`
	BaseTier   int                      `json:"base_tier"`
	HumanTier  int                      `json:"human_tier"`
	Agreed     bool                     `json:"agreed"`
	Detections []command.Detection      `json:"detections"`
	Scores     []command.DetectionScore `json:"detection_scores"`
	Directives []string                 `json:"directives"`
	Reason     string                   `json:"reason,omitempty"`
	Note       string                   `json:"note,omitempty"`
	Before     command.Item             `json:"before"`
}
type Log struct {
	mu     sync.Mutex
	f      *os.File
	events []Record
}

func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4<<20)
	for s.Scan() {
		var r Record
		if err = json.Unmarshal(s.Bytes(), &r); err != nil {
			f.Close()
			return nil, fmt.Errorf("review log: %w", err)
		}
		l.events = append(l.events, r)
	}
	if err = s.Err(); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}
func (l *Log) Close() error { l.mu.Lock(); defer l.mu.Unlock(); return l.f.Close() }
func Trim(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func (l *Log) Append(r Record) (Record, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.append(r) }
func (l *Log) append(r Record) (Record, error) {
	r.ID = uuid.NewString()
	r.ReviewedAt = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	b = append(b, '\n')
	n, err := l.f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = l.f.Sync()
	}
	if err != nil {
		return r, err
	}
	l.events = append(l.events, r)
	return r, nil
}
func (l *Log) effective() []Record {
	out := []Record{}
	idx := map[string]int{}
	gone := map[string]bool{}
	for _, r := range l.events {
		if r.Ref == "" {
			idx[r.ID] = len(out)
			out = append(out, r)
		} else if i, ok := idx[r.Ref]; ok {
			if r.Action == "undo" {
				gone[r.Ref] = true
			} else {
				out[i].Reason = r.Reason
				out[i].Note = r.Note
			}
		}
	}
	v := []Record{}
	for _, r := range out {
		if !gone[r.ID] {
			v = append(v, r)
		}
	}
	return v
}
func (l *Log) Effective() []Record { l.mu.Lock(); defer l.mu.Unlock(); return l.effective() }
func (l *Log) Correct(id, reviewer, action, reason, note string) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.effective() {
		if r.ID == id {
			if r.Reviewer != reviewer {
				return Record{}, errors.New("reviewer does not own record")
			}
			return l.append(Record{Ref: id, Action: action, ItemID: r.ItemID, Reviewer: reviewer, Reason: reason, Note: note, Before: r.Before})
		}
	}
	return Record{}, errors.New("no effective review")
}

type Counts struct {
	Count     int     `json:"count"`
	Agreed    int     `json:"agreed"`
	Agreement float64 `json:"agreement"`
}
type Movement struct {
	Raised  int `json:"raised"`
	Lowered int `json:"lowered"`
}
type Summary struct {
	Counts
	Reviewers          map[string]Counts   `json:"reviewers"`
	Confusion          [4][4]int           `json:"confusion"`
	Classes            map[string]Movement `json:"classes"`
	MultiReviewerItems int                 `json:"multi_reviewer_items"`
	PairCount          int                 `json:"inter_reviewer_pairs"`
	PairAgreement      float64             `json:"inter_reviewer_agreement"`
}

func (l *Log) Summary() Summary {
	s := Summary{Reviewers: map[string]Counts{}, Classes: map[string]Movement{}}
	items := map[string]map[string]int{}
	for _, r := range l.Effective() {
		s.Count++
		c := s.Reviewers[r.Reviewer]
		c.Count++
		if r.Agreed {
			s.Agreed++
			c.Agreed++
		}
		c.Agreement = float64(c.Agreed) / float64(c.Count)
		s.Reviewers[r.Reviewer] = c
		if r.ModelTier >= 1 && r.ModelTier <= 4 && r.HumanTier >= 1 && r.HumanTier <= 4 {
			s.Confusion[r.ModelTier-1][r.HumanTier-1]++
		}
		top := ""
		score := -1.0
		for i, d := range r.Detections {
			v := d.Conf
			if i < len(r.Scores) {
				v = r.Scores[i].Score
			}
			if v > score {
				score = v
				top = d.Class
			}
		}
		m := s.Classes[top]
		if r.HumanTier > 0 && r.HumanTier < r.ModelTier {
			m.Raised++
		}
		if r.HumanTier == 0 || r.HumanTier > r.ModelTier {
			m.Lowered++
		}
		s.Classes[top] = m
		if items[r.ItemID] == nil {
			items[r.ItemID] = map[string]int{}
		}
		items[r.ItemID][r.Reviewer] = r.HumanTier
	}
	if s.Count > 0 {
		s.Agreement = float64(s.Agreed) / float64(s.Count)
	}
	same := 0
	for _, rs := range items {
		if len(rs) < 2 {
			continue
		}
		s.MultiReviewerItems++
		v := []int{}
		for _, t := range rs {
			v = append(v, t)
		}
		for i := range v {
			for j := 0; j < i; j++ {
				s.PairCount++
				if v[i] == v[j] {
					same++
				}
			}
		}
	}
	if s.PairCount > 0 {
		s.PairAgreement = float64(same) / float64(s.PairCount)
	}
	return s
}
func (l *Log) Export(w io.Writer, format string) error {
	rs := l.Effective()
	if format == "jsonl" {
		e := json.NewEncoder(w)
		for _, r := range rs {
			if err := e.Encode(r); err != nil {
				return err
			}
		}
		return nil
	}
	c := csv.NewWriter(w)
	c.Write([]string{"id", "item_id", "reviewer", "reviewed_at", "model", "model_tier", "human_tier", "agreed", "reason", "note", "record_json"})
	for _, r := range rs {
		b, _ := json.Marshal(r)
		row := []string{r.ID, r.ItemID, r.Reviewer, r.ReviewedAt.Format(time.RFC3339Nano), r.Model, fmt.Sprint(r.ModelTier), fmt.Sprint(r.HumanTier), fmt.Sprint(r.Agreed), r.Reason, r.Note, string(b)}
		for i, cell := range row {
			if strings.HasPrefix(cell, "=") || strings.HasPrefix(cell, "+") || strings.HasPrefix(cell, "-") || strings.HasPrefix(cell, "@") || strings.HasPrefix(cell, "\t") || strings.HasPrefix(cell, "\r") {
				row[i] = "'" + cell
			}
		}
		c.Write(row)
	}
	c.Flush()
	return c.Error()
}
