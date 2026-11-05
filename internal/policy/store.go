package policy

import (
	"context"
	"errors"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

const DefaultTTL = 12 * time.Hour

type AuditEntry struct {
	AtUnixMs  int64    `json:"atUnixMs"`
	Who       string   `json:"who"`
	Source    string   `json:"source"`
	PolicyID  string   `json:"policyId"`
	Version   int64    `json:"version"`
	Text      string   `json:"text"`
	Result    string   `json:"result"`
	Reason    string   `json:"reason,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	Signed    bool     `json:"signed"`
	KeyID     string   `json:"keyId,omitempty"`
}

type Options struct {
	Keys          Keyring
	RequireSigned bool
	TTL           time.Duration
}

type Store struct {
	mu        sync.Mutex
	backend   Backend
	options   Options
	lastGood  *pb.Policy
	highWater int64
}

var (
	ErrStaleVersion = errors.New("stale policy version")
	ErrExpired      = errors.New("policy expired")
)

func NewStore(ctx context.Context, backend Backend, options Options) (*Store, error) {
	if backend == nil {
		backend = NewMemBackend()
	}
	if options.TTL == 0 {
		options.TTL = DefaultTTL
	}
	p, highWater, err := backend.LoadPolicyState(ctx)
	if err != nil {
		return nil, err
	}
	return &Store{backend: backend, options: options, lastGood: clonePolicy(p), highWater: highWater}, nil
}

func (s *Store) Accept(ctx context.Context, p *pb.Policy, source string, now time.Time) (*pb.Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == nil {
		err := errors.New("policy required")
		_ = s.appendAudit(ctx, auditEntry(nil, source, now, "rejected", err.Error()))
		return nil, err
	}
	if p.Signature != nil && len(p.Signature.Ed25519) > 0 {
		if err := Verify(p, s.options.Keys); err != nil {
			_ = s.appendAudit(ctx, auditEntry(p, source, now, "rejected", err.Error()))
			return nil, err
		}
	} else if s.options.RequireSigned && source == "downlink" {
		_ = s.appendAudit(ctx, auditEntry(p, source, now, "rejected", ErrUnsigned.Error()))
		return nil, ErrUnsigned
	}
	if p.Version != 0 && p.Version <= s.highWater {
		_ = s.appendAudit(ctx, auditEntry(p, source, now, "rejected", ErrStaleVersion.Error()))
		return nil, ErrStaleVersion
	}
	accepted := proto.Clone(p).(*pb.Policy)
	if accepted.IssuedAtUnixMs == 0 {
		accepted.IssuedAtUnixMs = now.UnixMilli()
	}
	if expiry := Expiry(accepted, s.options.TTL); !expiry.IsZero() && !expiry.After(now) {
		_ = s.appendAudit(ctx, auditEntry(accepted, source, now, "rejected", ErrExpired.Error()))
		return nil, ErrExpired
	}
	nextHighWater := s.highWater
	if accepted.Version > nextHighWater {
		nextHighWater = accepted.Version
	}
	if err := s.backend.SavePolicyState(ctx, accepted, nextHighWater); err != nil {
		return nil, err
	}
	s.lastGood, s.highWater = clonePolicy(accepted), nextHighWater
	if err := s.appendAudit(ctx, auditEntry(accepted, source, now, "applied", "")); err != nil {
		return clonePolicy(accepted), err
	}
	return clonePolicy(accepted), nil
}

func (s *Store) Reject(ctx context.Context, p *pb.Policy, source, reason string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.appendAudit(ctx, auditEntry(p, source, now, "rejected", reason))
}

func (s *Store) Active(now time.Time) (*pb.Policy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastGood == nil {
		return nil, false
	}
	p := clonePolicy(s.lastGood)
	expiry := Expiry(p, s.options.TTL)
	if !expiry.IsZero() && !expiry.After(now) {
		return nil, true
	}
	return p, false
}

func (s *Store) MarkExpired(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastGood == nil {
		return nil
	}
	entries, err := s.backend.ListPolicyAudit(ctx, 0)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Result == "expired" && entry.PolicyID == s.lastGood.Id && entry.Version == s.lastGood.Version {
			return nil
		}
	}
	return s.appendAudit(ctx, auditEntry(s.lastGood, "system", now, "expired", "policy TTL elapsed"))
}

func (s *Store) HighWater() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.highWater
}

func (s *Store) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.backend.ListPolicyAudit(ctx, limit)
}

func Expiry(p *pb.Policy, ttl time.Duration) time.Time {
	if p == nil {
		return time.Time{}
	}
	if p.ExpiresAtUnixMs > 0 {
		return time.UnixMilli(p.ExpiresAtUnixMs)
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return time.UnixMilli(p.IssuedAtUnixMs).Add(ttl)
}

func (s *Store) appendAudit(ctx context.Context, entry AuditEntry) error {
	return s.backend.AppendPolicyAudit(ctx, entry)
}

func auditEntry(p *pb.Policy, source string, now time.Time, result, reason string) AuditEntry {
	entry := AuditEntry{AtUnixMs: now.UnixMilli(), Source: source, Result: result, Reason: reason}
	if p == nil {
		return entry
	}
	entry.Who, entry.PolicyID, entry.Version, entry.Text = p.IssuedBy, p.Id, p.Version, p.Text
	if p.Signature != nil && len(p.Signature.Ed25519) > 0 {
		entry.Signed, entry.KeyID = true, p.Signature.KeyId
	}
	for _, conflict := range p.Conflicts {
		if conflict != nil {
			entry.Conflicts = append(entry.Conflicts, conflict.RuleId+": "+conflict.Reason)
		}
	}
	return entry
}
