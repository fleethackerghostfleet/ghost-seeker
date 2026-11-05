package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

var ctx = context.Background()

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func putTest(t *testing.T, s *Store, id string, score float64, age int64, delivered bool) {
	t.Helper()
	_, err := s.Put(ctx, &pb.Item{Id: id, CapturedAtUnixMs: age}, map[pb.FormKind][]byte{
		pb.FormKind_FORM_KIND_FACT: []byte("fact"), pb.FormKind_FORM_KIND_FULL: []byte("full-image"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateScores(ctx, map[string]*pb.Score{id: {Priority: score}}); err != nil {
		t.Fatal(err)
	}
	if delivered {
		ackTest(t, s, id)
	}
}

func ackTest(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.MarkManifestAcked(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Forms {
		if err := s.SetAcked(ctx, id, f.Kind, f.Bytes, true); err != nil {
			t.Fatal(err)
		}
	}
}

func hasTest(t *testing.T, s *Store, id string, want bool) {
	t.Helper()
	_, err := s.Get(ctx, id)
	if want && err != nil {
		t.Fatalf("%s missing: %v", id, err)
	}
	if !want && !errors.Is(err, ErrNotFound) {
		t.Fatalf("%s retained: %v", id, err)
	}
}

func TestEvictionOrderAndAudit(t *testing.T) {
	s := openTest(t)
	putTest(t, s, "low-new", 1, 30, true)
	putTest(t, s, "high-old", 9, 1, true)
	putTest(t, s, "low-old", 1, 10, true)
	if err := s.SetLimits(ctx, Limits{MaxItems: 1}); err != nil {
		t.Fatal(err)
	}
	hasTest(t, s, "high-old", true)
	q, err := s.db.Query(`SELECT item_id,bytes,score,reason,ts FROM eviction_log ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, want := range []string{"low-old", "low-new"} {
		if !q.Next() {
			t.Fatal("missing audit entry")
		}
		var id, reason string
		var bytes, at int64
		var score float64
		if err := q.Scan(&id, &bytes, &score, &reason, &at); err != nil {
			t.Fatal(err)
		}
		if id != want || bytes != 14 || score != 1 || reason != "storage_cap" || at <= 0 {
			t.Fatalf("bad eviction: %s %d %v %s %d", id, bytes, score, reason, at)
		}
		if _, err := os.Stat(filepath.Join(s.blobDir, id)); !os.IsNotExist(err) {
			t.Fatalf("payload retained: %v", err)
		}
	}
	if q.Next() {
		t.Fatal("extra eviction")
	}
}

func TestProtectionRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		protect func(*testing.T, *Store)
	}{
		{"undelivered", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE forms SET complete=0,acked_through=0`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"partial", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE forms SET complete=0,acked_through=3 WHERE kind=?`, int(pb.FormKind_FORM_KIND_FULL))
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"fact only delivered", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE forms SET complete=0,acked_through=0 WHERE kind=?`, int(pb.FormKind_FORM_KIND_FULL))
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"manifest unacked", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE items SET manifest_acked=0`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"inconsistent ack", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE forms SET acked_through=1`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"not ready", func(t *testing.T, s *Store) {
			_, err := s.db.Exec(`UPDATE forms SET ready=0`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"pinned", func(t *testing.T, s *Store) {
			if err := s.SetPinned(ctx, "protected", true); err != nil {
				t.Fatal(err)
			}
		}},
		{"pulled", func(t *testing.T, s *Store) {
			if err := s.SetPull(ctx, "protected", pb.FormKind_FORM_KIND_FULL, 100, "ops"); err != nil {
				t.Fatal(err)
			}
		}},
		{"zero boost pull", func(t *testing.T, s *Store) {
			if err := s.SetPull(ctx, "protected", pb.FormKind_FORM_KIND_FULL, 0, "ops"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			putTest(t, s, "protected", 0, 1, true)
			tc.protect(t, s)
			putTest(t, s, "safe", 10, 2, true)
			if err := s.SetLimits(ctx, Limits{MaxItems: 1}); err != nil {
				t.Fatal(err)
			}
			hasTest(t, s, "protected", true)
			hasTest(t, s, "safe", false)
			if _, err := s.Put(ctx, &pb.Item{Id: "new"}, nil); !errors.Is(err, ErrFull) {
				t.Fatalf("want backpressure: %v", err)
			}
		})
	}
}

func TestAdmissionBackpressureAndRecovery(t *testing.T) {
	s := openTest(t)
	if err := s.SetLimits(ctx, Limits{MaxBytes: 14, MaxItems: 1}); err != nil {
		t.Fatal(err)
	}
	putTest(t, s, "first", 1, 1, false)
	_, err := s.Put(ctx, &pb.Item{Id: "new"}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: []byte("data")})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.blobDir, "new")); !os.IsNotExist(err) {
		t.Fatalf("rejected item wrote blobs: %v", err)
	}
	if n, _ := s.ItemsEvicted(ctx); n != 0 {
		t.Fatal(n)
	}
	ackTest(t, s, "first")
	if _, err := s.Put(ctx, &pb.Item{Id: "new"}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FACT: []byte("data")}); err != nil {
		t.Fatal(err)
	}
	hasTest(t, s, "first", false)
	hasTest(t, s, "new", true)
	if n, _ := s.ItemsEvicted(ctx); n != 1 {
		t.Fatal(n)
	}
}

func TestByteCapAndImpossibleAdmission(t *testing.T) {
	s := openTest(t)
	putTest(t, s, "first", 1, 1, true)
	putTest(t, s, "second", 2, 2, true)
	if err := s.SetLimits(ctx, Limits{MaxBytes: 20}); err != nil {
		t.Fatal(err)
	}
	hasTest(t, s, "first", false)
	hasTest(t, s, "second", true)
	if _, err := s.Put(ctx, &pb.Item{Id: "too-large"}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: make([]byte, 21)}); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	hasTest(t, s, "second", true)
}

