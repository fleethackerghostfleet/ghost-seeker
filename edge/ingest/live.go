package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/imglimit"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

const DefaultMaxFrameBytes int64 = 32 << 20

var ErrBusy = errors.New("live frame queue full")

type Metadata struct {
	Source     string            `json:"source"`
	CapturedAt time.Time         `json:"captured_at,omitempty"`
	Geo        *Geo              `json:"geo,omitempty"`
	SensorType string            `json:"sensor_type,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type Geo struct {
	Lat     *float64 `json:"lat"`
	Lon     *float64 `json:"lon"`
	Alt     *float64 `json:"alt,omitempty"`
	Heading float64  `json:"heading,omitempty"`
}

func (m Metadata) Capture(data []byte) (*Capture, error) {
	if !ids.ValidName(m.Source) {
		return nil, errors.New("source is required: 1-128 characters [A-Za-z0-9_.:/-]")
	}
	if m.SensorType != "" && (!ids.Valid(m.SensorType) || len(m.SensorType) > 32) {
		return nil, errors.New("invalid sensor_type")
	}
	if len(m.Tags) > 64 {
		return nil, errors.New("at most 64 tags allowed")
	}
	tags := map[string]string{}
	for k, v := range m.Tags {
		if !ids.ValidName(k) || len(v) > 1024 {
			return nil, errors.New("invalid tag key or value (maximum 1024 bytes)")
		}
		tags[k] = v
	}
	c := &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Source: m.Source, CapturedAt: m.CapturedAt, Data: data, Tags: tags}
	if c.CapturedAt.IsZero() {
		c.CapturedAt = time.Now().UTC()
	}
	if c.CapturedAt.Year() < 1970 || c.CapturedAt.Year() > 9999 {
		return nil, errors.New("captured_at outside supported range")
	}
	if m.SensorType != "" {
		tags["sensor_type"] = m.SensorType
	}
	if g := m.Geo; g != nil {
		finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
		if g.Lat == nil || g.Lon == nil || !finite(*g.Lat) || !finite(*g.Lon) || !finite(g.Heading) || math.Abs(*g.Lat) > 90 || math.Abs(*g.Lon) > 180 || g.Heading < 0 || g.Heading >= 360 || (g.Alt != nil && !finite(*g.Alt)) {
			return nil, errors.New("invalid geo coordinates or heading")
		}
		c.Geo = &pb.Geo{Lat: *g.Lat, Lon: *g.Lon, HeadingDeg: g.Heading}
		if g.Alt != nil {
			tags["alt_m"] = strconv.FormatFloat(*g.Alt, 'f', -1, 64)
		}
	}
	_, format, err := imglimit.Decode(data, 0)
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("a valid JPEG or PNG within the 50 MP pixel cap is required")
	}
	return c, nil
}

type Result struct {
	ID        string `json:"id"`
	Duplicate bool   `json:"duplicate"`
	err       error
}

type SourceStats struct {
	Accepted   uint64     `json:"accepted"`
	Processed  uint64     `json:"processed"`
	Duplicates uint64     `json:"duplicates"`
	Rejected   uint64     `json:"rejected"`
	Failed     uint64     `json:"failed"`
	LastFrame  *time.Time `json:"last_frame_at"`
}

// Queue bounds readers, buffered captures, and the frame currently in inference
// together. Exactly one Ingester.Run drains it; all producers share that writer.
type Queue struct {
	frames chan *Capture
	slots  chan struct{}
	mu     sync.Mutex
	stats  map[string]SourceStats
}

func NewQueue(capacity int) *Queue {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue{frames: make(chan *Capture, capacity), slots: make(chan struct{}, capacity), stats: map[string]SourceStats{}}
}

func (q *Queue) Next(ctx context.Context) (*Capture, bool, error) {
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case c := <-q.frames:
		return c, false, nil
	}
}

func (q *Queue) count(key string, update func(*SourceStats)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.stats[key]; !ok && len(q.stats) >= 256 {
		key = "other"
	}
	s := q.stats[key]
	update(&s)
	q.stats[key] = s
}

func (q *Queue) Health(w http.ResponseWriter, _ *http.Request) {
	q.mu.Lock()
	sources := make(map[string]SourceStats, len(q.stats))
	for key, stats := range q.stats {
		sources[key] = stats
	}
	q.mu.Unlock()
	liveJSON(w, 200, map[string]any{"status": "ok", "in_flight": len(q.slots), "capacity": cap(q.slots), "sources": sources})
}

func (q *Queue) reserve() bool {
	select {
	case q.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (q *Queue) submit(ctx context.Context, c *Capture, key string) (Result, error) {
	done := make(chan Result, 1)
	c.complete = func(row *store.Row, err error, duplicate bool) {
		r := Result{Duplicate: duplicate, err: err}
		if row != nil {
			r.ID = row.Item.Id
		}
		q.count(key, func(s *SourceStats) {
			if err != nil {
				s.Failed++
			} else {
				s.Processed++
				if duplicate {
					s.Duplicates++
				}
			}
		})
		<-q.slots
		done <- r
	}
	select {
	case q.frames <- c:
		now := time.Now().UTC()
		q.count(key, func(s *SourceStats) { s.Accepted++; s.LastFrame = &now })
	case <-ctx.Done():
		<-q.slots
		return Result{}, ctx.Err()
	}
	select {
	case r := <-done:
		return r, r.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// Push is used by local/pull producers. It uses the same admission bound as HTTP.
func (q *Queue) Push(ctx context.Context, c *Capture, transport string) (Result, error) {
	key := transport + ":" + c.Source
	if !q.reserve() {
		q.count(key, func(s *SourceStats) { s.Rejected++ })
		return Result{}, ErrBusy
	}
	return q.submit(ctx, c, key)
}

// Handler requires the configured operator credential or this edge's unit JWT.
// Neither credential is taken from a URL or logged.
func (q *Queue) Handler(operator, unit string, maxBytes int64) http.Handler {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFrameBytes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := opauth.BearerToken(r)
		if !opauth.Equal(operator, bearer) && !(opauth.Equal(unit, bearer) && unitauth.ConfiguredTokenActive(unit)) {
			opauth.Unauthorized(w)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			liveError(w, 405, "POST required")
			return
		}
		if !q.reserve() {
			q.count("push", func(s *SourceStats) { s.Rejected++ })
			w.Header().Set("Retry-After", "1")
			liveError(w, 429, ErrBusy.Error())
			return
		}
		reserved := true
		defer func() {
			if reserved {
				<-q.slots
			}
		}()
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		defer r.Body.Close()
		m, data, err := readFrame(r)
		if err != nil {
			q.count("push", func(s *SourceStats) { s.Rejected++ })
			var large *http.MaxBytesError
			code := 400
			if errors.As(err, &large) {
				code = 413
			}
			if errors.Is(err, errContentType) {
				code = 415
			}
			liveError(w, code, err.Error())
			return
		}
		c, err := m.Capture(data)
		if err != nil {
			q.count("push", func(s *SourceStats) { s.Rejected++ })
			liveError(w, 400, err.Error())
			return
		}
		reserved = false
		result, err := q.submit(r.Context(), c, "push:"+c.Source)
		if err != nil {
			liveError(w, 503, "ingest unavailable; retry may fold a duplicate")
			return
		}
		liveJSON(w, 202, result)
	})
}

var errContentType = errors.New("content type must be image/jpeg, image/png or multipart/form-data")

func readFrame(r *http.Request) (Metadata, []byte, error) {
	m := Metadata{Source: r.URL.Query().Get("source"), SensorType: r.URL.Query().Get("sensor_type")}
	if at := r.URL.Query().Get("captured_at"); at != "" {
		var err error
		m.CapturedAt, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return m, nil, errors.New("invalid captured_at")
		}
	}
	t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return m, nil, errContentType
	}
	if t == "image/jpeg" || t == "image/png" {
		b, err := io.ReadAll(r.Body)
		if err == nil {
			err = checkType(b, t)
		}
		return m, b, err
	}
	if t != "multipart/form-data" {
		return m, nil, errContentType
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return m, nil, err
	}
	var data []byte
	seenMeta := false
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, err
		}
		switch p.FormName() {
		case "meta":
			if seenMeta {
				return m, nil, errors.New("duplicate meta part")
			}
			seenMeta = true
			b, err := io.ReadAll(io.LimitReader(p, 64<<10+1))
			if err != nil {
				return m, nil, err
			}
			if len(b) > 64<<10 {
				return m, nil, errors.New("meta exceeds 64 KiB")
			}
			if err := json.Unmarshal(b, &m); err != nil {
				return m, nil, errors.New("invalid meta JSON")
			}
		case "frame":
			if data != nil {
				return m, nil, errors.New("duplicate frame part")
			}
			t, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if t != "image/jpeg" && t != "image/png" {
				return m, nil, errContentType
			}
			data, err = io.ReadAll(p)
			if err != nil {
				return m, nil, err
			}
			if err := checkType(data, t); err != nil {
				return m, nil, err
			}
		default:
			return m, nil, errors.New("expected frame and optional meta parts")
		}
	}
	if data == nil {
		return m, nil, errors.New("frame part required")
	}
	// Consume epilogue too: the body cap applies to the entire request.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return m, nil, err
	}
	return m, data, nil
}

func checkType(data []byte, contentType string) error {
	_, format, err := imglimit.Check(data, 0)
	if err != nil {
		return fmt.Errorf("invalid image: %w", err)
	}
	if "image/"+strings.ReplaceAll(format, "jpg", "jpeg") != contentType {
		return errors.New("image does not match declared content type")
	}
	return nil
}

func liveJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func liveError(w http.ResponseWriter, status int, message string) {
	liveJSON(w, status, map[string]string{"code": http.StatusText(status), "message": message})
}
