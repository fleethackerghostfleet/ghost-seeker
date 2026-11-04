package store

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

const MaxActiveDirectives = 20

var ErrTooManyActiveDirectives = errors.New("Too many active directives — remove one first")

// Decisions are the commander's final-review outcomes. DecisionReturn hands
// the image back to the watch floor for another look.
var Decisions = map[string]bool{"action": true, "track": true, "intel": true, "close": true, DecisionReturn: true}

const (
	DecisionReturn = "return"
	maxNoteRunes   = 280
)

var (
	ErrNotFound        = errors.New("no such item")
	ErrNotEscalated    = errors.New("item has not been sent to the commander")
	ErrUnknownDecision = errors.New("unknown decision")
)

type State struct {
	Model      string              `json:"model"`
	Directives []command.Directive `json:"directives"`
}

type Store struct {
	mu         sync.RWMutex
	retain     int
	items      []command.Item
	stats      command.Stats
	cdr        int
	model      string
	directives []command.Directive
}

func New(retain int) *Store {
	if retain < 1 {
		retain = 1
	}
	return &Store{retain: retain, model: "maritime"}
}

func (s *Store) Add(item *command.Item, extra int) (command.Item, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Cap += extra + 1
	s.stats.Drop += extra
	if item == nil {
		s.stats.Drop++
		return command.Item{}, ""
	}
	s.stats.Keep++
	s.stats.Bytes += item.KB
	s.stats.DB++
	item.DBID = s.stats.DB + 180000
	command.Rescore(item, s.model, s.directives)
	s.items = append(s.items, cloneItem(*item))
	evicted := ""
	if len(s.items) > s.retain {
		oldest := oldestIndex(s.items, func(it command.Item) bool { return !it.Sent && it.Decision == "" })
		if oldest < 0 {
			oldest = oldestIndex(s.items, func(command.Item) bool { return true })
		}
		evicted = s.items[oldest].ID
		s.items = append(s.items[:oldest], s.items[oldest+1:]...)
	}
	return cloneItem(*item), evicted
}

func (s *Store) RecordDropped(extra int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Cap += extra + 1
	s.stats.Drop += extra + 1
}

func (s *Store) Snapshot() ([]command.Item, command.Stats, int, State) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]command.Item, len(s.items))
	for i, item := range s.items {
		items[i] = cloneItem(item)
	}
	return items, s.stats, s.cdr, State{Model: s.model, Directives: cloneDirectives(s.directives)}
}

func (s *Store) Stats() (command.Stats, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats, s.cdr
}

func (s *Store) Items() []command.Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]command.Item, len(s.items))
	for i, item := range s.items {
		items[i] = cloneItem(item)
	}
	return items
}

func (s *Store) Model() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

func (s *Store) SetModel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = id
	s.rescoreLocked()
}

func (s *Store) AddDirective(text string, rules []command.Rule) (command.Directive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.directives) >= MaxActiveDirectives {
		return command.Directive{}, ErrTooManyActiveDirectives
	}
	directive := command.Directive{ID: time.Now().UTC().Format("d20060102150405.000000000"), Text: text, Rules: append([]command.Rule(nil), rules...)}
	for i := range directive.Rules {
		directive.Rules[i].DirectiveID = directive.ID
	}
	s.directives = append(s.directives, directive)
	s.rescoreLocked()
	return cloneDirectives([]command.Directive{directive})[0], nil
}

func (s *Store) DeleteDirective(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, directive := range s.directives {
		if directive.ID == id {
			s.directives = append(s.directives[:i], s.directives[i+1:]...)
			s.rescoreLocked()
			return true
		}
	}
	return false
}

func (s *Store) ClearDirectives() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directives = nil
	s.rescoreLocked()
}

func (s *Store) PatchItem(id string, manual *int, reviewed, dismissed, sent *bool) (command.Item, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		item := &s.items[i]
		if item.ID != id {
			continue
		}
		if manual != nil {
			item.Manual = *manual
		}
		if reviewed != nil {
			item.Reviewed = *reviewed
		}
		if dismissed != nil {
			item.Dismissed = *dismissed
		}
		if sent != nil && item.Sent != *sent {
			if *sent {
				s.cdr++
				now := time.Now()
				item.SentAt, item.Decision, item.DecisionNote, item.DecidedAt = &now, "", "", nil
			} else {
				s.cdr--
				item.SentAt = nil
			}
			item.Sent = *sent
		}
		command.Rescore(item, s.model, s.directives)
		return cloneItem(*item), s.cdr, true
	}
	return command.Item{}, s.cdr, false
}

