package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

var ErrFull = errors.New("edge storage cap reached: protected backlog must drain")

// Limits bound retained payload bytes and item rows, not SQLite's high-water
// size or the audit log. Zero means unlimited; no age-based deletion is done.
type Limits struct{ MaxBytes, MaxItems int64 }

func (s *Store) SetLimits(ctx context.Context, limits Limits) error {
	if limits.MaxBytes < 0 || limits.MaxItems < 0 {
		return errors.New("storage caps must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = limits
	return s.makeRoom(ctx, 0, 0, "")
}

// EnforceLimits is called after a sync response, including its pulls, is applied.
func (s *Store) EnforceLimits(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.makeRoom(ctx, 0, 0, "")
}

// SetPinned protects a local item independently of temporary pull boosts.
func (s *Store) SetPinned(ctx context.Context, itemID string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pinned {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO pins(item_id) SELECT id FROM items WHERE id=?`, itemID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM pins WHERE item_id=?`, itemID)
	return err
}

func (s *Store) ItemsEvicted(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM eviction_log`).Scan(&n)
	return n, err
}

type eviction struct {
	id    string
	bytes int64
	score float64
}

func (s *Store) makeRoom(ctx context.Context, extraBytes, extraItems int64, protectedID string) error {
	if err := s.cleanupEvictions(ctx); err != nil {
		return err
	}
	l := s.limits
	if l.MaxBytes == 0 && l.MaxItems == 0 {
		return nil
	}
	var bytes, items int64
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COALESCE(SUM(bytes),0) FROM forms), COUNT(*) FROM items`).Scan(&bytes, &items); err != nil {
		return err
	}
	over := func() bool {
		return (l.MaxBytes > 0 && bytes+extraBytes > l.MaxBytes) || (l.MaxItems > 0 && items+extraItems > l.MaxItems)
	}
	if !over() {
		return nil
	}
	// Both the manifest and every form must be fully acknowledged. A completed
	// FACT alone is not delivery; not-ready forms also protect the item.
	q, err := s.db.QueryContext(ctx, `SELECT i.id, (SELECT COALESCE(SUM(bytes),0) FROM forms WHERE item_id=i.id), i.priority
FROM items i WHERE i.manifest_acked=1 AND i.pull_boost=0 AND i.pull_form=0 AND i.pull_by=''
AND i.id<>? AND NOT EXISTS(SELECT 1 FROM pins WHERE item_id=i.id)
AND EXISTS(SELECT 1 FROM forms WHERE item_id=i.id)
AND NOT EXISTS(SELECT 1 FROM forms WHERE item_id=i.id AND (complete=0 OR ready=0 OR acked_through<bytes))
ORDER BY i.priority ASC, i.captured_at ASC, i.ingest_seq ASC, i.id ASC`, protectedID)
	if err != nil {
		return err
	}
	var candidates []eviction
	for q.Next() {
		var e eviction
		if err := q.Scan(&e.id, &e.bytes, &e.score); err != nil {
			q.Close()
			return err
		}
		candidates = append(candidates, e)
	}
	err = q.Err()
	q.Close()
	if err != nil {
		return err
	}
	var selected []eviction
	for _, e := range candidates {
		if !over() {
			break
		}
		selected = append(selected, e)
		bytes -= e.bytes
		items--
	}
	full := over()
	// An admission that cannot fit must not evict useful local copies in vain.
	if full && (extraBytes > 0 || extraItems > 0) {
		return s.backpressure(bytes, items, extraBytes, extraItems)
	}
	if len(selected) > 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		at := time.Now().UnixMilli()
		for _, e := range selected {
			if !safeBlobID(e.id) {
				return fmt.Errorf("invalid eviction item id")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO eviction_log(item_id,bytes,score,reason,ts) VALUES(?,?,?,?,?)`, e.id, e.bytes, e.score, "storage_cap", at); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM forms WHERE item_id=?`, e.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id=?`, e.id); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		for _, e := range selected {
			slog.Info("edge item evicted", "item_id", e.id, "bytes", e.bytes, "score", e.score, "reason", "storage_cap", "time_unix_ms", at)
		}
		if err := s.cleanupEvictions(ctx); err != nil {
			return err
		}
	}
	if full {
		return s.backpressure(bytes, items, extraBytes, extraItems)
	}
	return nil
}

func (s *Store) backpressure(bytes, items, extraBytes, extraItems int64) error {
	slog.Warn("edge storage backpressure: refusing intake; protected items will not be deleted", "bytes", bytes, "items", items, "incoming_bytes", extraBytes, "incoming_items", extraItems, "max_bytes", s.limits.MaxBytes, "max_items", s.limits.MaxItems)
	return ErrFull
}

// Persisted cleanup markers make a crash between the DB commit and unlink
// recoverable. Admission cannot proceed until those payloads are removed.
func (s *Store) cleanupEvictions(ctx context.Context) error {
	q, err := s.db.QueryContext(ctx, `SELECT seq,item_id FROM eviction_log WHERE cleanup_pending=1 ORDER BY seq`)
	if err != nil {
		return err
	}
	type pending struct {
		seq int64
		id  string
	}
	var list []pending
	for q.Next() {
		var p pending
		if err := q.Scan(&p.seq, &p.id); err != nil {
			q.Close()
			return err
		}
		list = append(list, p)
	}
	err = q.Err()
	q.Close()
	if err != nil {
		return err
	}
	for _, p := range list {
		if !safeBlobID(p.id) {
			return errors.New("invalid eviction cleanup id")
		}
		if err := os.RemoveAll(filepath.Join(s.blobDir, p.id)); err != nil {
			return fmt.Errorf("eviction cleanup: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE eviction_log SET cleanup_pending=0 WHERE seq=?`, p.seq); err != nil {
			return err
		}
	}
	return nil
}

func safeBlobID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !filepath.IsAbs(id)
}
