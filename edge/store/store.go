// Package store is the edge's durable item store: SQLite for metadata and
// scheduler state, plain files for form payloads. Nothing is ever deleted;
// the scheduler only changes order.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db      *sql.DB
	blobDir string
}

// Row is an item plus its scheduler state, as the scheduler and console see it.
type Row struct {
	Item           *pb.Item
	Score          *pb.Score
	Forms          []FormState
	ManifestSent   bool
	ManifestAcked  bool
	PullBoost      float64
	PullForm       pb.FormKind
	PullBy         string
	ManifestSentAt time.Time
}

type FormState struct {
	Kind         pb.FormKind
	Bytes        int64
	Hash         string
	Mime         string
	Path         string
	Ready        bool
	AckedThrough int64
	Complete     bool
}

func (r *Row) Form(kind pb.FormKind) *FormState {
	for i := range r.Forms {
		if r.Forms[i].Kind == kind {
			return &r.Forms[i]
		}
	}
	return nil
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  type INTEGER NOT NULL,
  captured_at INTEGER NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  ingest_seq INTEGER NOT NULL,
  item BLOB NOT NULL,
  score BLOB,
  priority REAL NOT NULL DEFAULT 0,
  suppressed INTEGER NOT NULL DEFAULT 0,
  manifest_sent INTEGER NOT NULL DEFAULT 0,
  manifest_sent_at INTEGER NOT NULL DEFAULT 0,
  manifest_acked INTEGER NOT NULL DEFAULT 0,
  pull_boost REAL NOT NULL DEFAULT 0,
  pull_form INTEGER NOT NULL DEFAULT 0,
  pull_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS items_seq ON items(ingest_seq);
CREATE INDEX IF NOT EXISTS items_hash ON items(content_hash);
CREATE TABLE IF NOT EXISTS forms (
  item_id TEXT NOT NULL REFERENCES items(id),
  kind INTEGER NOT NULL,
  bytes INTEGER NOT NULL,
  hash TEXT NOT NULL,
  mime TEXT NOT NULL,
  path TEXT NOT NULL,
  ready INTEGER NOT NULL DEFAULT 1,
  acked_through INTEGER NOT NULL DEFAULT 0,
  complete INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, kind)
);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v BLOB);
CREATE TABLE IF NOT EXISTS events (
  ts INTEGER NOT NULL, kind TEXT NOT NULL, item_id TEXT, detail TEXT
);
`

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "edge.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db, blobDir: filepath.Join(dataDir, "blobs")}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Put stores a new item and its ready forms. payloads maps FormKind to the
// bytes of that form; Item.Forms is filled in (bytes, hash) from payloads.
// Returns the assigned ingest sequence number.
func (s *Store) Put(ctx context.Context, it *pb.Item, payloads map[pb.FormKind][]byte) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ingest_seq),0)+1 FROM items`).Scan(&seq); err != nil {
		return 0, err
	}
	it.IngestSeq = seq
	kinds := make([]pb.FormKind, 0, len(payloads))
	for k := range payloads {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	it.Forms = it.Forms[:0]
	for _, k := range kinds {
		data := payloads[k]
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		path := filepath.Join(s.blobDir, it.Id, fmt.Sprintf("%d", k))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return 0, err
		}
		mime := mimeFor(k, it.Type)
		it.Forms = append(it.Forms, &pb.Form{Kind: k, Bytes: int64(len(data)), ContentHash: h, Mime: mime, Ready: true})
		if _, err := tx.ExecContext(ctx, `INSERT INTO forms(item_id,kind,bytes,hash,mime,path) VALUES(?,?,?,?,?,?)`,
			it.Id, int(k), len(data), h, mime, path); err != nil {
			return 0, err
		}
	}
	if it.ContentHash == "" {
		if full, ok := payloads[pb.FormKind_FORM_KIND_FULL]; ok {
			sum := sha256.Sum256(full)
			it.ContentHash = hex.EncodeToString(sum[:])
		}
	}
	raw, err := proto.Marshal(it)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items(id,type,captured_at,source,content_hash,ingest_seq,item) VALUES(?,?,?,?,?,?,?)`,
		it.Id, int(it.Type), it.CapturedAtUnixMs, it.Source, it.ContentHash, seq, raw); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

func mimeFor(k pb.FormKind, t pb.ItemType) string {
	switch k {
	case pb.FormKind_FORM_KIND_FACT:
		return "application/json"
	case pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_CROP:
		return "image/jpeg"
	}
	if t == pb.ItemType_ITEM_TYPE_IMAGE {
		return "image/jpeg"
	}
	return "application/octet-stream"
}

// HasHash reports whether an item with this content hash already exists (dedup).
// IDsByHash lists every item holding this exact content (several can: a
// FACT+THUMB scene item and a full item of the same frame).
func (s *Store) IDsByHash(ctx context.Context, h string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM items WHERE content_hash=? ORDER BY ingest_seq`, h)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) HasHash(ctx context.Context, h string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM items WHERE content_hash=? LIMIT 1`, h).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

const rowCols = `id, item, score, manifest_sent, manifest_sent_at, manifest_acked, pull_boost, pull_form, pull_by`

func scanRow(sc interface{ Scan(...any) error }) (*Row, error) {
	var (
		id                string
		itemRaw, scoreRaw []byte
		ms, ma            int
		msAt              int64
		pullBoost         float64
		pullForm          int
		pullBy            string
	)
	if err := sc.Scan(&id, &itemRaw, &scoreRaw, &ms, &msAt, &ma, &pullBoost, &pullForm, &pullBy); err != nil {
		return nil, err
	}
	r := &Row{Item: &pb.Item{}, Score: &pb.Score{}, ManifestSent: ms == 1, ManifestAcked: ma == 1,
		PullBoost: pullBoost, PullForm: pb.FormKind(pullForm), PullBy: pullBy}
	if msAt > 0 {
		r.ManifestSentAt = time.UnixMilli(msAt)
	}
	if err := proto.Unmarshal(itemRaw, r.Item); err != nil {
		return nil, err
	}
	if len(scoreRaw) > 0 {
		if err := proto.Unmarshal(scoreRaw, r.Score); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (s *Store) loadForms(ctx context.Context, rows map[string]*Row) error {
	if len(rows) == 0 {
		return nil
	}
	q, err := s.db.QueryContext(ctx, `SELECT item_id,kind,bytes,hash,mime,path,ready,acked_through,complete FROM forms ORDER BY item_id, kind`)
	if err != nil {
		return err
	}
	defer q.Close()
	for q.Next() {
		var (
			id         string
			kind       int
			f          FormState
			ready, cpl int
		)
		if err := q.Scan(&id, &kind, &f.Bytes, &f.Hash, &f.Mime, &f.Path, &ready, &f.AckedThrough, &cpl); err != nil {
			return err
		}
		f.Kind, f.Ready, f.Complete = pb.FormKind(kind), ready == 1, cpl == 1
		if r, ok := rows[id]; ok {
			r.Forms = append(r.Forms, f)
		}
	}
	return q.Err()
}

// All returns every item with scheduler state, ordered by ingest sequence.
// Backlogs are tens of thousands of rows at most; loading them per tick is
// cheap compared to any link.
func (s *Store) All(ctx context.Context) ([]*Row, error) {
	q, err := s.db.QueryContext(ctx, `SELECT `+rowCols+` FROM items ORDER BY ingest_seq`)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []*Row
	byID := map[string]*Row{}
	for q.Next() {
		r, err := scanRow(q)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
		byID[r.Item.Id] = r
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	return out, s.loadForms(ctx, byID)
}

func (s *Store) Get(ctx context.Context, id string) (*Row, error) {
	r, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+rowCols+` FROM items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	q, err := s.db.QueryContext(ctx, `SELECT kind,bytes,hash,mime,path,ready,acked_through,complete FROM forms WHERE item_id=? ORDER BY kind`, id)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	for q.Next() {
		var (
			kind       int
			f          FormState
			ready, cpl int
		)
		if err := q.Scan(&kind, &f.Bytes, &f.Hash, &f.Mime, &f.Path, &ready, &f.AckedThrough, &cpl); err != nil {
			return nil, err
		}
		f.Kind, f.Ready, f.Complete = pb.FormKind(kind), ready == 1, cpl == 1
		r.Forms = append(r.Forms, f)
	}
	return r, q.Err()
}

