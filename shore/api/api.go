// Package api is the versioned, read-only shore integration API. It does not
// depend on the simulation, demo datasets, or console's ephemeral overlays.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	apidocs "github.com/fleethackerghostfleet/ghost-seeker/docs/api"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

type API struct {
	Store        *store.Store
	PollInterval time.Duration
	Heartbeat    time.Duration
	WriteTimeout time.Duration
	clients      chan struct{}
}

func New(st *store.Store) *API {
	return &API{Store: st, PollInterval: 250 * time.Millisecond, Heartbeat: 15 * time.Second, WriteTimeout: 10 * time.Second, clients: make(chan struct{}, 64)}
}

// Handler must be mounted behind opauth. Keeping auth outside also permits the
// existing PIV session middleware to grant read-only viewer access.
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/items", a.list)
	m.HandleFunc("GET /api/v1/items/{id}", a.get)
	m.HandleFunc("GET /api/v1/items/{id}/image", a.image)
	m.HandleFunc("GET /api/v1/stream", a.stream)
	m.HandleFunc("GET /api/v1/health", a.health)
	m.HandleFunc("GET /api/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(apidocs.Spec)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		m.ServeHTTP(w, r)
	})
}

type Contact struct {
	Label      string  `json:"label"`
	ShipType   string  `json:"ship_type"`
	Confidence float32 `json:"confidence"`
}
type Reason struct {
	Scorer string  `json:"scorer"`
	Value  float64 `json:"value"`
	Note   string  `json:"note"`
}
type Form struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Bytes     int64  `json:"bytes"`
	Mime      string `json:"mime"`
}
type DirectiveMatch struct {
	PolicyID string `json:"policy_id"`
	Note     string `json:"note"`
}
type Decision struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	By    string `json:"by"`
}
type Geo struct {
	Lat     float64  `json:"lat"`
	Lon     float64  `json:"lon"`
	Heading float64  `json:"heading"`
	Alt     *float64 `json:"alt,omitempty"`
}
type Item struct {
	ID               string            `json:"id"`
	ReceiptCursor    string            `json:"receipt_cursor"`
	Tier             string            `json:"tier"`
	Score            float64           `json:"score"`
	Reasons          []Reason          `json:"reasons"`
	Explanation      string            `json:"explanation"`
	Contacts         []Contact         `json:"contacts"`
	Source           string            `json:"source"`
	EdgeID           string            `json:"edge_id"`
	CapturedAt       time.Time         `json:"captured_at"`
	ReceivedAt       time.Time         `json:"received_at"`
	Geo              *Geo              `json:"geo"`
	SensorType       string            `json:"sensor_type"`
	Tags             map[string]string `json:"tags"`
	Forms            []Form            `json:"forms"`
	DirectiveMatches []DirectiveMatch  `json:"directive_matches"`
	Decisions        []Decision        `json:"decisions"`
}

func tier(rank, total int) string {
	p := float64(rank) / float64(total)
	switch {
	case rank <= 1 || p <= .10:
		return "P1"
	case p <= .35:
		return "P2"
	case p <= .70:
		return "P3"
	default:
		return "P4"
	}
}

