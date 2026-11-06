// Package store is shore's picture of the fleet: what each edge has
// (manifest), what has arrived (forms, possibly partial), and the downlink
// queue of control messages waiting for each edge. SQLite now; the
// interface is small enough to move to Postgres later.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrNotOwner = errors.New("item belongs to another edge")

type Store struct {
	db      *sql.DB
	blobDir string
	blobMu  sync.Mutex
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  edge_id TEXT NOT NULL,
  type INTEGER NOT NULL,
  captured_at INTEGER NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  manifest BLOB NOT NULL,
  manifest_at INTEGER NOT NULL,
  features BLOB,
  label_override TEXT NOT NULL DEFAULT '',
  override_by TEXT NOT NULL DEFAULT '',
  priority REAL NOT NULL DEFAULT 0,
  unknownness REAL NOT NULL DEFAULT 0,
  top_label TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS items_captured ON items(captured_at);
CREATE TABLE IF NOT EXISTS forms (
  item_id TEXT NOT NULL,
  kind INTEGER NOT NULL,
  total INTEGER NOT NULL,
  received INTEGER NOT NULL DEFAULT 0,
  complete INTEGER NOT NULL DEFAULT 0,
  hash TEXT NOT NULL DEFAULT '',
  mime TEXT NOT NULL DEFAULT '',
  first_at INTEGER NOT NULL DEFAULT 0,
  complete_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, kind)
);
CREATE TABLE IF NOT EXISTS downlink (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  edge_id TEXT NOT NULL,
  msg BLOB NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS edges (
  id TEXT PRIMARY KEY,
  heartbeat BLOB,
  last_seen INTEGER NOT NULL DEFAULT 0,
  bytes_received INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v BLOB);
`

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "shore.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if _, err := db.Exec(apiSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("API schema: %w", err)
	}
	return &Store{db: db, blobDir: filepath.Join(dataDir, "blobs")}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Item is shore's view of one item.
type Item struct {
	Manifest      *pb.ManifestEntry
	EdgeID        string
	ManifestAt    time.Time
	Features      *pb.Features // present once FACT has arrived
	LabelOverride string
	OverrideBy    string
	Forms         []FormState
}

type FormState struct {
	Kind       pb.FormKind
	Total      int64
	Received   int64 // contiguous from 0
	Complete   bool
	Mime       string
	FirstAt    time.Time
	CompleteAt time.Time
}

func (it *Item) Form(k pb.FormKind) *FormState {
	for i := range it.Forms {
		if it.Forms[i].Kind == k {
			return &it.Forms[i]
		}
	}
	return nil
}

// UpsertManifest records that an item exists on an edge. Idempotent.
func (s *Store) UpsertManifest(ctx context.Context, edgeID string, me *pb.ManifestEntry, at time.Time) error {
	if edgeID == "" {
		return fmt.Errorf("%w: empty edge id", ErrNotOwner)
	}
	if !ids.Valid(me.ItemId) {
		return fmt.Errorf("invalid item id %q", me.ItemId)
	}
	if me.Source != "" && !ids.ValidName(me.Source) {
		me = proto.Clone(me).(*pb.ManifestEntry)
		me.Source = ""
	}
	raw, err := proto.Marshal(me)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO items(id,edge_id,type,captured_at,source,manifest,manifest_at,priority,unknownness,top_label)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET edge_id=excluded.edge_id, manifest=excluded.manifest, priority=excluded.priority, unknownness=excluded.unknownness, top_label=excluded.top_label,
		type=excluded.type, captured_at=excluded.captured_at, source=excluded.source,
		manifest_at=CASE WHEN items.manifest_at=0 THEN excluded.manifest_at ELSE items.manifest_at END
		WHERE items.edge_id=excluded.edge_id OR items.edge_id=''`,
		me.ItemId, edgeID, int(me.Type), me.CapturedAtUnixMs, me.Source, raw, at.UnixMilli(), me.Priority, me.Unknownness, me.TopLabel)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: item %s", ErrNotOwner, me.ItemId)
	}
	for _, f := range me.Forms {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO forms(item_id,kind,total) VALUES(?,?,?)`, me.ItemId, int(f.Kind), f.Bytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MaxBlobBytes bounds any single form shore will accept.
const MaxBlobBytes = 256 << 20

// MaxChunkBytes caps one Chunk; the edge scheduler sends at most 64 KiB.
const MaxChunkBytes = 4 << 20

// WriteChunk stores a chunk at its offset and returns the new contiguous
// receive count and whether the form is complete. Out-of-order or duplicate
// chunks are safe: bytes are written in place and "received" only advances
// contiguously (chunks arrive in order from one edge in practice).
func (s *Store) WriteChunk(ctx context.Context, edgeID string, c *pb.Chunk, at time.Time) (int64, bool, error) {
	s.blobMu.Lock()
	defer s.blobMu.Unlock()
	if !ids.Valid(c.ItemId) {
		return 0, false, fmt.Errorf("invalid item id %q", c.ItemId)
	}
	n := int64(len(c.Data))
	if c.TotalBytes <= 0 || c.TotalBytes > MaxBlobBytes || n > MaxChunkBytes || c.Offset < 0 || c.Offset > c.TotalBytes || n > c.TotalBytes-c.Offset {
		return 0, false, fmt.Errorf("chunk out of bounds: off=%d len=%d total=%d", c.Offset, len(c.Data), c.TotalBytes)
	}
	if edgeID == "" {
		return 0, false, fmt.Errorf("%w: empty edge id", ErrNotOwner)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO items(id,edge_id,type,captured_at,source,manifest,manifest_at) VALUES(?,?,0,?,'',x'',0)`,
		c.ItemId, edgeID, at.UnixMilli()); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET edge_id=? WHERE id=? AND edge_id=''`, edgeID, c.ItemId); err != nil {
		return 0, false, err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT edge_id FROM items WHERE id=?`, c.ItemId).Scan(&owner); err != nil {
		return 0, false, err
	}
	if owner != edgeID {
		return 0, false, fmt.Errorf("%w: item %s", ErrNotOwner, c.ItemId)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	path := filepath.Join(s.blobDir, c.ItemId, fmt.Sprintf("%d", c.Form))
	partial := path + ".partial"
	var received int64
	var complete int
	var hash string
	err = s.db.QueryRowContext(ctx, `SELECT received, complete, hash FROM forms WHERE item_id=? AND kind=?`, c.ItemId, int(c.Form)).Scan(&received, &complete, &hash)
	if err == nil && complete == 1 && (c.ContentHash == "" || c.ContentHash == hash) {
		return c.TotalBytes, true, nil
	}
	if err == nil && c.ContentHash != hash && c.ContentHash != "" && (complete == 1 || hash != "") {
		// The edge regenerated this form (FACT after a label override, FULL
		// after an original pull), possibly mid-transfer: start over with
		// the new content.
		received, complete = 0, 0
		_ = os.Remove(partial)
	}
	if _, statErr := os.Stat(partial); errors.Is(statErr, os.ErrNotExist) {
		received = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, false, err
	}
	f, err2 := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY, 0o644)
	if err2 != nil {
		return 0, false, err2
	}
	if _, err := f.WriteAt(c.Data, c.Offset); err != nil {
		f.Close()
		return 0, false, err
	}
	if err := f.Close(); err != nil {
		return 0, false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Payload before manifest (FIFO baseline, or lost manifest): the
		// ownership check above already created the item stub.
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO forms(item_id,kind,total) VALUES(?,?,?)`, c.ItemId, int(c.Form), c.TotalBytes); err != nil {
			return 0, false, err
		}
	} else if err != nil {
		return 0, false, err
	}
	if c.Offset <= received {
		if end := c.Offset + int64(len(c.Data)); end > received {
			received = end
		}
	}
	done := received >= c.TotalBytes && c.TotalBytes > 0
	if done && c.ContentHash != "" {
		data, err := os.ReadFile(partial)
		if err != nil {
			return 0, false, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != c.ContentHash {
			// Corrupt: restart this form.
			_, _ = s.db.ExecContext(ctx, `UPDATE forms SET received=0,complete=0,complete_at=0 WHERE item_id=? AND kind=?`, c.ItemId, int(c.Form))
			return 0, false, fmt.Errorf("hash mismatch for %s/%v", c.ItemId, c.Form)
		}
	}
	cpl := 0
	if done {
		if err := os.Rename(partial, path); err != nil {
			return 0, false, err
		}
		cpl = 1
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE forms SET received=?, complete=?, hash=?, mime=?, total=?,
		first_at=CASE WHEN first_at=0 THEN ? ELSE first_at END,
		complete_at=CASE WHEN ?=1 THEN ? ELSE 0 END
		WHERE item_id=? AND kind=?`,
		received, cpl, c.ContentHash, c.Mime, c.TotalBytes, at.UnixMilli(), cpl, at.UnixMilli(), c.ItemId, int(c.Form)); err != nil {
		return 0, false, err
	}
	return received, done, nil
}

