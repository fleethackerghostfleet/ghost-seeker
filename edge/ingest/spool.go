package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch ingests stable regular files, acknowledging them by rename only after
// the ingester has stored/scored (or folded) the capture. Keep producers outside
// processed/ and failed/; filenames must not be reused while pending.
func Watch(ctx context.Context, q *Queue, dir string, defaults Metadata, maxBytes int64, poll time.Duration) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFrameBytes
	}
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	for _, sub := range []string{"processed", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return err
		}
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	previous := map[string]spoolStamp{}
	for ctx.Err() == nil {
		current, err := scanSpool(dir)
		if err != nil {
			return err
		}
		for path, stamps := range current {
			if err := ctx.Err(); err != nil {
				return err
			}
			if previous[path] != stamps {
				continue
			}
			if err := spoolFile(ctx, q, path, defaults, maxBytes, stamps); err != nil {
				return err
			}
		}
		previous = current
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return ctx.Err()
}

type spoolStamp struct{ image, meta fileStamp }

func spoolStamps(path string) spoolStamp { return spoolStamp{stamp(path), stamp(path + ".json")} }

func scanSpool(dir string) (map[string]spoolStamp, error) {
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	stamps := map[string]spoolStamp{}
	for {
		entries, err := d.ReadDir(128)
		for _, entry := range entries {
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || (ext != ".jpg" && ext != ".jpeg" && ext != ".png") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if s := spoolStamps(path); s.image.exists {
				stamps[path] = s
			}
		}
		if err == io.EOF {
			return stamps, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

type fileStamp struct {
	size   int64
	at     time.Time
	exists bool
}

func stamp(path string) fileStamp {
	f, err := os.Lstat(path)
	if err != nil || !f.Mode().IsRegular() {
		return fileStamp{}
	}
	return fileStamp{size: f.Size(), at: f.ModTime(), exists: true}
}

func spoolFile(ctx context.Context, q *Queue, path string, defaults Metadata, maxBytes int64, stable spoolStamp) error {
	a, meta := stable.image, stable.meta
	if !a.exists || stable != spoolStamps(path) {
		return nil
	}
	if !q.reserve() {
		q.count("spool:"+defaults.Source, func(s *SourceStats) { s.Rejected++ })
		return nil
	}
	reserved := true
	defer func() {
		if reserved {
			<-q.slots
		}
	}()
	m := defaults
	var data []byte
	var err error
	if a.size > maxBytes {
		err = errors.New("frame exceeds byte cap")
	} else {
		data, err = readBounded(path, maxBytes)
	}
	if err == nil && meta.exists {
		var b []byte
		b, err = readBounded(path+".json", 64<<10)
		if err == nil {
			err = json.Unmarshal(b, &m)
		}
	}
	if a != stamp(path) || meta != stamp(path+".json") {
		return nil
	}
	key := "spool:" + m.Source
	submitted := false
	if err == nil {
		var c *Capture
		c, err = m.Capture(data)
		if err == nil {
			submitted = true
			reserved = false
			_, err = q.submit(ctx, c, key)
			if errors.Is(err, ErrBusy) || ctx.Err() != nil {
				return nil
			}
		}
	}
	sub := "processed"
	if err != nil {
		sub = "failed"
		if !submitted {
			q.count(key, func(s *SourceStats) { s.Failed++ })
		}
	}
	// Unique suffixes prevent overwriting an earlier file with the same name.
	dest := filepath.Join(filepath.Dir(path), sub, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(path)))
	if err := os.Rename(path, dest); err != nil {
		return err
	}
	if meta.exists {
		return os.Rename(path+".json", dest+".json")
	}
	return nil
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, errors.New("file exceeds byte cap")
	}
	return b, err
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Pump fans any existing Source (including replay) into the single live writer.
func Pump(ctx context.Context, q *Queue, src Source, transport string) error {
	for {
		c, done, err := q.nextCapture(ctx, src)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		_, err = q.submit(ctx, c, transport+":"+c.Source)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

type admittedSource interface {
	nextAdmitted(context.Context, func() error, func()) (*Capture, bool, error)
}

func (q *Queue) nextCapture(ctx context.Context, src Source) (*Capture, bool, error) {
	if src, ok := src.(admittedSource); ok {
		return src.nextAdmitted(ctx, func() error { return q.waitSlot(ctx) }, func() { <-q.slots })
	}
	if err := q.waitSlot(ctx); err != nil {
		return nil, false, err
	}
	c, done, err := src.Next(ctx)
	if done || err != nil {
		<-q.slots
	}
	return c, done, err
}