// Decide records the commander's decision on an escalated item. An empty
// decision reopens it; DecisionReturn sends it back to the watch floor.
func (s *Store) Decide(id, decision, note string) (command.Item, int, error) {
	if decision != "" && !Decisions[decision] {
		return command.Item{}, 0, ErrUnknownDecision
	}
	if r := []rune(strings.TrimSpace(note)); len(r) > maxNoteRunes {
		note = string(r[:maxNoteRunes])
	} else {
		note = string(r)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		item := &s.items[i]
		if item.ID != id {
			continue
		}
		if !item.Sent && item.Decision != DecisionReturn {
			return command.Item{}, s.cdr, ErrNotEscalated
		}
		now := time.Now()
		switch {
		case decision == DecisionReturn && item.Sent:
			item.Sent, item.SentAt, item.Reviewed = false, nil, false
			s.cdr--
		case decision != DecisionReturn && !item.Sent:
			item.Sent, item.SentAt, item.Reviewed = true, &now, true
			s.cdr++
		}
		item.Decision, item.DecisionNote, item.DecidedAt = decision, note, &now
		if decision == "" {
			item.DecisionNote, item.DecidedAt = "", nil
		}
		return cloneItem(*item), s.cdr, nil
	}
	return command.Item{}, s.cdr, ErrNotFound
}

// AutoEscalate stands in for a watch officer: it confirms the highest-scoring
// unreviewed P1/P2 item at its model tier and sends it to the commander.
func (s *Store) AutoEscalate() (command.Item, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := -1
	for i, item := range s.items {
		if item.Reviewed || item.Dismissed || item.Sent || item.Decision != "" || item.Tier > 2 {
			continue
		}
		if best < 0 || item.Central > s.items[best].Central {
			best = i
		}
	}
	if best < 0 {
		return command.Item{}, s.cdr, false
	}
	item := &s.items[best]
	now := time.Now()
	item.Manual, item.Reviewed, item.Sent, item.SentAt = item.Tier, true, true, &now
	s.cdr++
	command.Rescore(item, s.model, s.directives)
	return cloneItem(*item), s.cdr, true
}

func oldestIndex(items []command.Item, eligible func(command.Item) bool) int {
	oldest := -1
	for i, item := range items {
		if eligible(item) && (oldest < 0 || item.TS.Before(items[oldest].TS)) {
			oldest = i
		}
	}
	return oldest
}

func (s *Store) GetItem(id string) (command.Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if item.ID == id {
			return cloneItem(item), true
		}
	}
	return command.Item{}, false
}

func (s *Store) rescoreLocked() {
	for i := range s.items {
		command.Rescore(&s.items[i], s.model, s.directives)
	}
}

func cloneItem(item command.Item) command.Item {
	item.Dets = append([]command.Detection(nil), item.Dets...)
	item.Det = append([]command.DetectionScore(nil), item.Det...)
	for i := range item.Det {
		notes := make([]command.Rule, len(item.Det[i].Notes))
		copy(notes, item.Det[i].Notes)
		item.Det[i].Notes = notes
	}
	return item
}

func cloneDirectives(directives []command.Directive) []command.Directive {
	copyOf := make([]command.Directive, len(directives))
	for i, directive := range directives {
		copyOf[i] = directive
		copyOf[i].Rules = append([]command.Rule(nil), directive.Rules...)
		for j := range copyOf[i].Rules {
			classes := make([]string, len(directive.Rules[j].Classes))
			copy(classes, directive.Rules[j].Classes)
			copyOf[i].Rules[j].Classes = classes
			attrs := make([]string, len(directive.Rules[j].Attrs))
			copy(attrs, directive.Rules[j].Attrs)
			copyOf[i].Rules[j].Attrs = attrs
		}
	}
	return copyOf
}
