package policy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

func TestBackendsCancellationAndPolicyIsolation(t *testing.T) {
	file, err := NewFileBackend(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]Backend{"memory": NewMemBackend(), "file": file} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			p := &pb.Policy{Id: "retained", Version: 9, Text: "original"}
			if err := b.SavePolicyState(ctx, p, 9); err != nil {
				t.Fatal(err)
			}
			p.Text = "mutated input"
			loaded, high, err := b.LoadPolicyState(ctx)
			if err != nil || high != 9 || loaded.Text != "original" {
				t.Fatalf("loaded=%v high=%d err=%v", loaded, high, err)
			}
			loaded.Text = "mutated output"
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := b.SavePolicyState(canceled, nil, 999); !errors.Is(err, context.Canceled) {
				t.Fatalf("save=%v", err)
			}
			if _, _, err := b.LoadPolicyState(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("load=%v", err)
			}
			if err := b.AppendPolicyAudit(canceled, AuditEntry{PolicyID: "bad"}); !errors.Is(err, context.Canceled) {
				t.Fatalf("append=%v", err)
			}
			if _, err := b.ListPolicyAudit(canceled, 0); !errors.Is(err, context.Canceled) {
				t.Fatalf("list=%v", err)
			}
			again, high, err := b.LoadPolicyState(ctx)
			if err != nil || high != 9 || again.Text != "original" {
				t.Fatalf("state changed=%v high=%d err=%v", again, high, err)
			}
			if entries, err := b.ListPolicyAudit(ctx, 0); err != nil || len(entries) != 0 {
				t.Fatalf("canceled append persisted: %+v %v", entries, err)
			}
			for _, id := range []string{"old", "new"} {
				if err := b.AppendPolicyAudit(ctx, AuditEntry{PolicyID: id, Result: "applied"}); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := b.ListPolicyAudit(ctx, 1)
			if err != nil || len(entries) != 1 || entries[0].PolicyID != "new" {
				t.Fatalf("limited=%+v %v", entries, err)
			}
			entries[0].PolicyID = "mutated result"
			entries, err = b.ListPolicyAudit(ctx, 0)
			if err != nil || len(entries) != 2 || entries[0].PolicyID != "new" || entries[1].PolicyID != "old" {
				t.Fatalf("full=%+v %v", entries, err)
			}
			if err := b.SavePolicyState(ctx, nil, 12); err != nil {
				t.Fatal(err)
			}
			p, high, err = b.LoadPolicyState(ctx)
			if err != nil || p != nil || high != 12 {
				t.Fatalf("empty policy=%v high=%d err=%v", p, high, err)
			}
		})
	}
}