func (a *API) item(ctx context.Context, r store.Receipt) (*Item, error) {
	s, err := a.Store.Get(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	m := s.Manifest
	x := &Item{ID: r.ID, ReceiptCursor: strconv.FormatInt(r.Sequence, 10), Tier: tier(r.Rank, r.Total), Score: m.Priority,
		Source: m.Source, EdgeID: s.EdgeID, CapturedAt: time.UnixMilli(m.CapturedAtUnixMs).UTC(), ReceivedAt: s.ManifestAt.UTC(),
		Contacts: []Contact{}, Reasons: []Reason{}, Tags: map[string]string{}, Forms: []Form{}, DirectiveMatches: []DirectiveMatch{}, Decisions: []Decision{},
		Explanation: "Score is the last edge-reported scheduler priority; reasons describe current shore evidence, not a reconstructed edge score. Tier is a relative shore rank, unless pinned by policy; scene overviews are P4."}
	if math.IsNaN(x.Score) || math.IsInf(x.Score, 0) {
		x.Score = 0
	}
	x.Reasons = append(x.Reasons, Reason{Scorer: "edge_priority", Value: x.Score, Note: "last reported in the edge manifest"})
	f := s.Features
	if f == nil {
		f = &pb.Features{Unknownness: m.Unknownness}
		if m.TopLabel != "" {
			f.Detections = []*pb.Detection{{Label: m.TopLabel, Confidence: m.TopConf}}
		}
	}
	for k, v := range f.Tags {
		x.Tags[k] = v
	}
	x.SensorType = x.Tags["sensor_type"]
	for _, d := range f.Detections {
		x.Contacts = append(x.Contacts, Contact{Label: d.Label, Confidence: d.Confidence, ShipType: x.Tags["subtype"]})
	}
	if s.LabelOverride != "" {
		f.LabelOverride, f.OverrideBy = s.LabelOverride, s.OverrideBy
	}
	if f.LabelOverride != "" {
		x.Decisions = append(x.Decisions, Decision{Kind: "label_override", Label: f.LabelOverride, By: f.OverrideBy})
	}
	// Commander/review decisions currently live only in the UI process. Never
	// fabricate them in shore: decisions contains only persisted shore data.
	if m.Geo != nil {
		x.Geo = &Geo{Lat: m.Geo.Lat, Lon: m.Geo.Lon, Heading: m.Geo.HeadingDeg}
		if alt, err := strconv.ParseFloat(x.Tags["alt_m"], 64); err == nil && !math.IsInf(alt, 0) && !math.IsNaN(alt) {
			x.Geo.Alt = &alt
		}
	}
	for _, f := range s.Forms {
		name := formName(f.Kind)
		if name != "" {
			x.Forms = append(x.Forms, Form{Name: name, Available: f.Complete, Bytes: f.Total, Mime: f.Mime})
		}
	}
	p, err := a.Store.GetPolicy(ctx)
	if err != nil {
		return nil, err
	}
	p = policy.Flatten(p, s.EdgeID, time.Now())
	it := &pb.Item{Id: r.ID, Type: m.Type, Source: m.Source, CapturedAtUnixMs: m.CapturedAtUnixMs, Features: f}
	rc := &scoring.Context{Policy: p, Now: time.Now()}
	unknown := (scoring.Unknown{}).Score(it, rc)
	x.Reasons = append(x.Reasons, Reason{Scorer: "unknown", Value: unknown.Value, Note: unknown.Note})
	interest := (scoring.Interest{}).Score(it, rc)
	if interest.Note != "" {
		x.Reasons = append(x.Reasons, Reason{Scorer: "interest", Value: interest.Value, Note: interest.Note})
		x.DirectiveMatches = append(x.DirectiveMatches, DirectiveMatch{PolicyID: p.GetId(), Note: interest.Note})
	}
	if interest.Suppressed {
		x.Tier = "P4"
	}
	if _, pin := scoring.TierPin(it, p); pin >= 1 && pin <= 4 {
		x.Tier = fmt.Sprintf("P%d", pin)
	}
	if x.Tags["event"] == "scene" {
		x.Tier = "P4"
	}
	return x, nil
}

func parseCursor(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("cursor must be a nonnegative decimal integer")
	}
	return n, nil
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	since, err := parseCursor(r.URL.Query().Get("since"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 1 || limit > 500 {
			fail(w, 400, "limit must be 1..500")
			return
		}
	}
	wanted := r.URL.Query().Get("tier")
	if wanted != "" && wanted != "P1" && wanted != "P2" && wanted != "P3" && wanted != "P4" {
		fail(w, 400, "tier must be P1..P4")
		return
	}
	source := r.URL.Query().Get("source")
	if source != "" && !ids.ValidName(source) {
		fail(w, 400, "invalid source")
		return
	}
	items := []*Item{}
	// A bounded scan budget prevents an expensive no-match filter from holding
	// a request indefinitely. The cursor advances even across filtered rows.
	for scanned := 0; scanned < 2000 && len(items) < limit; {
		rs, err := a.Store.Receipts(r.Context(), since, source, 100)
		if err != nil {
			fail(w, 500, "store unavailable")
			return
		}
		for _, receipt := range rs {
			x, err := a.item(r.Context(), receipt)
			if err != nil {
				fail(w, 500, "store unavailable")
				return
			}
			since = receipt.Sequence
			scanned++
			if wanted == "" || x.Tier == wanted {
				items = append(items, x)
			}
			if len(items) == limit {
				break
			}
		}
		if len(rs) < 100 {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": strconv.FormatInt(since, 10)})
}

func (a *API) lookup(r *http.Request) (*Item, error) {
	id := r.PathValue("id")
	if !ids.Valid(id) {
		return nil, store.ErrNotFound
	}
	receipt, err := a.Store.Receipt(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return a.item(r.Context(), receipt)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	x, err := a.lookup(r)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "item not received")
		return
	}
	if err != nil {
		fail(w, 500, "store unavailable")
		return
	}
	writeJSON(w, 200, x)
}

func formName(k pb.FormKind) string {
	switch k {
	case pb.FormKind_FORM_KIND_FACT:
		return "fact"
	case pb.FormKind_FORM_KIND_THUMB:
		return "thumb"
	case pb.FormKind_FORM_KIND_CROP:
		return "crop"
	case pb.FormKind_FORM_KIND_FULL:
		return "full"
	}
	return ""
}

func (a *API) image(w http.ResponseWriter, r *http.Request) {
	form := r.URL.Query().Get("form")
	if form == "" {
		form = "thumb"
	}
	kind := map[string]pb.FormKind{"thumb": pb.FormKind_FORM_KIND_THUMB, "crop": pb.FormKind_FORM_KIND_CROP, "full": pb.FormKind_FORM_KIND_FULL}[form]
	if kind == 0 {
		fail(w, 400, "form must be thumb, crop or full")
		return
	}
	if !ids.Valid(r.PathValue("id")) {
		fail(w, 404, "item not received")
		return
	}
	s, err := a.Store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "item not received")
		return
	}
	if err != nil {
		fail(w, 500, "store unavailable")
		return
	}
	f := s.Form(kind)
	if f == nil || !f.Complete {
		fail(w, 404, "image form not yet received")
		return
	}
	file, err := a.Store.OpenForm(r.PathValue("id"), kind)
	if err != nil {
		fail(w, 404, "image form not yet received")
		return
	}
	defer file.Close()
	var header [512]byte
	n, _ := io.ReadFull(file, header[:])
	t := http.DetectContentType(header[:n])
	if t != "image/jpeg" && t != "image/png" && t != "image/webp" {
		fail(w, 404, "form is not an image")
		return
	}
	info, err := file.Stat()
	if err != nil {
		fail(w, 500, "image unavailable")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		fail(w, 500, "image unavailable")
		return
	}
	w.Header().Set("Content-Type", t)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.CopyN(w, file, info.Size())
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	c, err := a.Store.Counts(r.Context())
	if err != nil {
		fail(w, 503, "store unavailable")
		return
	}
	cursor, err := a.Store.EventCursor(r.Context())
	if err != nil {
		fail(w, 503, "store unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "items": c.Manifested, "stream_cursor": strconv.FormatInt(cursor, 10)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"code": http.StatusText(status), "message": message})
}
