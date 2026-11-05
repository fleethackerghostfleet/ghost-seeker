package policy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Backend interface {
	LoadPolicyState(ctx context.Context) (lastGood *pb.Policy, highWater int64, err error)
	SavePolicyState(ctx context.Context, lastGood *pb.Policy, highWater int64) error
	AppendPolicyAudit(ctx context.Context, entry AuditEntry) error
	ListPolicyAudit(ctx context.Context, limit int) ([]AuditEntry, error)
}

type MemBackend struct {
	mu        sync.Mutex
	lastGood  *pb.Policy
	highWater int64
	audit     []AuditEntry
}

func NewMemBackend() *MemBackend { return &MemBackend{} }

func (b *MemBackend) LoadPolicyState(ctx context.Context) (*pb.Policy, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return clonePolicy(b.lastGood), b.highWater, nil
}

func (b *MemBackend) SavePolicyState(ctx context.Context, lastGood *pb.Policy, highWater int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastGood, b.highWater = clonePolicy(lastGood), highWater
	return nil
}

func (b *MemBackend) AppendPolicyAudit(ctx context.Context, entry AuditEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.audit = append(b.audit, entry)
	return nil
}

func (b *MemBackend) ListPolicyAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return newestFirst(b.audit, limit), nil
}

type FileBackend struct {
	mu   sync.Mutex
	path string
}

func NewFileBackend(path string) (*FileBackend, error) {
	if path == "" {
		return nil, errors.New("policy state path required")
	}
	return &FileBackend{path: path}, nil
}

func (b *FileBackend) LoadPolicyState(ctx context.Context) (*pb.Policy, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var state struct {
		LastGood  json.RawMessage `json:"lastGood"`
		HighWater int64           `json:"highWater"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, 0, err
	}
	if len(state.LastGood) == 0 || string(state.LastGood) == "null" {
		return nil, state.HighWater, nil
	}
	p := &pb.Policy{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(state.LastGood, p); err != nil {
		return nil, 0, err
	}
	return p, state.HighWater, nil
}

func (b *FileBackend) SavePolicyState(ctx context.Context, lastGood *pb.Policy, highWater int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var encoded json.RawMessage
	if lastGood != nil {
		raw, err := protojson.Marshal(lastGood)
		if err != nil {
			return err
		}
		encoded = raw
	}
	raw, err := json.Marshal(struct {
		LastGood  json.RawMessage `json:"lastGood"`
		HighWater int64           `json:"highWater"`
	}{LastGood: encoded, HighWater: highWater})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(b.path), ".policy-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, b.path)
}

func (b *FileBackend) AppendPolicyAudit(ctx context.Context, entry AuditEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(b.path+".audit.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(entry); err != nil {
		return err
	}
	return f.Sync()
}

func (b *FileBackend) ListPolicyAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := os.Open(b.path + ".audit.jsonl")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []AuditEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var entry AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return newestFirst(entries, limit), nil
}

func clonePolicy(p *pb.Policy) *pb.Policy {
	if p == nil {
		return nil
	}
	return proto.Clone(p).(*pb.Policy)
}

func newestFirst(entries []AuditEntry, limit int) []AuditEntry {
	out := make([]AuditEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		out = append(out, entries[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