func TestUnlimitedAndProtectedOversizedBacklog(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"a", "b", "c"} {
		putTest(t, s, id, 1, 1, false)
	}
	if err := s.EnforceLimits(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ItemsEvicted(ctx); n != 0 {
		t.Fatal(n)
	}
	if err := s.SetLimits(ctx, Limits{MaxItems: 1}); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		hasTest(t, s, id, true)
	}
	ackTest(t, s, "a")
	ackTest(t, s, "b")
	if err := s.EnforceLimits(ctx); err != nil {
		t.Fatal(err)
	}
	hasTest(t, s, "c", true)
	if n, _ := s.ItemsEvicted(ctx); n != 2 {
		t.Fatal(n)
	}
	if err := s.SetLimits(ctx, Limits{MaxItems: -1}); err == nil {
		t.Fatal("negative cap accepted")
	}
}

func TestReplacementCannotEvictItselfOrExceedCap(t *testing.T) {
	s := openTest(t)
	putTest(t, s, "a", 1, 1, true)
	if err := s.SetLimits(ctx, Limits{MaxBytes: 14}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceForm(ctx, "a", pb.FormKind_FORM_KIND_FULL, make([]byte, 11)); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	data, err := s.ReadFormAll("a", pb.FormKind_FORM_KIND_FULL)
	if err != nil || string(data) != "full-image" {
		t.Fatalf("modified on rejection: %q %v", data, err)
	}
	if _, err := s.ReplaceForm(ctx, "a", pb.FormKind_FORM_KIND_FULL, []byte("small")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if r.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatal("replacement still acked")
	}
	if _, err := s.Put(ctx, &pb.Item{Id: "a"}, nil); err == nil {
		t.Fatal("accepted duplicate id")
	}
	if _, err := s.ReadFormAll("a", pb.FormKind_FORM_KIND_FACT); err != nil {
		t.Fatal("duplicate removed original", err)
	}
}

func TestPersistenceAndCrashCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	putTest(t, s, "pin", 1, 1, true)
	putTest(t, s, "evict", 1, 1, true)
	if err := s.SetPinned(ctx, "pin", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLimits(ctx, Limits{MaxItems: 1}); err != nil {
		t.Fatal(err)
	}
	// Simulate the committed metadata/pending unlink state after a crash.
	if err := os.MkdirAll(filepath.Join(s.blobDir, "evict"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.blobDir, "evict", "payload"), []byte("leftover"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE eviction_log SET cleanup_pending=1`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.ItemsEvicted(ctx); n != 1 {
		t.Fatal(n)
	}
	if _, err := os.Stat(filepath.Join(s.blobDir, "evict")); !os.IsNotExist(err) {
		t.Fatal("cleanup not recovered", err)
	}
	if err := s.SetLimits(ctx, Limits{MaxBytes: 1}); !errors.Is(err, ErrFull) {
		t.Fatal("pin lost", err)
	}
	if err := s.SetPinned(ctx, "pin", false); err != nil {
		t.Fatal(err)
	}
	if err := s.EnforceLimits(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ItemsEvicted(ctx); n != 2 {
		t.Fatal(n)
	}
}

func TestConcurrentAdmissionStaysUnderCap(t *testing.T) {
	s := openTest(t)
	if err := s.SetLimits(ctx, Limits{MaxItems: 1}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.Put(ctx, &pb.Item{Id: id}, nil)
			if err != nil && !errors.Is(err, ErrFull) {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	rows, err := s.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal(len(rows))
	}
}
