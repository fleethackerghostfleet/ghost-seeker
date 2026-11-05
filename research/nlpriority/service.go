package nlpriority

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrConflict = errors.New("preview is stale; preview the instruction again")
var ErrNotFound = errors.New("preview not found or expired")

type revision struct {
	Engine  string           `json:"engine"`
	Rules   []Rule           `json:"rules"`
	Matches map[string]Match `json:"matches"`
}
type persisted struct {
	Version       int        `json:"version"`
	Revision      uint64     `json:"revision"`
	QueueRevision uint64     `json:"queueRevision"`
	Active        revision   `json:"active"`
	Items         []Item     `json:"items"`
	History       []revision `json:"history"`
}
type pending struct {
	Preview       Preview
	Matches       map[string]Match
	QueueRevision uint64
}
type Config struct {
	DataDir      string
	Compiler     *Compiler
	Vision       *Vision
	BandwidthBPS float64
}
type Service struct {
	mu       sync.Mutex
	cfg      Config
	data     persisted
	previews map[string]pending
	now      func() time.Time
}

func Open(cfg Config) (*Service, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("research data directory is required")
	}
	if cfg.Compiler == nil {
		cfg.Compiler = &Compiler{}
	}
	if cfg.BandwidthBPS == 0 {
		cfg.BandwidthBPS = 128000
	}
	if cfg.BandwidthBPS < 512 || cfg.BandwidthBPS > 1e9 {
		return nil, errors.New("bandwidth must be 512–1000000000 bps")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, previews: map[string]pending{}, now: time.Now, data: persisted{Version: 1, Revision: 1, Items: []Item{}, History: []revision{}, Active: revision{Engine: "edgeml", Rules: []Rule{}, Matches: map[string]Match{}}}}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "state.json"))
	if err == nil {
		if err = json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("invalid research state: %w", err)
		}
		if s.data.Version != 1 {
			return nil, errors.New("unsupported research state version")
		}
		if err = validateRevision(s.data.Active); err != nil {
			return nil, err
		}
		for _, r := range s.data.History {
			if err = validateRevision(r); err != nil {
				return nil, err
			}
		}
		if len(s.data.Items) > 1000 || len(s.data.History) > 32 {
			return nil, errors.New("research state exceeds configured limits")
		}
		if err = validateItems(s.data.Items, s.now()); err != nil {
			return nil, err
		}
		if err = validateSavedMatches(s.data.Active, s.data.Items); err != nil {
			return nil, err
		}
		for _, r := range s.data.History {
			if err = validateSavedMatches(r, s.data.Items); err != nil {
				return nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func validateRevision(r revision) error {
	if r.Engine != "edgeml" && r.Engine != "tomer" {
		return errors.New("engine must be edgeml or tomer")
	}
	return validateRules(r.Rules)
}
func (s *Service) save(next persisted) error {
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.cfg.DataDir, "state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(s.cfg.DataDir, "state.json")); err != nil {
		return err
	}
	// Rename is the commit point; memory follows it even if directory fsync
	// is unsupported by the filesystem.
	s.data = next
	if dir, e := os.Open(s.cfg.DataDir); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *Service) Compile(ctx context.Context, text string, rules []Rule) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rules == nil {
		rules = s.data.Active.Rules
	}
	return s.cfg.Compiler.Compile(ctx, text, rules)
}
func (s *Service) Preview(ctx context.Context, text, engine string) (Preview, error) {
	return s.PreviewFrom(ctx, text, engine, "")
}

// PreviewFrom supports follow-ups to an unapplied draft. The active revision
// must still match; comparison always remains against the actual active state.
func (s *Service) PreviewFrom(ctx context.Context, text, engine, basePreviewID string) (Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	contextRules := s.data.Active.Rules
	if basePreviewID != "" {
		base, ok := s.previews[basePreviewID]
		if !ok || s.now().UnixMilli()-base.Preview.createdAt > 600000 {
			return Preview{}, ErrNotFound
		}
		if base.Preview.Revision != s.data.Revision {
			return Preview{}, ErrConflict
		}
		contextRules = base.Preview.Draft.Rules
		if engine == "" {
			engine = base.Preview.Engine
		}
	}
	draft, err := s.cfg.Compiler.Compile(ctx, text, contextRules)
	if err != nil {
		return Preview{}, err
	}
	if draft.Operation == "undo" {
		if len(s.data.History) == 0 {
			return Preview{}, errors.New("nothing to undo")
		}
		previous := s.data.History[len(s.data.History)-1]
		draft = makeDraft(append([]Rule{}, previous.Rules...), "rules", "undo")
		engine = previous.Engine
	}
	if engine == "" {
		engine = s.data.Active.Engine
	}
	if err = validateRevision(revision{Engine: engine, Rules: draft.Rules}); err != nil {
		return Preview{}, err
	}
	matches, warnings, err := s.cfg.Vision.Classify(ctx, draft.Rules, s.data.Items)
	if err != nil {
		return Preview{}, err
	}
	now := s.now()
	before, err := Evaluate(ctx, s.data.Items, s.data.Active.Rules, s.data.Active.Matches, s.data.Active.Engine, now, s.cfg.BandwidthBPS)
	if err != nil {
		return Preview{}, err
	}
	after, err := Evaluate(ctx, s.data.Items, draft.Rules, matches, engine, now, s.cfg.BandwidthBPS)
	if err != nil {
		return Preview{}, err
	}
	if engine == "tomer" {
		for _, it := range s.data.Items {
			if it.TomerScore == nil {
				warnings = append(warnings, "Some items lack Tomer image-priority values; production fallback scores are shown.")
				break
			}
		}
	}
	buf := make([]byte, 16)
	if _, err = rand.Read(buf); err != nil {
		return Preview{}, err
	}
	p := Preview{PreviewID: hex.EncodeToString(buf), Revision: s.data.Revision, Engine: engine, Draft: draft, Before: before, After: after, Warnings: warnings, ElapsedMS: time.Since(start).Milliseconds(), createdAt: now.UnixMilli()}
	for id, x := range s.previews {
		if now.UnixMilli()-x.Preview.createdAt > 600000 {
			delete(s.previews, id)
		}
	}
	if len(s.previews) >= 16 {
		s.previews = map[string]pending{}
	}
	s.previews[p.PreviewID] = pending{p, matches, s.data.QueueRevision}
	return p, nil
}

