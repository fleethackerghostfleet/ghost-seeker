// Package store is the central imagery database: image blobs on disk plus an
// in-memory index of metadata, scores and review state persisted as JSON.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scoring"
)

// MaxItems bounds the number of records kept; the oldest are evicted first.
const MaxItems = 5000

type Latency struct {
	Edge  int     `json:"edge"`
	Up    int     `json:"up"`
	Store float64 `json:"store"`
	Cen   float64 `json:"cen"`
}

// Item is one stored image record.
type Item struct {
	ID          string              `json:"id"`
	DBID        int                 `json:"dbId"`
	Source      string              `json:"src"`
	CapturedAt  int64               `json:"ts"`
	ReceivedAt  int64               `json:"rx"`
	Frame       int                 `json:"frame"`
	Edge        float64             `json:"edge"`
	Dets        []catalog.Detection `json:"dets"`
	KB          float64             `json:"kb"`
	ContentType string              `json:"contentType"`
	Lat         Latency             `json:"lat"`
	scoring.Result
	Tier      int  `json:"tier"`
	Manual    int  `json:"manual"`
	Reviewed  bool `json:"reviewed"`
	Dismissed bool `json:"dismissed"`
	Sent      bool `json:"sent"`
}

func (it *Item) applyTier() {
	it.Tier = scoring.Tier(it.Central)
	if it.Manual > 0 {
		it.Tier = it.Manual
	}
}

// Stats are pipeline counters across all edge nodes.
type Stats struct {
	Captured int              `json:"cap"`
	Kept     int              `json:"keep"`
	Dropped  int              `json:"drop"`
	KB       float64          `json:"kb"`
	DB       int              `json:"db"`
	CDR      int              `json:"cdr"`
	LastSeen map[string]int64 `json:"lastSeen"`
}

// Review is an operator decision on an image.
type Review struct {
	Manual    int    `json:"manual"`
	Reviewed  bool   `json:"reviewed"`
	Dismissed bool   `json:"dismissed"`
	Sent      bool   `json:"sent"`
	Action    string `json:"action,omitempty"`
	Suggested int    `json:"suggested,omitempty"`
}

type persisted struct {
	Items  []*Item        `json:"items"`
	Stats  Stats          `json:"stats"`
	Config scoring.Config `json:"config"`
	NextDB int            `json:"nextDb"`
}

type Store struct {
	dir   string
	mu    sync.RWMutex
	items []*Item // in arrival order
	byID  map[string]*Item
	stats Stats
	cfg   scoring.Config
	next  int
	dirty bool
}

var ErrNotFound = errors.New("not found")

func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, byID: map[string]*Item{}, cfg: scoring.Config{Model: "maritime", Directives: []scoring.Directive{}}, next: 180001, stats: Stats{LastSeen: map[string]int64{}}}
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return nil, err
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	s.items, s.stats, s.next = p.Items, p.Stats, p.NextDB
	if p.Config.Model != "" {
		s.cfg = p.Config
	}
	if s.cfg.Directives == nil {
		s.cfg.Directives = []scoring.Directive{}
	}
	if s.stats.LastSeen == nil {
		s.stats.LastSeen = map[string]int64{}
	}
	for _, it := range s.items {
		s.byID[it.ID] = it
	}
	return s, nil
}

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "img_" + hex.EncodeToString(b)
}

func ext(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	return ".bin"
}

func (s *Store) blobPath(it *Item) string {
	return filepath.Join(s.dir, "images", it.ID+ext(it.ContentType))
}

// Ingest stores a triaged frame and scores it under the active config.
func (s *Store) Ingest(r catalog.Report, contentType string, img []byte) (Item, Stats, error) {
	t0 := time.Now()
	it := &Item{ID: newID(), Source: r.Source, CapturedAt: r.CapturedAt, ReceivedAt: t0.UnixMilli(), Frame: r.Frame,
		Edge: r.EdgeScore, Dets: r.Detections, KB: float64(len(img)) / 1024, ContentType: contentType,
		Lat: Latency{Edge: r.EdgeMs, Up: r.UplinkMs}}
	if it.Dets == nil {
		it.Dets = []catalog.Detection{}
	}
	if err := os.WriteFile(s.blobPath(it), img, 0o644); err != nil {
		return Item{}, Stats{}, err
	}
	t1 := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	it.Result = scoring.Score(s.cfg, it.Dets)
	it.applyTier()
	it.Lat.Store = ms(t1.Sub(t0))
	it.Lat.Cen = ms(time.Since(t1))
	it.DBID = s.next
	s.next++
	s.items = append(s.items, it)
	s.byID[it.ID] = it
	if len(s.items) > MaxItems {
		old := s.items[0]
		s.items = s.items[1:]
		delete(s.byID, old.ID)
		os.Remove(s.blobPath(old))
	}
	captured := max(r.Captured, 1)
	s.stats.Captured += captured
	s.stats.Dropped += max(r.Dropped, captured-1)
	s.stats.Kept++
	s.stats.KB += it.KB
	s.stats.DB = len(s.items)
	s.stats.LastSeen[r.Source] = t0.UnixMilli()
	s.dirty = true
	return *it, s.statsCopy(), nil
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (s *Store) statsCopy() Stats {
	st := s.stats
	st.LastSeen = make(map[string]int64, len(s.stats.LastSeen))
	for k, v := range s.stats.LastSeen {
		st.LastSeen[k] = v
	}
	return st
}

func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statsCopy()
}