// UpdateScores writes new scores for the given items in one transaction.
func (s *Store) UpdateScores(ctx context.Context, scores map[string]*pb.Score) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `UPDATE items SET score=?, priority=?, suppressed=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer st.Close()
	for id, sc := range scores {
		raw, err := proto.Marshal(sc)
		if err != nil {
			return err
		}
		sup := 0
		if sc.Suppressed {
			sup = 1
		}
		if _, err := st.ExecContext(ctx, raw, sc.Priority, sup, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateItem rewrites the item blob (e.g. after a label override).
func (s *Store) UpdateItem(ctx context.Context, it *pb.Item) error {
	raw, err := proto.Marshal(it)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE items SET item=? WHERE id=?`, raw, it.Id)
	return err
}

func (s *Store) MarkManifestSent(ctx context.Context, ids []string, at time.Time) error {
	return s.execEach(ctx, `UPDATE items SET manifest_sent=1, manifest_sent_at=? WHERE id=?`, ids, at.UnixMilli())
}

func (s *Store) MarkManifestAcked(ctx context.Context, ids []string) error {
	return s.execEach(ctx, `UPDATE items SET manifest_acked=1, manifest_sent=1 WHERE id=?`, ids)
}

// ClearManifestSent un-marks items whose manifest was sent but never acked,
// so they are retried (called after an exchange failure or on a timeout).
func (s *Store) ClearManifestSent(ctx context.Context, ids []string) error {
	return s.execEach(ctx, `UPDATE items SET manifest_sent=0 WHERE id=? AND manifest_acked=0`, ids)
}

