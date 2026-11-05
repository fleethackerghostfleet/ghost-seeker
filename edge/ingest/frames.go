package ingest

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

// FrameSet is a directory or zip of frames plus the ground truth the mock
// analyzer should report for them. Real analyzers ignore Hints.
type FrameSet struct {
	Path        string // .zip or directory
	Source      string
	Label       string  // detector label, "" = nothing detected
	Conf        float64 // detector confidence
	Unknownness float64 // 0..1
	Truth       string  // what it really is (for unknowns), e.g. "dolphin"
	Every       int     // use every Nth frame (default 1)
	Limit       int     // max frames (0 = all)
}

type frame struct {
	name string
	read func() ([]byte, error)
}

func (fs FrameSet) frames() ([]frame, error) {
	var out []frame
	if strings.HasSuffix(fs.Path, ".zip") {
		zr, err := zip.OpenReader(fs.Path)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		path := fs.Path
		for i, f := range zr.File {
			if f.FileInfo().IsDir() || !isImage(f.Name) {
				continue
			}
			i := i
			// Re-open per read so no descriptor outlives the call; the
			// central directory is small next to a JPEG decode.
			out = append(out, frame{name: f.Name, read: func() ([]byte, error) {
				zr, err := zip.OpenReader(path)
				if err != nil {
					return nil, err
				}
				defer zr.Close()
				rc, err := zr.File[i].Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}})
		}
	} else {
		entries, err := os.ReadDir(fs.Path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !isImage(e.Name()) {
				continue
			}
			p := filepath.Join(fs.Path, e.Name())
			out = append(out, frame{name: e.Name(), read: func() ([]byte, error) { return os.ReadFile(p) }})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	every := max(fs.Every, 1)
	var picked []frame
	for i, f := range out {
		if i%every != 0 {
			continue
		}
		picked = append(picked, f)
		if fs.Limit > 0 && len(picked) >= fs.Limit {
			break
		}
	}
	return picked, nil
}

func isImage(n string) bool {
	l := strings.ToLower(n)
	return strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".jpeg") || strings.HasSuffix(l, ".png")
}

// Replay interleaves several frame sets into one capture stream at a fixed
// rate in clock time. Sets are mixed proportionally so a short set (a few
// dolphin frames) is spread across a long one (hours of cargo ship).
type Replay struct {
	clk    clock.Clock
	period time.Duration
	queue  []replayFrame
	i      int
	mu     sync.Mutex
	bursts map[string]replayBurst
}

type replayFrame struct {
	Capture
	read func() ([]byte, error)
}

type replayBurst struct {
	period time.Duration
	until  time.Time
}

func NewReplay(clk clock.Clock, period time.Duration, sets ...FrameSet) (*Replay, error) {
	var lists [][]replayFrame
	for _, s := range sets {
		frs, err := s.frames()
		if err != nil {
			return nil, err
		}
		var l []replayFrame
		for _, f := range frs {
			hints := map[string]string{"truth": s.Truth}
			if s.Label != "" {
				hints["label"] = s.Label
				hints["conf"] = ftoa(s.Conf)
			}
			if s.Unknownness > 0 {
				hints["unknownness"] = ftoa(s.Unknownness)
			}
			l = append(l, replayFrame{Capture: Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Source: s.Source, Hints: hints,
				Tags: map[string]string{"frame": f.name}}, read: f.read})
		}
		if len(l) > 0 {
			lists = append(lists, l)
		}
	}
	// Proportional interleave.
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	idx := make([]int, len(lists))
	var q []replayFrame
	for len(q) < total {
		best, bestFrac := -1, -1.0
		for k, l := range lists {
			if idx[k] >= len(l) {
				continue
			}
			frac := float64(idx[k]) / float64(len(l))
			if best == -1 || frac < bestFrac {
				best, bestFrac = k, frac
			}
		}
		q = append(q, lists[best][idx[best]])
		idx[best]++
	}
	return &Replay{clk: clk, period: period, queue: q, bursts: make(map[string]replayBurst)}, nil
}

func (r *Replay) Len() int { return len(r.queue) }

func (r *Replay) Burst(source string, fps float64, d time.Duration) {
	if fps <= 0 || d <= 0 {
		return
	}
	period := time.Duration(float64(time.Second) / fps)
	if period <= 0 || period >= r.period {
		return
	}
	now := r.clk.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bursts == nil {
		r.bursts = make(map[string]replayBurst)
	}
	burst := r.bursts[source]
	burst.period = period
	if deadline := now.Add(d); deadline.After(burst.until) {
		burst.until = deadline
	}
	r.bursts[source] = burst
}

func (r *Replay) Next(ctx context.Context) (*Capture, bool, error) {
	if r.i >= len(r.queue) {
		return nil, true, nil
	}
	if r.i > 0 {
		now := r.clk.Now()
		r.mu.Lock()
		period := r.period
		source := r.queue[r.i].Source
		if burst, ok := r.bursts[source]; ok {
			if now.Before(burst.until) && burst.period > 0 {
				period = burst.period
			} else {
				delete(r.bursts, source)
			}
		}
		r.mu.Unlock()
		if err := r.clk.Sleep(ctx, period); err != nil {
			return nil, false, err
		}
	}
	f := r.queue[r.i]
	c := f.Capture
	if f.read != nil {
		data, err := f.read()
		if err != nil {
			return nil, false, err
		}
		c.Data = data
	}
	r.i++
	c.CapturedAt = r.clk.Now()
	return &c, false, nil
}

func ftoa(f float64) string {
	return strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
}

func formatFloat(f float64) string {
	b := make([]byte, 0, 8)
	return string(appendFloat(b, f))
}