// SetFeatures stores the decoded FACT for an item.
func (s *Store) SetFeatures(ctx context.Context, itemID string, f *pb.Features, topLabel string) error {
	raw, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE items SET features=?, top_label=CASE WHEN ?='' THEN top_label ELSE ? END WHERE id=?`, raw, topLabel, topLabel, itemID)
	return err
}

// SetOverride is operator-only and may update an item owned by any edge.
func (s *Store) SetOverride(ctx context.Context, itemID, label, by string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE items SET label_override=?, override_by=? WHERE id=?`, label, by, itemID)
	return err
}

func (s *Store) SetOverrideFrom(ctx context.Context, edgeID, itemID, label, by string) error {
	if edgeID == "" {
		return fmt.Errorf("%w: empty edge id", ErrNotOwner)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE items SET label_override=?, override_by=? WHERE id=? AND (edge_id=? OR edge_id='')`,
		label, by, itemID, edgeID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 0 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM items WHERE id=?)`, itemID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: item %s", ErrNotOwner, itemID)
	}
	return nil
}

func (s *Store) Owner(ctx context.Context, itemID string) (string, error) {
	var edgeID string
	err := s.db.QueryRowContext(ctx, `SELECT edge_id FROM items WHERE id=?`, itemID).Scan(&edgeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return edgeID, err
}

func (s *Store) ReadForm(itemID string, kind pb.FormKind) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.blobDir, itemID, fmt.Sprintf("%d", kind)))
}

