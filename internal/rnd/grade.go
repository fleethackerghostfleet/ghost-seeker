package rnd

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Counters struct {
	Graded      int     `json:"graded"`
	Changed     int     `json:"changed"`
	ChangedPct  float64 `json:"changed_pct"`
	MeanAbs     float64 `json:"mean_abs_delta"`
	MeanChanged float64 `json:"mean_abs_changed"`
	MeanLast20  float64 `json:"mean_abs_last20"`
	MeanSigned  float64 `json:"mean_signed_delta"`
}
type Event struct {
	Seq        int       `json:"seq"`
	Kind       string    `json:"kind"`
	TS         time.Time `json:"ts"`
	Reviewer   string    `json:"reviewer"`
	Testset    string    `json:"testset"`
	ImageID    string    `json:"image_id,omitempty"`
	Source     string    `json:"source,omitempty"`
	ModelTier  int       `json:"model_tier,omitempty"`
	ModelScore float64   `json:"model_score,omitempty"`
	HumanTier  int       `json:"human_tier,omitempty"`
	Delta      int       `json:"delta"`
	Changed    bool      `json:"changed"`
	Ref        int       `json:"ref,omitempty"`
	Running    Counters  `json:"running"`
}
type Summary struct {
	Counters
	PerSource map[string]Counters `json:"per_source"`
	Confusion [4][4]int           `json:"confusion"`
}
type Entry struct {
	Image Image `json:"image"`
	Prediction
}
type State struct {
	Testset  string           `json:"testset"`
	Reviewer string           `json:"reviewer"`
	Entries  []Entry          `json:"entries"`
	Grades   map[string]Event `json:"grades"`
	Summary  Summary          `json:"summary"`
}