func TestFileBackendCorruptAndUnavailableStorage(t *testing.T) {
	if _, err := NewFileBackend(""); err == nil {
		t.Fatal("empty state path accepted")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.json")
	b, err := NewFileBackend(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"not JSON", `{"highWater":1,"lastGood":{"unexpectedField":true}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.LoadPolicyState(ctx); err == nil {
			t.Fatalf("corrupt state accepted: %s", raw)
		}
	}
	if err := os.WriteFile(path, []byte(`{"highWater":17}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, high, err := b.LoadPolicyState(ctx)
	if err != nil || p != nil || high != 17 {
		t.Fatalf("absent policy=%v high=%d err=%v", p, high, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SavePolicyState(ctx, &pb.Policy{Text: string([]byte{0xff})}, 18); err == nil {
		t.Fatal("invalid UTF-8 policy saved")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("marshal failure changed state")
	}
	blocked, err := NewFileBackend(filepath.Join(path, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.SavePolicyState(ctx, nil, 1); err == nil {
		t.Fatal("non-directory parent accepted")
	}
	if err := blocked.AppendPolicyAudit(ctx, AuditEntry{}); err == nil {
		t.Fatal("audit parent file accepted")
	}
	dir := filepath.Join(t.TempDir(), "state-directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := NewFileBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := directory.LoadPolicyState(ctx); err == nil {
		t.Fatal("directory loaded")
	}
	if err := directory.SavePolicyState(ctx, nil, 1); err == nil {
		t.Fatal("state renamed over directory")
	}
	left, err := filepath.Glob(filepath.Join(filepath.Dir(dir), ".policy-state-*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("temporary state leaked: %v %v", left, err)
	}
	if err := os.Mkdir(path+".audit.jsonl", 0700); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendPolicyAudit(ctx, AuditEntry{}); err == nil {
		t.Fatal("audit directory written")
	}
	if _, err := b.ListPolicyAudit(ctx, 0); err == nil {
		t.Fatal("audit directory read")
	}
}

func TestAuditScannerValidRecordSizeBoundary(t *testing.T) {
	const limit = 4 << 20
	for _, tc := range []struct {
		name string
		size int
	}{{"under limit", limit - 1}, {"over limit", limit + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			size := tc.size
			path := filepath.Join(t.TempDir(), "state.json")
			b, err := NewFileBackend(path)
			if err != nil {
				t.Fatal(err)
			}
			entry := AuditEntry{PolicyID: "size-control", Result: "applied"}
			empty, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			entry.Text = strings.Repeat("x", size-len(empty)-1)
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			if len(raw) != size {
				t.Fatalf("size=%d want=%d", len(raw), size)
			}
			var control AuditEntry
			if err := json.Unmarshal(raw, &control); err != nil || control.PolicyID != "size-control" || control.Text != entry.Text {
				t.Fatalf("invalid fixture: %v", err)
			}
			if err := os.WriteFile(path+".audit.jsonl", raw, 0600); err != nil {
				t.Fatal(err)
			}
			entries, err := b.ListPolicyAudit(context.Background(), 0)
			if size < limit {
				if err != nil || len(entries) != 1 || entries[0].Text != entry.Text || entries[0].PolicyID != entry.PolicyID {
					t.Fatalf("under-limit records=%d err=%v", len(entries), err)
				}
			} else if !errors.Is(err, bufio.ErrTooLong) {
				t.Fatalf("overflow=%v want=%v", err, bufio.ErrTooLong)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "state.json")
	b, err := NewFileBackend(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".audit.jsonl", []byte("{}\ninvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListPolicyAudit(context.Background(), 0); err == nil {
		t.Fatal("malformed audit accepted")
	}
}

type saveErrorBackend struct {
	Backend
	err error
}

func (b *saveErrorBackend) SavePolicyState(ctx context.Context, p *pb.Policy, h int64) error {
	if b.err != nil {
		return b.err
	}
	return b.Backend.SavePolicyState(ctx, p, h)
}

func TestStoreFailedSavePreservesPolicyAndEmptyStatePaths(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := NewStore(canceled, nil, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("load=%v", err)
	}
	b := &saveErrorBackend{Backend: NewMemBackend()}
	s, err := NewStore(ctx, b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p, expired := s.Active(now); p != nil || expired || s.LastVersion() != 0 {
		t.Fatalf("empty=%v expired=%v", p, expired)
	}
	if err := s.MarkExpired(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptLocal(ctx, nil, "ui", now); err == nil {
		t.Fatal("nil local accepted")
	}
	p := &pb.Policy{Id: "retained", Version: 10}
	accepted, err := s.Accept(ctx, p, "ui", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.IssuedAtUnixMs != 0 || accepted.IssuedAtUnixMs != now.UnixMilli() {
		t.Fatal("timestamp did not apply to clone")
	}
	b.err = errors.New("disk unavailable")
	if _, err := s.Accept(ctx, &pb.Policy{Id: "replacement", Version: 11}, "ui", now); !errors.Is(err, b.err) {
		t.Fatalf("save=%v", err)
	}
	active, expired := s.Active(now)
	if expired || !proto.Equal(active, accepted) || s.LastVersion() != 10 {
		t.Fatalf("failed save changed state: %v", active)
	}
	if err := s.MarkExpired(canceled, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("expiry audit=%v", err)
	}
	if !Expiry(nil, 0).IsZero() || !Expiry(accepted, 0).Equal(now.Add(DefaultTTL)) {
		t.Fatal("default expiry changed")
	}
	entry := auditEntry(&pb.Policy{Conflicts: []*pb.MergeConflict{nil, {RuleId: "rule", Reason: "locked"}}}, "ui", now, "rejected", "conflict")
	if len(entry.Conflicts) != 1 || entry.Conflicts[0] != "rule: locked" {
		t.Fatalf("conflicts=%+v", entry)
	}
}

func TestCommandRulesWarningsRespectEchelonAndLeaveInputUntouched(t *testing.T) {
	locks := []Lock{{RuleID: "hq", LockedBy: "fleet", Echelon: pb.Echelon_ECHELON_COMMAND, BoostLabels: []string{"boat"}, Weight: 3}}
	rules := []RuleLabels{{Type: "ignored", Classes: []string{"boat"}}, {Type: "boost", Weight: 1, Classes: []string{"boat"}}, {Type: "tier", Tier: 4, Attrs: []string{"boat"}}}
	warnings, blocked := CheckCommandRules(locks, pb.Echelon_ECHELON_SHIP, rules)
	if !blocked || len(warnings) != 2 {
		t.Fatalf("warnings=%v blocked=%v", warnings, blocked)
	}
	for _, warning := range warnings {
		if !strings.Contains(warning, "fleet") || !strings.Contains(warning, "ship level") {
			t.Fatalf("warning=%s", warning)
		}
	}
	if warnings, blocked := CheckCommandRules(locks, pb.Echelon_ECHELON_COMMAND, rules); blocked || len(warnings) != 0 {
		t.Fatalf("peer blocked=%v %v", blocked, warnings)
	}
	if len(rules[1].Classes) != 1 || len(rules[2].Attrs) != 1 {
		t.Fatal("command inputs mutated")
	}
}