func (s *Store) execEach(ctx context.Context, q string, ids []string, lead ...any) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		args := append(append([]any{}, lead...), id)
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceForm rewrites a form's payload in place and resets its delivery
// state, so the new content is re-sent. Used when a FACT changes (label
// override) after it has already reached shore.
func (s *Store) ReplaceForm(ctx context.Context, itemID string, kind pb.FormKind, data []byte) (*pb.Form, error) {
	var path, mime string
	if err := s.db.QueryRowContext(ctx, `SELECT path, mime FROM forms WHERE item_id=? AND kind=?`, itemID, int(kind)).Scan(&path, &mime); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	if _, err := s.db.ExecContext(ctx, `UPDATE forms SET bytes=?, hash=?, acked_through=0, complete=0 WHERE item_id=? AND kind=?`,
		len(data), h, itemID, int(kind)); err != nil {
		return nil, err
	}
	return &pb.Form{Kind: kind, Bytes: int64(len(data)), ContentHash: h, Mime: mime, Ready: true}, nil
}

// SetAcked records shore's contiguous receipt of a form.
func (s *Store) SetAcked(ctx context.Context, itemID string, kind pb.FormKind, through int64, complete bool) error {
	c := 0
	if complete {
		c = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE forms SET acked_through=MAX(acked_through,?), complete=MAX(complete,?) WHERE item_id=? AND kind=?`,
		through, c, itemID, int(kind))
	if err != nil || !complete {
		return err
	}
	// A pull is satisfied once the requested form (FULL if unspecified) lands.
	_, err = s.db.ExecContext(ctx, `UPDATE items SET pull_boost=0, pull_form=0, pull_by='' WHERE id=? AND pull_boost>0 AND (pull_form=? OR (pull_form=0 AND ?=?))`,
		itemID, int(kind), int(kind), int(pb.FormKind_FORM_KIND_FULL))
	return err
}

func (s *Store) SetPull(ctx context.Context, itemID string, kind pb.FormKind, boost float64, by string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE items SET pull_boost=?, pull_form=?, pull_by=? WHERE id=?`, boost, int(kind), by, itemID)
	return err
}

