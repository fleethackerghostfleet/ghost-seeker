package store

import (
	"context"
	"database/sql"
	"errors"
)

// The receipt and event sequences are separate: list pagination sees each item
// once, while the stream also records subsequent changes. Triggers make the
// journal atomic with writes from every caller, not just the sync HTTP handler.
const rankChanges = `
 INSERT INTO api_events(item_id,kind)
 SELECT r.item_id,'updated' FROM api_base_tiers r JOIN api_tier_cache c ON c.item_id=r.item_id
 WHERE r.tier!=c.tier ORDER BY r.seq;
 INSERT INTO api_tier_cache(item_id,tier) SELECT item_id,tier FROM api_base_tiers WHERE true
 ON CONFLICT(item_id) DO UPDATE SET tier=excluded.tier;
`

const apiSchema = `
CREATE TABLE IF NOT EXISTS api_receipts (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 item_id TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS api_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 item_id TEXT NOT NULL,
 kind TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS api_manifest_insert AFTER INSERT ON items WHEN NEW.manifest_at>0 BEGIN
 INSERT INTO api_receipts(item_id) VALUES(NEW.id) ON CONFLICT(item_id) DO NOTHING;
 INSERT INTO api_events(item_id,kind) VALUES(NEW.id,'created');
END;
CREATE TRIGGER IF NOT EXISTS api_manifest_update AFTER UPDATE OF manifest ON items
 WHEN NEW.manifest_at>0 AND OLD.manifest IS NOT NEW.manifest BEGIN
 INSERT INTO api_receipts(item_id) VALUES(NEW.id) ON CONFLICT(item_id) DO NOTHING;
 INSERT INTO api_events(item_id,kind) VALUES(NEW.id,CASE WHEN OLD.manifest_at=0 THEN 'created' ELSE 'updated' END);
END;
CREATE TRIGGER IF NOT EXISTS api_features AFTER UPDATE OF features,label_override,override_by ON items
 WHEN NEW.manifest_at>0 AND (OLD.features IS NOT NEW.features OR OLD.label_override IS NOT NEW.label_override OR OLD.override_by IS NOT NEW.override_by) BEGIN
 INSERT INTO api_events(item_id,kind) VALUES(NEW.id,'updated');
END;
DROP TRIGGER IF EXISTS api_form;
CREATE TRIGGER api_form AFTER UPDATE ON forms
 WHEN (NEW.complete!=OLD.complete OR NEW.complete_at!=OLD.complete_at OR NEW.hash IS NOT OLD.hash)
 AND EXISTS(SELECT 1 FROM api_receipts WHERE item_id=NEW.item_id) BEGIN
 INSERT INTO api_events(item_id,kind) VALUES(NEW.item_id,'updated');
END;
CREATE TRIGGER IF NOT EXISTS api_policy_insert AFTER INSERT ON kv WHEN NEW.k='policy' BEGIN
 INSERT INTO api_events(item_id,kind) SELECT item_id,'updated' FROM api_receipts ORDER BY seq;
END;
CREATE TRIGGER IF NOT EXISTS api_policy_update AFTER UPDATE ON kv WHEN NEW.k='policy' AND NEW.v IS NOT OLD.v BEGIN
 INSERT INTO api_events(item_id,kind) SELECT item_id,'updated' FROM api_receipts ORDER BY seq;
END;
DROP TRIGGER IF EXISTS api_rank_receipt;
DROP TRIGGER IF EXISTS api_rank_priority;
INSERT OR IGNORE INTO api_receipts(item_id) SELECT id FROM items WHERE manifest_at>0 ORDER BY manifest_at,rowid;
INSERT INTO api_events(item_id,kind) SELECT item_id,'created' FROM api_receipts WHERE NOT EXISTS(SELECT 1 FROM api_events) ORDER BY seq;
CREATE VIEW IF NOT EXISTS api_base_tiers AS
 SELECT seq,item_id,CASE WHEN rank<=1 OR 1.0*rank/total<=0.10 THEN 1
 WHEN 1.0*rank/total<=0.35 THEN 2 WHEN 1.0*rank/total<=0.70 THEN 3 ELSE 4 END AS tier FROM (
 SELECT r.seq,r.item_id,ROW_NUMBER() OVER(ORDER BY i.priority DESC,r.seq) AS rank,COUNT(*) OVER() AS total
 FROM api_receipts r JOIN items i ON i.id=r.item_id);
CREATE TABLE IF NOT EXISTS api_tier_cache(item_id TEXT PRIMARY KEY,tier INTEGER NOT NULL);
INSERT INTO api_tier_cache(item_id,tier) SELECT item_id,tier FROM api_base_tiers WHERE true
 ON CONFLICT(item_id) DO UPDATE SET tier=excluded.tier;
CREATE TRIGGER api_rank_receipt AFTER INSERT ON api_receipts BEGIN
` + rankChanges + `END;
CREATE TRIGGER api_rank_priority AFTER UPDATE OF priority ON items
 WHEN NEW.manifest_at>0 AND NEW.priority IS NOT OLD.priority BEGIN
` + rankChanges + `END;`

type Receipt struct {
	ID       string
	Sequence int64
	Rank     int
	Total    int
}

// Receipts is stable over received order, never captured time or mutable scores.
func (s *Store) Receipts(ctx context.Context, since int64, source string, limit int) ([]Receipt, error) {
	q, err := s.db.QueryContext(ctx, `WITH ranked AS (
 SELECT r.seq,r.item_id,i.source,ROW_NUMBER() OVER(ORDER BY i.priority DESC,r.seq) AS rank,COUNT(*) OVER() AS total
 FROM api_receipts r JOIN items i ON i.id=r.item_id)
 SELECT seq,item_id,rank,total FROM ranked WHERE seq>? AND (?='' OR source=?) ORDER BY seq LIMIT ?`, since, source, source, limit)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []Receipt{}
	for q.Next() {
		var r Receipt
		if err := q.Scan(&r.Sequence, &r.ID, &r.Rank, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, q.Err()
}

func (s *Store) Receipt(ctx context.Context, id string) (Receipt, error) {
	r := Receipt{ID: id}
	err := s.db.QueryRowContext(ctx, `WITH ranked AS (
 SELECT r.seq,r.item_id,ROW_NUMBER() OVER(ORDER BY i.priority DESC,r.seq) AS rank,COUNT(*) OVER() AS total
 FROM api_receipts r JOIN items i ON i.id=r.item_id)
 SELECT seq,rank,total FROM ranked WHERE item_id=?`, id).Scan(&r.Sequence, &r.Rank, &r.Total)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

type Event struct {
	Sequence int64
	ID       string
	Kind     string
}

func (s *Store) Events(ctx context.Context, since int64, limit int) ([]Event, error) {
	q, err := s.db.QueryContext(ctx, `SELECT seq,item_id,kind FROM api_events WHERE seq>? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []Event{}
	for q.Next() {
		var e Event
		if err := q.Scan(&e.Sequence, &e.ID, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, q.Err()
}

func (s *Store) EventCursor(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM api_events`).Scan(&n)
	return n, err
}
