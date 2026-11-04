package main

// Three-way delivery comparison for the ghost-fleet demo:
//
//	naive     every captured frame becomes an item (edge/ingest still drops
//	          byte-identical frames).
//	hash      perceptual (dHash) near-duplicate suppression: a frame whose
//	          hash is within hammingMax of a recently kept frame is dropped.
//	sightings one item per object (open) plus a refresh at most every
//	          -sighting-update. Until F1's sidecar lands, "object" is read
//	          from the clip's MOT ground truth (gt/gt.txt + qa merges), so
//	          this is an oracle upper bound on tracking; the sidecar swaps in
//	          via cmd/edge -infer / -dedup without touching the scenario.
//
// Every mode tags captures with clip / gt_tracks so shore-side metrics can
// count *unique objects* that have an image on shore, independent of the
// dedup strategy.

import (
	"bufio"
	"bytes"
	"context"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	sstore "github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

const (
	hammingMax = 10 // dHash bits; ~ edgeml imgdedup's near-duplicate threshold
	hashWindow = 64 // recently kept frames compared against
)

var dedupModes = []string{"naive", "hash", "sightings"}

// ---- MOT ground truth ----

type clipGT struct {
	byFrame map[int][]string // frame -> object keys ("clip/track")
}

type gtIndex struct {
	clips   map[string]*clipGT // by Source name
	objects []string
}

func (g *gtIndex) at(clip string, frame int) []string {
	if g == nil {
		return nil
	}
	if c := g.clips[clip]; c != nil {
		return c.byFrame[frame]
	}
	return nil
}

// loadGT reads <clip>/gt/gt.txt next to each set's img1 directory. Sets
// without ground truth are simply untracked (their items count as one
// object each, like the live UI does).
func loadGT(sets []ingest.FrameSet) *gtIndex {
	g := &gtIndex{clips: map[string]*clipGT{}}
	seen := map[string]bool{}
	for _, s := range sets {
		dir := s.Path
		if filepath.Base(dir) == "img1" {
			dir = filepath.Dir(dir)
		}
		f, err := os.Open(filepath.Join(dir, "gt", "gt.txt"))
		if err != nil {
			continue
		}
		merge := loadMerges(filepath.Join(dir, "qa", "corrections.yaml"))
		c := &clipGT{byFrame: map[int][]string{}}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Split(strings.TrimSpace(sc.Text()), ",")
			if len(p) < 2 {
				continue
			}
			fr, err1 := strconv.Atoi(p[0])
			id, err2 := strconv.Atoi(p[1])
			if err1 != nil || err2 != nil {
				continue
			}
			if m, ok := merge[id]; ok {
				id = m
			}
			key := s.Source + "/" + strconv.Itoa(id)
			if !contains(c.byFrame[fr], key) {
				c.byFrame[fr] = append(c.byFrame[fr], key)
			}
			if !seen[key] {
				seen[key] = true
				g.objects = append(g.objects, key)
			}
		}
		f.Close()
		g.clips[s.Source] = c
	}
	sort.Strings(g.objects)
	return g
}

var mergeRe = regexp.MustCompile(`(\d+)\s*:\s*\[([^\]]*)\]`)