// List returns up to limit of the most recently captured records, optionally for one source.
func (s *Store) List(source string, limit int) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Item
	for i := len(s.items) - 1; i >= 0 && len(out) < limit; i-- {
		if source == "" || s.items[i].Source == source {
			out = append(out, *s.items[i])
		}
	}
	slices.SortFunc(out, func(a, b Item) int { return int(b.CapturedAt - a.CapturedAt) })
	return out
}

func (s *Store) Get(id string) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.byID[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return *it, nil
}

// Blob returns the stored image bytes and content type.
func (s *Store) Blob(id string) ([]byte, string, error) {
	s.mu.RLock()
	it, ok := s.byID[id]
	var p, ct string
	if ok {
		p, ct = s.blobPath(it), it.ContentType
	}
	s.mu.RUnlock()
	if !ok {
		return nil, "", ErrNotFound
	}
	b, err := os.ReadFile(p)
	return b, ct, err
}

// SetReview records an operator decision and appends it to the feedback log
// used to retrain the command model.
func (s *Store) SetReview(id string, r Review) (Item, Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.byID[id]
	if !ok {
		return Item{}, Stats{}, ErrNotFound
	}
	if r.Manual < 0 || r.Manual > 4 {
		r.Manual = 0
	}
	if r.Sent && !it.Sent {
		s.stats.CDR++
	} else if !r.Sent && it.Sent {
		s.stats.CDR--
	}
	it.Manual, it.Reviewed, it.Dismissed, it.Sent = r.Manual, r.Reviewed, r.Dismissed, r.Sent
	it.applyTier()
	s.dirty = true
	if f, err := os.OpenFile(filepath.Join(s.dir, "feedback.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		json.NewEncoder(f).Encode(map[string]any{"t": time.Now().UTC(), "id": id, "dbId": it.DBID, "src": it.Source,
			"model": s.cfg.Model, "central": it.Central, "suggested": scoring.Tier(it.Central), "review": r, "dets": it.Dets})
		f.Close()
	}
	return *it, s.statsCopy(), nil
}

// Config returns a copy of the active scoring config.
func (s *Store) Config() scoring.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyConfig(s.cfg)
}

func copyConfig(c scoring.Config) scoring.Config {
	c.Directives = append([]scoring.Directive{}, c.Directives...)
	return c
}

// ScoreUpdate is the rescored state of one record.
type ScoreUpdate struct {
	ID string `json:"id"`
	scoring.Result
	Tier int `json:"tier"`
}

// Rescore summarizes a config change applied to every stored record.
type Rescore struct {
	Config   scoring.Config `json:"config"`
	Scores   []ScoreUpdate  `json:"scores"`
	Promoted int            `json:"promoted"`
	Demoted  int            `json:"demoted"`
}

// UpdateConfig mutates the config and rescores every record.
func (s *Store) UpdateConfig(fn func(*scoring.Config)) Rescore {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	res := Rescore{Config: copyConfig(s.cfg), Scores: make([]ScoreUpdate, 0, len(s.items))}
	for _, it := range s.items {
		before := it.Tier
		it.Result = scoring.Score(s.cfg, it.Dets)
		it.applyTier()
		if it.Tier < before {
			res.Promoted++
		} else if it.Tier > before {
			res.Demoted++
		}
		res.Scores = append(res.Scores, ScoreUpdate{it.ID, it.Result, it.Tier})
	}
	s.dirty = true
	return res
}

// Flush writes state.json if anything changed.
func (s *Store) Flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	b, err := json.Marshal(persisted{Items: s.items, Stats: s.stats, Config: s.cfg, NextDB: s.next})
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "state.json.tmp")
	err = os.WriteFile(tmp, b, 0o644)
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.dir, "state.json"))
	}
	if err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
	}
	return err
}