func (s *Service) Apply(ctx context.Context, id string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[id]
	if !ok || s.now().UnixMilli()-p.Preview.createdAt > 600000 {
		return State{}, ErrNotFound
	}
	if p.Preview.Revision != s.data.Revision {
		return State{}, ErrConflict
	}
	matches := p.Matches
	if p.QueueRevision != s.data.QueueRevision {
		var err error
		matches, _, err = s.cfg.Vision.Classify(ctx, p.Preview.Draft.Rules, s.data.Items)
		if err != nil {
			return State{}, err
		}
	}
	next := s.data
	next.Revision++
	next.Active = revision{p.Preview.Engine, append([]Rule{}, p.Preview.Draft.Rules...), matches}
	if p.Preview.Draft.Operation == "undo" {
		if len(next.History) == 0 {
			return State{}, ErrConflict
		}
		next.History = append([]revision{}, next.History[:len(next.History)-1]...)
	} else {
		next.History = append(append([]revision{}, s.data.History...), s.data.Active)
		if len(next.History) > 32 {
			next.History = next.History[1:]
		}
	}
	state, err := s.stateFor(ctx, next)
	if err != nil {
		return State{}, err
	}
	if err = s.save(next); err != nil {
		return State{}, err
	}
	return state, nil
}

// The page previews Undo before Apply. This direct endpoint is also available
// for local recovery tools.
func (s *Service) Undo(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.History) == 0 {
		return State{}, errors.New("nothing to undo")
	}
	next := s.data
	next.Active = next.History[len(next.History)-1]
	next.History = append([]revision{}, next.History[:len(next.History)-1]...)
	next.Revision++
	matches, _, err := s.cfg.Vision.Classify(ctx, next.Active.Rules, next.Items)
	if err != nil {
		return State{}, err
	}
	next.Active.Matches = matches
	state, err := s.stateFor(ctx, next)
	if err != nil {
		return State{}, err
	}
	if err = s.save(next); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Service) Ingest(ctx context.Context, items []Item) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(items) == 0 {
		return State{}, errors.New("no items supplied")
	}
	if len(s.data.Items)+len(items) > 1000 {
		return State{}, errors.New("research queue is capped at 1000 images")
	}
	next := s.data
	next.Items = append(append([]Item{}, s.data.Items...), items...)
	if err := validateItems(next.Items, s.now()); err != nil {
		return State{}, err
	}
	next.QueueRevision++
	matches, _, err := s.cfg.Vision.Classify(ctx, next.Active.Rules, next.Items)
	if err != nil {
		return State{}, err
	}
	next.Active = revision{s.data.Active.Engine, s.data.Active.Rules, matches}
	state, err := s.stateFor(ctx, next)
	if err != nil {
		return State{}, err
	}
	if err = s.save(next); err != nil {
		return State{}, err
	}
	return state, nil
}
func (s *Service) State(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateFor(ctx, s.data)
}
func (s *Service) stateFor(ctx context.Context, data persisted) (State, error) {
	ev, err := Evaluate(ctx, data.Items, data.Active.Rules, data.Active.Matches, data.Active.Engine, s.now(), s.cfg.BandwidthBPS)
	if err != nil {
		return State{}, err
	}
	return State{Revision: data.Revision, Engine: data.Active.Engine, Rules: append([]Rule{}, data.Active.Rules...), Vocabulary: vocabulary(data.Active.Rules), Items: ev.Items, HistoryDepth: len(data.History), LLMConfigured: s.cfg.Compiler.URL != "", VisionConfigured: s.cfg.Vision != nil && s.cfg.Vision.URL != ""}, nil
}
