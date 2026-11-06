package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/proto"
)

func TestHashLookupAndBoundedFormReads(t *testing.T) {
	s := openTest(t)
	if ids, err := s.IDsByHash(ctx, "missing"); err != nil || len(ids) != 0 {
		t.Fatalf("missing ids=%v err=%v", ids, err)
	}
	if id, found, err := s.HasHash(ctx, "missing"); err != nil || found || id != "" {
		t.Fatalf("missing hash: id=%q found=%v err=%v", id, found, err)
	}
	data := []byte("0123456789")
	for _, id := range []string{"z-first", "a-second"} {
		if _, err := s.Put(ctx, &pb.Item{Id: id, ContentHash: "shared"}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: data}); err != nil {
			t.Fatal(err)
		}
	}
	if ids, err := s.IDsByHash(ctx, "shared"); err != nil || !reflect.DeepEqual(ids, []string{"z-first", "a-second"}) {
		t.Fatalf("hash order=%v err=%v", ids, err)
	}
	if id, found, err := s.HasHash(ctx, "shared"); err != nil || !found || (id != "z-first" && id != "a-second") {
		t.Fatalf("hash lookup: id=%q found=%v err=%v", id, found, err)
	}
	for _, tc := range []struct {
		offset, size int64
		want         string
	}{
		{0, 4, "0123"}, {8, 9, "89"}, {10, 4, ""}, {100, 4, ""}, {1, 0, ""},
	} {
		got, err := s.ReadForm("z-first", pb.FormKind_FORM_KIND_FULL, tc.offset, tc.size)
		if err != nil || string(got) != tc.want {
			t.Fatalf("read offset=%d size=%d got=%q err=%v", tc.offset, tc.size, got, err)
		}
	}
	if _, err := s.ReadForm("z-first", pb.FormKind_FORM_KIND_FULL, -1, 2); err == nil {
		t.Fatal("negative seek accepted")
	}
	if _, err := s.ReadForm("missing", pb.FormKind_FORM_KIND_FULL, 0, 2); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing form err=%v", err)
	}
	row, err := s.Get(ctx, "z-first")
	if err != nil {
		t.Fatal(err)
	}
	if row.Form(pb.FormKind_FORM_KIND_FACT) != nil {
		t.Fatal("found unannounced form")
	}
}