// OpenForm streams a blob without allocating its full payload in an API reader.
func (s *Store) OpenForm(itemID string, kind pb.FormKind) (*os.File, error) {
	if !ids.Valid(itemID) {
		return nil, ErrNotFound
	}
	return os.Open(filepath.Join(s.blobDir, itemID, fmt.Sprintf("%d", kind)))
}

// Heartbeat records the latest edge heartbeat.
func (s *Store) Heartbeat(ctx context.Context, edgeID string, hb *pb.Heartbeat, bytes int64, at time.Time) error {
	raw, _ := proto.Marshal(hb)
	_, err := s.db.ExecContext(ctx, `INSERT INTO edges(id,heartbeat,last_seen,bytes_received) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET heartbeat=excluded.heartbeat, last_seen=excluded.last_seen, bytes_received=edges.bytes_received+excluded.bytes_received`,
		edgeID, raw, at.UnixMilli(), bytes)
	return err
}

type Edge struct {
	ID            string
	Heartbeat     *pb.Heartbeat
	LastSeen      time.Time
	BytesReceived int64
}

func (s *Store) Edges(ctx context.Context) ([]Edge, error) {
	q, err := s.db.QueryContext(ctx, `SELECT id, heartbeat, last_seen, bytes_received FROM edges`)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []Edge
	for q.Next() {
		var e Edge
		var raw []byte
		var ls int64
		if err := q.Scan(&e.ID, &raw, &ls, &e.BytesReceived); err != nil {
			return nil, err
		}
		e.LastSeen = time.UnixMilli(ls)
		e.Heartbeat = &pb.Heartbeat{}
		_ = proto.Unmarshal(raw, e.Heartbeat)
		out = append(out, e)
	}
	return out, q.Err()
}