// loadMerges parses the `merge: {13: [2, 9, ...]}` line of qa/corrections.yaml.
func loadMerges(path string) map[int]int {
	out := map[int]int{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "merge:") {
			continue
		}
		for _, m := range mergeRe.FindAllStringSubmatch(line, -1) {
			into, _ := strconv.Atoi(m[1])
			for _, s := range strings.Split(m[2], ",") {
				if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					out[id] = into
				}
			}
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var frameNoRe = regexp.MustCompile(`(\d+)\.[A-Za-z]+$`)

func frameNo(name string) int {
	m := frameNoRe.FindStringSubmatch(name)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// ---- source wrapper ----

type dedupSource struct {
	inner       ingest.Source
	mode        string
	gt          *gtIndex
	clk         clock.Clock
	updateEvery time.Duration

	hashes   []uint64
	lastSent map[string]time.Time
	Skipped  int64
}

func newDedup(inner ingest.Source, mode string, gt *gtIndex, clk clock.Clock, updateEvery time.Duration) *dedupSource {
	return &dedupSource{inner: inner, mode: mode, gt: gt, clk: clk, updateEvery: updateEvery, lastSent: map[string]time.Time{}}
}

// Next returns (nil, false, nil) for a frame the strategy drops so the caller
// still advances the capture clock by one period.
func (d *dedupSource) Next(ctx context.Context) (*ingest.Capture, bool, error) {
	c, done, err := d.inner.Next(ctx)
	if err != nil || done || c == nil {
		return c, done, err
	}
	objs := d.gt.at(c.Source, frameNo(c.Tags["frame"]))
	if c.Tags == nil {
		c.Tags = map[string]string{}
	}
	c.Tags["clip"] = c.Source
	if len(objs) > 0 {
		c.Tags["gt_tracks"] = strings.Join(objs, ",")
	}
	switch d.mode {
	case "hash":
		h, ok := frameHash(c.Tags["clip"]+"/"+c.Tags["frame"], c.Data)
		if ok {
			for _, p := range d.hashes {
				if bits.OnesCount64(p^h) <= hammingMax {
					d.Skipped++
					return nil, false, nil
				}
			}
			d.hashes = append(d.hashes, h)
			if len(d.hashes) > hashWindow {
				d.hashes = d.hashes[1:]
			}
		}
	case "sightings":
		now := d.clk.Now()
		var pick, event string
		for _, o := range objs {
			last, seen := d.lastSent[o]
			switch {
			case !seen:
				pick, event = o, "open"
			case pick == "" && now.Sub(last) >= d.updateEvery:
				pick, event = o, "update"
			}
			if event == "open" {
				break
			}
		}
		if pick == "" {
			d.Skipped++
			return nil, false, nil
		}
		d.lastSent[pick] = now
		c.Tags["sighting_id"] = pick
		c.Tags["event"] = event
	}
	return c, false, nil
}

// ---- dHash (9x8 grey gradient) with a cross-run cache ----

var (
	hashMu    sync.Mutex
	hashCache = map[string]uint64{}
)

func frameHash(key string, data []byte) (uint64, bool) {
	hashMu.Lock()
	h, ok := hashCache[key]
	hashMu.Unlock()
	if ok {
		return h, true
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, false
	}
	h = dhash(img)
	hashMu.Lock()
	hashCache[key] = h
	hashMu.Unlock()
	return h, true
}

func dhash(img image.Image) uint64 {
	b := img.Bounds()
	w, hgt := b.Dx(), b.Dy()
	var grey [8][9]float64
	// Box-sample each cell on a coarse grid: cheap and robust to noise.
	for gy := 0; gy < 8; gy++ {
		for gx := 0; gx < 9; gx++ {
			x0, x1 := b.Min.X+gx*w/9, b.Min.X+(gx+1)*w/9
			y0, y1 := b.Min.Y+gy*hgt/8, b.Min.Y+(gy+1)*hgt/8
			var sum, n float64
			for y := y0; y < y1; y += 4 {
				for x := x0; x < x1; x += 4 {
					g := color.GrayModel.Convert(img.At(x, y)).(color.Gray)
					sum += float64(g.Y)
					n++
				}
			}
			if n > 0 {
				grey[gy][gx] = sum / n
			}
		}
	}
	var h uint64
	for gy := 0; gy < 8; gy++ {
		for gx := 0; gx < 8; gx++ {
			h <<= 1
			if grey[gy][gx] < grey[gy][gx+1] {
				h |= 1
			}
		}
	}
	return h
}

// ---- shore-side object metrics ----

// deliveredObjects returns the set of gt objects that have at least one
// image form (thumb/crop/full) complete on shore, plus the number of items
// with an image.
func deliveredObjects(items []*sstore.Item) (map[string]bool, int64) {
	objs := map[string]bool{}
	var withImage int64
	for _, it := range items {
		img := false
		for _, f := range it.Forms {
			if f.Complete && (f.Kind == pb.FormKind_FORM_KIND_THUMB || f.Kind == pb.FormKind_FORM_KIND_CROP || f.Kind == pb.FormKind_FORM_KIND_FULL) {
				img = true
				break
			}
		}
		if !img {
			continue
		}
		withImage++
		if it.Features == nil {
			continue
		}
		if tr := it.Features.GetTags()["gt_tracks"]; tr != "" {
			for _, o := range strings.Split(tr, ",") {
				objs[o] = true
			}
		}
	}
	return objs, withImage
}