func TestDeliveryStateTransitionsPersist(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	for _, id := range []string{"first", "second"} {
		putTest(t, s, id, 0, 1, false)
	}
	at := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := s.MarkManifestSent(ctx, nil, at); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkManifestSent(ctx, []string{"first", "second"}, at); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkManifestAcked(ctx, []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearManifestSent(ctx, []string{"first", "second"}); err != nil {
		t.Fatal(err)
	}
	score := &pb.Score{Priority: 0.9, Suppressed: true}
	if err := s.UpdateScores(ctx, map[string]*pb.Score{"first": score}); err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	row.Item.Features = &pb.Features{LabelOverride: "confirmed-boat", OverrideBy: "watchstander"}
	if err := s.UpdateItem(ctx, row.Item); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPull(ctx, "first", pb.FormKind_FORM_KIND_FULL, 42, "watchstander", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAcked(ctx, "first", pb.FormKind_FORM_KIND_FULL, 4, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAcked(ctx, "first", pb.FormKind_FORM_KIND_FULL, 2, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.All(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	first, second := rows[0], rows[1]
	if first.Item.Id != "first" || second.Item.Id != "second" || !first.ManifestSent || !first.ManifestAcked || second.ManifestSent || second.ManifestAcked || !first.ManifestSentAt.Equal(at) {
		t.Fatalf("manifest states: first=%+v second=%+v", first, second)
	}
	if !proto.Equal(first.Score, score) || first.Item.Features.LabelOverride != "confirmed-boat" || first.PullBoost != 42 || first.PullBy != "watchstander" || !first.PullOriginal {
		t.Fatalf("persisted state=%+v", first)
	}
	full := first.Form(pb.FormKind_FORM_KIND_FULL)
	if full == nil || full.AckedThrough != 4 || full.Complete || !full.Ready {
		t.Fatalf("partial form=%+v", full)
	}
	if err := s.SetAcked(ctx, "first", pb.FormKind_FORM_KIND_FACT, 4, true); err != nil {
		t.Fatal(err)
	}
	row, err = s.Get(ctx, "first")
	if err != nil || row.PullBoost != 42 {
		t.Fatalf("FACT unexpectedly satisfied FULL pull: row=%v err=%v", row, err)
	}
	if err := s.SetAcked(ctx, "first", pb.FormKind_FORM_KIND_FULL, full.Bytes, true); err != nil {
		t.Fatal(err)
	}
	row, err = s.Get(ctx, "first")
	if err != nil || row.PullBoost != 0 || row.PullBy != "" || row.PullForm != 0 || !row.Form(pb.FormKind_FORM_KIND_FULL).Complete {
		t.Fatalf("completed pull: row=%v err=%v", row, err)
	}
}

func TestMetadataRoundTripsAndOverrideSupersession(t *testing.T) {
	s := openTest(t)
	if p, err := s.GetPolicy(ctx); err != nil || p != nil {
		t.Fatalf("empty policy=%v err=%v", p, err)
	}
	if cursor, err := s.GetCursor(ctx); err != nil || cursor != 0 {
		t.Fatalf("empty cursor=%d err=%v", cursor, err)
	}
	if overrides, err := s.PendingOverrides(ctx); err != nil || len(overrides) != 0 {
		t.Fatalf("empty overrides=%v err=%v", overrides, err)
	}
	for _, cursor := range []int64{1, 1<<62 + 17, 0} {
		if err := s.SetCursor(ctx, cursor); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetCursor(ctx); err != nil || got != cursor {
			t.Fatalf("cursor=%d want=%d err=%v", got, cursor, err)
		}
	}
	p := &pb.Policy{Id: "policy-1", Version: 9, Text: "keep this policy"}
	if err := s.SetPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPolicy(ctx); err != nil || !proto.Equal(got, p) {
		t.Fatalf("policy=%v err=%v", got, err)
	}
	if err := s.SavePolicyState(ctx, p, 12); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePolicyState(ctx, nil, 13); err != nil {
		t.Fatal(err)
	}
	if got, high, err := s.LoadPolicyState(ctx); err != nil || got != nil || high != 13 {
		t.Fatalf("cleared policy=%v high=%d err=%v", got, high, err)
	}
	old := &pb.LabelOverride{ItemId: "first", Label: "boat", By: "first-watch"}
	other := &pb.LabelOverride{ItemId: "second", Label: "buoy", By: "second-watch"}
	latest := &pb.LabelOverride{ItemId: "first", Label: "warship", By: "second-watch"}
	for _, override := range []*pb.LabelOverride{old, other, latest} {
		if err := s.QueueOverride(ctx, override); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearOverrides(ctx, []*pb.LabelOverride{old}); err != nil {
		t.Fatal(err)
	}
	got, err := s.PendingOverrides(ctx)
	if err != nil || len(got) != 2 || !proto.Equal(got[0], other) || !proto.Equal(got[1], latest) {
		t.Fatalf("superseded correction=%v err=%v", got, err)
	}
	if err := s.ClearOverrides(ctx, []*pb.LabelOverride{other, latest}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.PendingOverrides(ctx); err != nil || len(got) != 0 {
		t.Fatalf("acknowledged corrections=%v err=%v", got, err)
	}
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Event(ctx, at, "override", "first", "confirmed label"); err != nil {
		t.Fatal(err)
	}
	var ts int64
	var kind, id, detail string
	if err := s.db.QueryRow(`SELECT ts,kind,item_id,detail FROM events`).Scan(&ts, &kind, &id, &detail); err != nil {
		t.Fatal(err)
	}
	if ts != at.UnixMilli() || kind != "override" || id != "first" || detail != "confirmed label" {
		t.Fatalf("event=%d %q %q %q", ts, kind, id, detail)
	}
}

func TestMalformedPersistedMetadataReturnsErrors(t *testing.T) {
	for _, tc := range []struct {
		key  string
		read func(*Store) error
	}{
		{"policy", func(s *Store) error { _, err := s.GetPolicy(ctx); return err }},
		{"policy_last_good", func(s *Store) error { _, _, err := s.LoadPolicyState(ctx); return err }},
		{"pending_overrides", func(s *Store) error { _, err := s.PendingOverrides(ctx); return err }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			s := openTest(t)
			if err := s.setKV(ctx, tc.key, []byte{0xff}); err != nil {
				t.Fatal(err)
			}
			if err := tc.read(s); err == nil {
				t.Fatal("corrupt metadata accepted")
			}
		})
	}
	s := openTest(t)
	if err := s.setKV(ctx, "downlink_cursor", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetCursor(ctx); err != nil || got != 0 {
		t.Fatalf("short legacy cursor=%d err=%v", got, err)
	}
}

func TestFailedPutCleansPayloadAndInvalidPolicyKeepsState(t *testing.T) {
	s := openTest(t)
	invalidUTF8 := string([]byte{0xff})
	if _, err := s.Put(ctx, &pb.Item{Id: "invalid-proto", Source: invalidUTF8}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: []byte("payload")}); err == nil {
		t.Fatal("invalid protobuf string accepted")
	}
	if _, err := os.Stat(filepath.Join(s.blobDir, "invalid-proto")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Put left payload: %v", err)
	}
	if rows, err := s.All(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("failed Put left rows=%v err=%v", rows, err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "/absolute", "nested/item"} {
		if _, err := s.Put(ctx, &pb.Item{Id: id}, nil); err == nil {
			t.Fatalf("invalid id %q accepted", id)
		}
	}
	good := &pb.Policy{Id: "good"}
	if err := s.SavePolicyState(ctx, good, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePolicyState(ctx, &pb.Policy{Id: invalidUTF8}, 8); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if got, high, err := s.LoadPolicyState(ctx); err != nil || !proto.Equal(got, good) || high != 7 {
		t.Fatalf("failed policy changed state=%v high=%d err=%v", got, high, err)
	}
	if _, err := s.Put(ctx, &pb.Item{Id: "valid.with.dot"}, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: []byte("payload")}); err != nil {
		t.Fatal(err)
	}
	if raw, err := s.ReadFormAll("valid.with.dot", pb.FormKind_FORM_KIND_FULL); err != nil || !bytes.Equal(raw, []byte("payload")) {
		t.Fatalf("store not reusable: raw=%q err=%v", raw, err)
	}
}

func TestClosedStorePropagatesErrors(t *testing.T) {
	s := openTest(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"hash ids", func() error { _, err := s.IDsByHash(ctx, "hash"); return err }},
		{"has hash", func() error { _, _, err := s.HasHash(ctx, "hash"); return err }},
		{"all", func() error { _, err := s.All(ctx); return err }},
		{"get", func() error { _, err := s.Get(ctx, "item"); return err }},
		{"scores", func() error { return s.UpdateScores(ctx, map[string]*pb.Score{}) }},
		{"manifest", func() error { return s.MarkManifestSent(ctx, []string{"item"}, time.Time{}) }},
		{"policy read", func() error { _, err := s.GetPolicy(ctx); return err }},
		{"policy write", func() error { return s.SetPolicy(ctx, &pb.Policy{}) }},
		{"cursor read", func() error { _, err := s.GetCursor(ctx); return err }},
		{"cursor write", func() error { return s.SetCursor(ctx, 7) }},
		{"state read", func() error { _, _, err := s.LoadPolicyState(ctx); return err }},
		{"state write", func() error { return s.SavePolicyState(ctx, nil, 0) }},
		{"audit read", func() error { _, err := s.ListPolicyAudit(ctx, 10); return err }},
		{"audit write", func() error { return s.AppendPolicyAudit(ctx, policy.AuditEntry{}) }},
		{"override queue", func() error { return s.QueueOverride(ctx, &pb.LabelOverride{ItemId: "item"}) }},
		{"override clear", func() error { return s.ClearOverrides(ctx, nil) }},
		{"event", func() error { return s.Event(ctx, time.Time{}, "event", "item", "detail") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("closed store operation succeeded")
			}
		})
	}
}

func TestOpenRejectsInvalidFilesystemAndDatabase(t *testing.T) {
	for _, invalidDatabase := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "not-directory")
		if invalidDatabase {
			path = filepath.Join(dir, "edge.db")
		}
		if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
			t.Fatal(err)
		}
		if invalidDatabase {
			path = dir
		}
		s, err := Open(path)
		if err == nil {
			_ = s.Close()
			t.Fatal("invalid storage path accepted")
		}
	}
}

func TestCanceledContextDoesNotWriteCursor(t *testing.T) {
	s := openTest(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.SetCursor(canceled, 99); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cursor write: %v", err)
	}
	if got, err := s.GetCursor(ctx); err != nil || got != 0 {
		t.Fatalf("canceled write persisted=%d err=%v", got, err)
	}
}
