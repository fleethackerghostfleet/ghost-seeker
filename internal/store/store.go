package store

import (
	"errors"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

const MaxActiveDirectives = 20

var ErrTooManyActiveDirectives = errors.New("Too many active directives — remove one first")

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
		oldest := 0
		for i := 1; i < len(s.items); i++ {
			if s.items[i].TS.Before(s.items[oldest].TS) {
				oldest = i
			}
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
			} else {
				s.cdr--
			}
			item.Sent = *sent
		}
		command.Rescore(item, s.model, s.directives)
		return cloneItem(*item), s.cdr, true
	}
	return command.Item{}, s.cdr, false
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

func (s *Store) RescoreTimeScoped(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, directive := range s.directives {
		for _, rule := range directive.Rules {
			if rule.WithinMin > 0 {
				for i := range s.items {
					command.RescoreAt(&s.items[i], s.model, s.directives, now)
				}
				return true
			}
		}
	}
	return false
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
			copyOf[i].Rules[j].Sources = append([]string(nil), directive.Rules[j].Sources...)
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
