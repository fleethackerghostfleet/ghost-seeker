// Package edgesim simulates edge sensors: it synthesizes frames, runs a cheap
// first-pass triage, drops low-value frames on the "device" and uploads the
// keepers to the command server's ingest API.
package edgesim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scoring"
)

var pools = map[string]map[string]float64{
	"harbor": {"cargo ship": 3, "tanker": 2, "small boat": 4, "fishing vessel": 2, "warship": 1.4, "person": .5, "bird": 3, "buoy": 2, "helicopter": .8, "drone": .8},
	"open":   {"cargo ship": 3, "tanker": 2, "warship": 1.2, "submarine": .5, "fishing vessel": 2, "small boat": 2, "debris": 1, "bird": 2, "military aircraft": .8, "helicopter": .6},
	"aerial": {"small boat": 4, "cargo ship": 2, "fishing vessel": 3, "warship": 1, "debris": 1.2, "person": .7, "submarine": .4, "tanker": 1},
	"sky":    {"civil aircraft": 3, "military aircraft": 2, "helicopter": 1.5, "drone": 1.5, "bird": 4},
	"sat":    {"cargo ship": 3, "tanker": 2, "warship": 1, "fishing vessel": 2, "small boat": 1.5},
}

func pickClass(scene string) string {
	p := pools[scene]
	keys := make([]string, 0, len(p))
	for _, k := range catalog.ClassOrder {
		if _, ok := p[k]; ok {
			keys = append(keys, k)
		}
	}
	ws := make([]float64, len(keys))
	for i, k := range keys {
		ws[i] = p[k]
	}
	return wpick(keys, ws)
}

func place(cls, scene string, hz float64, existing []placed) placed {
	c := catalog.Classes[cls]
	var b placed
	for t := 0; t < 14; t++ {
		switch {
		case scene == "aerial" || scene == "sat":
			k := 1.0
			if scene == "sat" {
				k = .6
			}
			L := c.Length * W * rnd(.5, .9) * k
			B := L * math.Min(.32, c.Aspect*.5)
			a := rnd(0, math.Pi)
			cx := rnd(L/2+6, W-L/2-6)
			cy := rnd(L/2*math.Abs(math.Sin(a))+8, H-L/2*math.Abs(math.Sin(a))-8)
			w := math.Abs(L*math.Cos(a)) + math.Abs(B*math.Sin(a))
			h := math.Abs(L*math.Sin(a)) + math.Abs(B*math.Cos(a))
			b = placed{x: cx - w/2, y: cy - h/2, w: w, h: h, top: true, cx: cx, cy: cy, l: L, bw: B, a: a}
		case catalog.Airborne[cls]:
			k := 1.0
			if scene == "sky" {
				k = 1.5
			}
			w := c.Length * W * rnd(.7, 1.25) * k
			h := w * c.Aspect
			top, bot := 10.0, hz*H-12-h
			b = placed{x: rnd(8, W-w-8), y: rnd(top, math.Max(top+1, bot)), w: w, h: h}
		default:
			yb := rnd(hz+.05, .97)
			s := (yb - hz) / (1 - hz)
			w := c.Length * W * (.35 + s) * rnd(.85, 1.15)
			h := w * c.Aspect
			b = placed{x: rnd(4, W-w-4), y: yb*H - h, w: w, h: h}
		}
		ok := true
		for _, e := range existing {
			if !(b.x+b.w < e.x || e.x+e.w < b.x || b.y+b.h < e.y || e.y+e.h < b.y) {
				ok = false
				break
			}
		}
		if ok {
			break
		}
	}
	return b
}

func attrs(cls string) map[string]bool {
	g := catalog.Classes[cls].Group
	a := map[string]bool{}
	switch {
	case g == "vessel":
		a["moving"] = chance(.5)
		a["approaching"] = a["moving"] && chance(.45)
		a["dark"] = chance(.16)
		a["loitering"] = !a["moving"] && chance(.25)
	case g == "aircraft" || cls == "drone":
		a["moving"] = true
		a["approaching"] = chance(.35)
		a["loitering"] = cls != "civil aircraft" && chance(.15)
		a["dark"] = chance(.1)
	case cls == "person" || cls == "debris":
		a["loitering"] = true
	}
	a["new"] = chance(.25)
	for k, v := range a {
		if !v {
			delete(a, k)
		}
	}
	return a
}

// Frame is one captured, triaged frame.
type Frame struct {
	Report catalog.Report
	Image  []byte
	Kept   bool
}

// Sensor holds per-device state (frame counter, frames dropped since last upload).
type Sensor struct {
	Src     catalog.Source
	frame   int
	pending int
}

func NewSensor(src catalog.Source) *Sensor {
	return &Sensor{Src: src, frame: 4200 + rand.IntN(900)}
}