// EnqueueDownlink queues a control message for an edge ("" = all edges).
func (s *Store) EnqueueDownlink(ctx context.Context, edgeID string, d *pb.Downlink, at time.Time) (int64, error) {
	raw, err := proto.Marshal(d)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO downlink(edge_id,msg,created_at) VALUES(?,?,?)`, edgeID, raw, at.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PendingDownlink returns messages after cursor for this edge, oldest first.
func (s *Store) PendingDownlink(ctx context.Context, edgeID string, cursor int64, limit int) ([]*pb.Downlink, error) {
	q, err := s.db.QueryContext(ctx, `SELECT seq, msg FROM downlink WHERE seq>? AND (edge_id=? OR edge_id='') ORDER BY seq LIMIT ?`, cursor, edgeID, limit)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []*pb.Downlink
	for q.Next() {
		var seq int64
		var raw []byte
		if err := q.Scan(&seq, &raw); err != nil {
			return nil, err
		}
		d := &pb.Downlink{}
		if err := proto.Unmarshal(raw, d); err != nil {
			return nil, err
		}
		d.Seq = seq
		out = append(out, d)
	}
	return out, q.Err()
}

type ListFilter struct {
	Type        pb.ItemType
	UnknownOnly bool
	Label       string
	Limit       int
	Offset      int
}

func (s *Store) List(ctx context.Context, f ListFilter) ([]*Item, int64, error) {
	where, args := "1=1", []any{}
	if f.Type != pb.ItemType_ITEM_TYPE_UNSPECIFIED {
		where += " AND type=?"
		args = append(args, int(f.Type))
	}
	if f.UnknownOnly {
		where += " AND unknownness>=0.5 AND label_override=''"
	}
	if f.Label != "" {
		where += " AND (top_label LIKE ? OR label_override LIKE ?)"
		args = append(args, "%"+f.Label+"%", "%"+f.Label+"%")
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	q, err := s.db.QueryContext(ctx, `SELECT id FROM items WHERE `+where+` ORDER BY priority DESC, captured_at DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for q.Next() {
		var id string
		if err := q.Scan(&id); err != nil {
			q.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	q.Close()
	out := make([]*Item, 0, len(ids))
	for _, id := range ids {
		it, err := s.Get(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, nil
}

func (s *Store) Get(ctx context.Context, id string) (*Item, error) {
	it := &Item{Manifest: &pb.ManifestEntry{}}
	var mraw, fraw []byte
	var mat int64
	err := s.db.QueryRowContext(ctx, `SELECT edge_id, manifest, manifest_at, features, label_override, override_by FROM items WHERE id=?`, id).
		Scan(&it.EdgeID, &mraw, &mat, &fraw, &it.LabelOverride, &it.OverrideBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(mraw) > 0 {
		if err := proto.Unmarshal(mraw, it.Manifest); err != nil {
			return nil, err
		}
	}
	it.Manifest.ItemId = id
	it.ManifestAt = time.UnixMilli(mat)
	if len(fraw) > 0 {
		it.Features = &pb.Features{}
		if err := proto.Unmarshal(fraw, it.Features); err != nil {
			return nil, err
		}
	}
	q, err := s.db.QueryContext(ctx, `SELECT kind,total,received,complete,mime,first_at,complete_at FROM forms WHERE item_id=? ORDER BY kind`, id)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	for q.Next() {
		var fs FormState
		var kind, cpl int
		var fa, ca int64
		if err := q.Scan(&kind, &fs.Total, &fs.Received, &cpl, &fs.Mime, &fa, &ca); err != nil {
			return nil, err
		}
		fs.Kind, fs.Complete = pb.FormKind(kind), cpl == 1
		if fa > 0 {
			fs.FirstAt = time.UnixMilli(fa)
		}
		if ca > 0 {
			fs.CompleteAt = time.UnixMilli(ca)
		}
		it.Forms = append(it.Forms, fs)
	}
	return it, q.Err()
}

type Counts struct {
	Items, Manifested, Unknown, AnyFormComplete int64
	AllFormsComplete                            int64 // every announced form has arrived (records: FACT only)
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM items),
		(SELECT COUNT(*) FROM items WHERE manifest_at>0),
		(SELECT COUNT(*) FROM items WHERE unknownness>=0.5 AND label_override=''),
		(SELECT COUNT(DISTINCT item_id) FROM forms WHERE complete=1),
		(SELECT COUNT(*) FROM items i WHERE EXISTS(SELECT 1 FROM forms f WHERE f.item_id=i.id)
			AND NOT EXISTS(SELECT 1 FROM forms f WHERE f.item_id=i.id AND f.complete=0))`).
		Scan(&c.Items, &c.Manifested, &c.Unknown, &c.AnyFormComplete, &c.AllFormsComplete)
	return c, err
}

func (s *Store) GetPolicy(ctx context.Context) (*pb.Policy, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k='policy'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := &pb.Policy{}
	return p, proto.Unmarshal(raw, p)
}

func (s *Store) SetPolicy(ctx context.Context, p *pb.Policy) error {
	return s.SetPolicyState(ctx, p, 0, false)
}

func (s *Store) GetCommandVersion(ctx context.Context) (int64, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k='policy_command_version'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	version, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse command policy version: %w", err)
	}
	return version, true, nil
}

func (s *Store) SetPolicyState(ctx context.Context, p *pb.Policy, commandVersion int64, writeCommandVersion bool) error {
	raw, err := proto.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES('policy',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, raw); err != nil {
		return err
	}
	if writeCommandVersion {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES('policy_command_version',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, strconv.FormatInt(commandVersion, 10)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
