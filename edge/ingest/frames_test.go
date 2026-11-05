package ingest

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

func TestReplayReadsFramesOnEmission(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		name := "directory"
		if zipped {
			name = "zip"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := dir
			write := func(value string) {
				t.Helper()
				if !zipped {
					if err := os.WriteFile(filepath.Join(dir, "001.jpg"), []byte(value), 0600); err != nil {
						t.Fatal(err)
					}
					return
				}
				path = filepath.Join(dir, "frames.zip")
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				w := zip.NewWriter(f)
				entry, err := w.Create("img1/001.jpg")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte(value)); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			write("before")
			r, err := NewReplay(clock.NewVirtual(time.Now()), time.Second, FrameSet{Path: path, Source: name, Label: "boat"})
			if err != nil {
				t.Fatal(err)
			}
			write("after")
			c, done, err := r.Next(context.Background())
			if err != nil || done || string(c.Data) != "after" || c.Hints["label"] != "boat" {
				t.Fatalf("capture = %+v, done = %v, err = %v", c, done, err)
			}
			if r.queue[0].Data != nil {
				t.Fatal("replay retained emitted image bytes")
			}
			if _, done, err := r.Next(context.Background()); err != nil || !done {
				t.Fatalf("end = %v, %v", done, err)
			}
		})
	}
}

func TestReplayLazyReadFailureAndEmpty(t *testing.T) {
	r, err := NewReplay(clock.NewVirtual(time.Now()), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c, done, err := r.Next(context.Background()); c != nil || !done || err != nil {
		t.Fatalf("empty replay = %v, %v, %v", c, done, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "001.jpg")
	if err := os.WriteFile(path, []byte("frame"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "002.jpg"), []byte("next frame"), 0600); err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(dir, "003.jpg")
	if err := os.WriteFile(last, []byte("last frame"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = NewReplay(clock.NewVirtual(time.Now()), time.Second, FrameSet{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(last); err != nil {
		t.Fatal(err)
	}
	if c, done, err := r.Next(context.Background()); err != nil || done || c == nil || string(c.Data) != "next frame" || c.Tags["frame"] != "002.jpg" {
		t.Fatalf("capture after missing frame = %v, %v, %v", c, done, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, done, err := r.Next(ctx); c != nil || done || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay = %v, %v, %v", c, done, err)
	}
	if c, done, err := r.Next(context.Background()); c != nil || !done || err != nil {
		t.Fatalf("missing final frame = %v, %v, %v", c, done, err)
	}
}

func TestReplayInterleavesSortedFrames(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, p := range []string{filepath.Join(a, "002.jpg"), filepath.Join(a, "001.jpg"), filepath.Join(b, "001.jpg")} {
		if err := os.WriteFile(p, []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReplay(clock.NewVirtual(time.Now()), time.Second, FrameSet{Path: a, Source: "a"}, FrameSet{Path: b, Source: "b"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a/001.jpg", "b/001.jpg", "a/002.jpg"} {
		c, done, err := r.Next(context.Background())
		if err != nil || done || c.Source+"/"+c.Tags["frame"] != want {
			t.Fatalf("capture = %+v, done = %v, err = %v; want %s", c, done, err, want)
		}
	}
}