func uplinkMs(link string) int {
	switch {
	case strings.Contains(link, "Iridium"):
		return int(rnd(4000, 9000))
	case strings.Contains(link, "MANET"):
		return int(rnd(300, 900))
	case strings.Contains(link, "downlink"):
		return int(rnd(60000, 180000))
	}
	return int(rnd(40, 160))
}

// Capture synthesizes a frame, runs edge triage and returns it. Frames below
// the keep threshold are counted and discarded without rendering.
func (s *Sensor) Capture(now time.Time) Frame {
	scene := s.Src.Scene
	hz := 0.0
	switch scene {
	case "harbor":
		hz = rnd(.36, .42)
	case "open":
		hz = rnd(.34, .46)
	case "sky":
		hz = .9
	}
	count := int(wpick([]string{"0", "1", "2", "3"}, []float64{.6, 4, 3, 1.3})[0] - '0')
	var dets []placed
	var edge []float64
	for i := 0; i < count; i++ {
		cls := pickClass(scene)
		p := place(cls, scene, hz, dets)
		p.Detection = catalog.Detection{Class: cls, Conf: math.Round(rnd(.55, .98)*100) / 100, Attrs: attrs(cls)}
		dets = append(dets, p)
		edge = append(edge, catalog.EdgeWeights[catalog.Classes[cls].Group]*p.Conf*rnd(.75, 1))
	}
	score := rnd(0, .05)
	if len(dets) > 0 {
		score = scoring.NoisyOr(edge)
	}
	s.frame += 3 + rand.IntN(37)
	burst := 3 + rand.IntN(6) // near-duplicate frames suppressed before scoring
	s.pending += burst + 1
	if score < catalog.EdgeKeepThreshold {
		return Frame{}
	}
	edgeMs, up := int(rnd(14, 60)), uplinkMs(s.Src.Link)
	ts := now.Add(-time.Duration(edgeMs+up) * time.Millisecond)
	r := catalog.Report{
		Source: s.Src.ID, CapturedAt: ts.UnixMilli(), Frame: s.frame,
		EdgeScore: math.Round(score*1000) / 1000, Captured: s.pending, Dropped: s.pending - 1,
		EdgeMs: edgeMs, UplinkMs: up,
	}
	s.pending = 0
	for _, d := range dets {
		det := d.Detection
		det.Box = catalog.Box{X: d.x / W, Y: d.y / H, W: d.w / W, H: d.h / H}
		r.Detections = append(r.Detections, det)
	}
	return Frame{Report: r, Image: Render(s.Src, dets, ts, s.frame, hz), Kept: true}
}

// Uploader posts kept frames to a command server.
type Uploader struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (u *Uploader) Upload(ctx context.Context, fr Frame) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	meta, _ := json.Marshal(fr.Report)
	if err := mw.WriteField("meta", string(meta)); err != nil {
		return err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s-%d.svg"`, fr.Report.Source, fr.Report.Frame))
	h.Set("Content-Type", "image/svg+xml")
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	part.Write(fr.Image)
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(u.BaseURL, "/")+"/api/ingest", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if u.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.Token)
	}
	c := u.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ingest: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// Options configures a simulation run.
type Options struct {
	Sources  []catalog.Source
	Interval [2]time.Duration // random delay between captures across all sensors
	Backfill int              // kept frames to backfill over the past 45 min (0 = none)
}

// Run backfills (optionally) and then streams frames until ctx is done.
func Run(ctx context.Context, u *Uploader, o Options) {
	sensors := make([]*Sensor, len(o.Sources))
	for i, s := range o.Sources {
		sensors[i] = NewSensor(s)
	}
	if o.Backfill > 0 {
		backfill(ctx, u, sensors, o.Backfill)
	}
	for {
		d := o.Interval[0] + time.Duration(rand.Int64N(int64(o.Interval[1]-o.Interval[0])+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		fr := pick(sensors).Capture(time.Now())
		if !fr.Kept {
			continue
		}
		if err := u.Upload(ctx, fr); err != nil && ctx.Err() == nil {
			log.Printf("edge-sim: upload %s: %v", fr.Report.Source, err)
		}
	}
}

func backfill(ctx context.Context, u *Uploader, sensors []*Sensor, n int) {
	t := time.Now().Add(-45 * time.Minute)
	kept := 0
	for i := 0; i < n*3 && kept < n && ctx.Err() == nil; i++ {
		t = t.Add(time.Duration(rnd(8000, 22000)) * time.Millisecond)
		now := t
		if lim := time.Now().Add(-4 * time.Second); now.After(lim) {
			now = lim
		}
		fr := pick(sensors).Capture(now)
		if !fr.Kept {
			continue
		}
		if err := u.Upload(ctx, fr); err != nil {
			log.Printf("edge-sim: backfill upload: %v", err)
			return
		}
		kept++
	}
	log.Printf("edge-sim: backfilled %d frames", kept)
}
