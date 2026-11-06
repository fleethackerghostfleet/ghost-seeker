package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

func positiveLimit(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func (s *Store) loadLimits() error {
	var err error
	s.maxItems, err = positiveLimit("GS_SHORE_MAX_ITEMS", 10000)
	if err != nil {
		return err
	}
	s.blobQuota, err = positiveLimit("GS_SHORE_MAX_BLOB_BYTES", 2<<30)
	if err != nil {
		return err
	}
	s.blobSizes = make(map[string]int64)
	return filepath.WalkDir(s.blobDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			s.recordBlob(path, info.Size())
		}
		return nil
	})
}

func (s *Store) itemCapacity(ctx context.Context, id string) error {
	var exists, count int64
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM items WHERE id=?)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		return err
	}
	if count >= s.maxItems {
		return fmt.Errorf("shore item limit reached")
	}
	return nil
}

func (s *Store) recordBlob(path string, size int64) {
	s.blobBytes += size - s.blobSizes[path]
	if size == 0 {
		delete(s.blobSizes, path)
	} else {
		s.blobSizes[path] = size
	}
}

func (s *Store) removeBlob(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.recordBlob(path, 0)
	return nil
}