// ReadForm returns up to n bytes of a form starting at offset.
func (s *Store) ReadForm(itemID string, kind pb.FormKind, offset, n int64) ([]byte, error) {
	f, err := os.Open(filepath.Join(s.blobDir, itemID, fmt.Sprintf("%d", kind)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:k], nil
}

func (s *Store) ReadFormAll(itemID string, kind pb.FormKind) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.blobDir, itemID, fmt.Sprintf("%d", kind)))
}

// Policy persistence (singleton).
func (s *Store) GetPolicy(ctx context.Context) (*pb.Policy, error) {
	raw, err := s.getKV(ctx, "policy")
	if err != nil || raw == nil {
		return nil, err
	}
	p := &pb.Policy{}
	return p, proto.Unmarshal(raw, p)
}

func (s *Store) SetPolicy(ctx context.Context, p *pb.Policy) error {
	raw, err := proto.Marshal(p)
	if err != nil {
		return err
	}
	return s.setKV(ctx, "policy", raw)
}

func (s *Store) GetCursor(ctx context.Context) (int64, error) {
	raw, err := s.getKV(ctx, "downlink_cursor")
	if err != nil || len(raw) < 8 {
		return 0, err
	}
	var v int64
	for i := 0; i < 8; i++ {
		v = v<<8 | int64(raw[i])
	}
	return v, nil
}

func (s *Store) SetCursor(ctx context.Context, v int64) error {
	raw := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		raw[i] = byte(v)
		v >>= 8
	}
	return s.setKV(ctx, "downlink_cursor", raw)
}

// QueueOverride records a shipboard label correction to be uplinked.
func (s *Store) QueueOverride(ctx context.Context, o *pb.LabelOverride) error {
	cur, err := s.PendingOverrides(ctx)
	if err != nil {
		return err
	}
	kept := cur[:0]
	for _, c := range cur {
		if c.ItemId != o.ItemId {
			kept = append(kept, c)
		}
	}
	kept = append(kept, o)
	return s.saveOverrides(ctx, kept)
}

// PendingOverrides returns shipboard overrides shore has not acknowledged.
func (s *Store) PendingOverrides(ctx context.Context) ([]*pb.LabelOverride, error) {
	raw, err := s.getKV(ctx, "pending_overrides")
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var list []*pb.LabelOverride
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// ClearOverrides drops the given overrides once shore has taken them.
// A newer correction for the same item queued meanwhile is kept.
func (s *Store) ClearOverrides(ctx context.Context, sent []*pb.LabelOverride) error {
	cur, err := s.PendingOverrides(ctx)
	if err != nil {
		return err
	}
	kept := cur[:0]
	for _, c := range cur {
		acked := false
		for _, o := range sent {
			if o.ItemId == c.ItemId && o.Label == c.Label && o.By == c.By {
				acked = true
				break
			}
		}
		if !acked {
			kept = append(kept, c)
		}
	}
	return s.saveOverrides(ctx, kept)
}

func (s *Store) saveOverrides(ctx context.Context, list []*pb.LabelOverride) error {
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.setKV(ctx, "pending_overrides", raw)
}

func (s *Store) getKV(ctx context.Context, k string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (s *Store) setKV(ctx context.Context, k string, v []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// Event appends to the audit log (overrides, policy changes, pulls).
func (s *Store) Event(ctx context.Context, at time.Time, kind, itemID, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(ts,kind,item_id,detail) VALUES(?,?,?,?)`, at.UnixMilli(), kind, itemID, detail)
	return err
}