func Effective(events []Event) []Event {
	undone := map[int]bool{}
	for _, e := range events {
		if e.Kind == "undo" {
			undone[e.Ref] = true
		}
	}
	latest := map[string]Event{}
	for _, e := range events {
		if e.Kind == "grade" && !undone[e.Seq] {
			latest[e.ImageID] = e
		}
	}
	out := make([]Event, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}
func count(grades []Event) Counters {
	c := Counters{Graded: len(grades)}
	for i, g := range grades {
		delta := g.HumanTier - g.ModelTier
		abs := math.Abs(float64(delta))
		if delta != 0 {
			c.Changed++
		}
		c.MeanAbs += abs
		c.MeanSigned += float64(delta)
		if i >= len(grades)-20 {
			c.MeanLast20 += abs
		}
	}
	if c.Graded > 0 {
		c.MeanChanged = c.MeanAbs
		c.MeanAbs /= float64(c.Graded)
		c.MeanSigned /= float64(c.Graded)
		c.MeanLast20 /= float64(min(20, c.Graded))
		c.ChangedPct = 100 * float64(c.Changed) / float64(c.Graded)
	}
	if c.Changed > 0 {
		c.MeanChanged /= float64(c.Changed)
	}
	return c
}
func Summarize(events []Event) Summary {
	grades := Effective(events)
	s := Summary{Counters: count(grades), PerSource: map[string]Counters{}}
	sources := map[string][]Event{}
	for _, g := range grades {
		sources[g.Source] = append(sources[g.Source], g)
		if g.ModelTier >= 1 && g.ModelTier <= 4 && g.HumanTier >= 1 && g.HumanTier <= 4 {
			s.Confusion[g.ModelTier-1][g.HumanTier-1]++
		}
	}
	for source, rows := range sources {
		s.PerSource[source] = count(rows)
	}
	return s
}

type Session struct {
	mu                           sync.Mutex
	dir, reviewer, testset, path string
	entries                      []Entry
	byID                         map[string]Entry
	events                       []Event
	file                         *os.File
	lock                         *os.File
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) > 40 {
		name = name[:40]
	}
	return fmt.Sprintf("%s-%x", name, sha256.Sum256([]byte(s)))[:len(name)+13]
}
func readRows[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows := []T{}
	d := json.NewDecoder(f)
	for {
		var row T
		err = d.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rows = append(rows, row)
	}
}
func OpenSession(dir, logs, reviewer string) (*Session, error) {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" || len(reviewer) > 100 {
		return nil, fmt.Errorf("reviewer must be 1-100 bytes")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	images, err := readRows[Image](filepath.Join(dir, "manifest.jsonl"))
	if err != nil {
		return nil, err
	}
	predictions, err := readRows[Prediction](filepath.Join(dir, "predictions.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(images) == 0 || len(images) != len(predictions) {
		return nil, fmt.Errorf("manifest/predictions must be nonempty and aligned")
	}
	pmap := map[string]Prediction{}
	for _, p := range predictions {
		if _, exists := pmap[p.ID]; exists || p.ModelTier < 1 || p.ModelTier > 4 {
			return nil, fmt.Errorf("invalid/duplicate prediction %q", p.ID)
		}
		pmap[p.ID] = p
	}
	s := &Session{dir: dir, reviewer: reviewer, testset: filepath.Base(dir), byID: map[string]Entry{}}
	for _, im := range images {
		p, ok := pmap[im.ID]
		if !ok || !safeID.MatchString(im.ID) || im.Width < 1 || im.Height < 1 || filepath.Base(im.File) != im.File || !imageFile(im.File) {
			return nil, fmt.Errorf("invalid manifest image %q", im.ID)
		}
		if _, exists := s.byID[im.ID]; exists {
			return nil, fmt.Errorf("duplicate manifest image %q", im.ID)
		}
		if _, err = os.Stat(filepath.Join(dir, "images", im.File)); err != nil {
			return nil, err
		}
		entry := Entry{Image: im, Prediction: p}
		s.entries = append(s.entries, entry)
		s.byID[im.ID] = entry
	}
	if err = os.MkdirAll(logs, 0700); err != nil {
		return nil, err
	}
	prefix := slug(s.testset) + "-" + slug(reviewer) + "-"
	s.lock, err = os.OpenFile(filepath.Join(logs, "."+prefix+"lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		s.lock.Close()
		return nil, fmt.Errorf("session already open: %w", err)
	}
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()
	matches, err := filepath.Glob(filepath.Join(logs, prefix+"*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if len(matches) > 0 {
		s.path = matches[len(matches)-1]
		if err = s.replay(); err != nil {
			return nil, err
		}
	} else {
		s.path = filepath.Join(logs, prefix+time.Now().UTC().Format("20060102T150405.000000000Z")+".jsonl")
	}
	s.file, err = os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if err = s.summary(); err != nil {
		return nil, err
	}
	success = true
	return s, nil
}

// A torn final append is retained separately, then truncated back to the last
// complete JSONL record. Corruption of a complete record is never ignored.
func (s *Session) replay() error {
	f, err := os.OpenFile(s.path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	offset := int64(0)
	undone := map[int]bool{}
	for {
		line, readErr := r.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			if len(line) > 0 {
				if err = os.WriteFile(s.path+".partial-"+time.Now().UTC().Format("20060102T150405.000000000"), line, 0600); err != nil {
					return err
				}
				if err = f.Truncate(offset); err != nil {
					return err
				}
				return f.Sync()
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}
		var e Event
		if err = json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("corrupt journal: %w", err)
		}
		if e.Seq != len(s.events)+1 || e.Reviewer != s.reviewer || e.Testset != s.testset {
			return fmt.Errorf("journal identity/sequence mismatch")
		}
		switch e.Kind {
		case "grade":
			item, ok := s.byID[e.ImageID]
			if !ok || e.ModelTier != item.ModelTier || e.ModelScore != item.ModelScore || e.Source != item.Image.Source || e.HumanTier < 1 || e.HumanTier > 4 || e.Delta != e.HumanTier-e.ModelTier || e.Changed != (e.Delta != 0) {
				return fmt.Errorf("invalid grade in journal")
			}
		case "undo":
			if e.Ref < 1 || e.Ref > len(s.events) || s.events[e.Ref-1].Kind != "grade" || undone[e.Ref] {
				return fmt.Errorf("invalid undo in journal")
			}
			undone[e.Ref] = true
		default:
			return fmt.Errorf("unknown journal event")
		}
		s.events = append(s.events, e)
		offset += int64(len(line))
	}
}
func (s *Session) Close() error {
	if s.file != nil {
		s.file.Close()
		s.file = nil
	}
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
	return nil
}
func (s *Session) Path() string { return s.path }
func (s *Session) state() State {
	grades := map[string]Event{}
	for _, g := range Effective(s.events) {
		grades[g.ImageID] = g
	}
	return State{Testset: s.testset, Reviewer: s.reviewer, Entries: s.entries, Grades: grades, Summary: Summarize(s.events)}
}
func (s *Session) State() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state() }
func (s *Session) Grade(id string, tier int) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.byID[id]
	if !ok || tier < 1 || tier > 4 {
		return State{}, fmt.Errorf("unknown image or tier outside 1-4")
	}
	e := Event{Kind: "grade", ImageID: id, Source: item.Image.Source, ModelTier: item.ModelTier, ModelScore: item.ModelScore, HumanTier: tier, Delta: tier - item.ModelTier, Changed: tier != item.ModelTier}
	return s.append(e)
}

var ErrNoUndo = errors.New("nothing to undo")

func (s *Session) Undo() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	undone := map[int]bool{}
	for _, e := range s.events {
		if e.Kind == "undo" {
			undone[e.Ref] = true
		}
	}
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Kind == "grade" && !undone[e.Seq] {
			return s.append(Event{Kind: "undo", Ref: e.Seq, ImageID: e.ImageID})
		}
	}
	return State{}, ErrNoUndo
}
func (s *Session) append(e Event) (State, error) {
	e.Seq = len(s.events) + 1
	e.TS = time.Now().UTC()
	e.Reviewer = s.reviewer
	e.Testset = s.testset
	next := append(append([]Event(nil), s.events...), e)
	e.Running = Summarize(next).Counters
	data, err := json.Marshal(e)
	if err != nil {
		return State{}, err
	}
	data = append(data, '\n')
	if s.file == nil {
		return State{}, fmt.Errorf("session closed")
	}
	if _, err = s.file.Write(data); err != nil {
		s.file.Close()
		s.file = nil
		return State{}, err
	}
	if err = s.file.Sync(); err != nil {
		s.file.Close()
		s.file = nil
		return State{}, err
	}
	s.events = append(s.events, e)
	if err = s.summary(); err != nil {
		return State{}, fmt.Errorf("grade persisted; summary write failed (restart repairs): %w", err)
	}
	return s.state(), nil
}
func (s *Session) summary() error {
	data, err := json.MarshalIndent(s.state().Summary, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".summary-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), strings.TrimSuffix(s.path, ".jsonl")+"-summary.json"); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
