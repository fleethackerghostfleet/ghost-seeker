package api_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	apidocs "github.com/fleethackerghostfleet/ghost-seeker/docs/api"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	estore "github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/api"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	ssync "github.com/fleethackerghostfleet/ghost-seeker/shore/sync"
)

const operator = "synthetic-contract-test-operator"

type rig struct {
	shore, edge *httptest.Server
	st          *store.Store
	api         *api.API
	q           *ingest.Queue
	ctx         context.Context
	unit        string
	contract    contract
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	es, err := estore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := api.New(st)
	a.PollInterval = 10 * time.Millisecond
	a.Heartbeat = 30 * time.Millisecond
	key := strings.Repeat("synthetic-key-", 4)
	unit, err := unitauth.Mint("contract-edge", key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := unitauth.New(true, key)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc := ssync.New(st, clock.Real{}, log)
	path, h := ghostseekerv1connect.NewSyncServiceHandler(ssync.Handler{Service: svc}, append(link.HandlerOptions(), connect.WithInterceptors(auth))...)
	mux.Handle(path, h)
	mux.Handle("/api/v1/", opauth.RequireBearer(operator, a.Handler()))
	shore := httptest.NewServer(mux)
	cfg := scheduler.DefaultConfig("contract-edge")
	cfg.Tick = 20 * time.Millisecond
	cfg.ProbeInterval = 20 * time.Millisecond
	sch, err := scheduler.New(cfg, es, link.NewConnect(shore.URL, 2*time.Second, link.NewEstimator(1e9, 300, 1e9), unit), scoring.Default(), clock.Real{}, log)
	if err != nil {
		t.Fatal(err)
	}
	icfg := ingest.DefaultConfig()
	icfg.EdgeID = "contract-edge"
	g := ingest.New(icfg, es, inference.NewMock(), sch, clock.Real{}, log)
	q := ingest.NewQueue(3)
	em := http.NewServeMux()
	em.Handle("/api/v1/ingest/frames", q.Handler(operator, unit, 1<<20))
	em.Handle("GET /api/v1/health", opauth.RequireBearer(operator, http.HandlerFunc(q.Health)))
	edge := httptest.NewServer(em)
	done := make(chan struct{}, 2)
	go func() { _ = g.Run(ctx, q); done <- struct{}{} }()
	go func() { _ = sch.Run(ctx); done <- struct{}{} }()
	t.Cleanup(func() {
		cancel()
		edge.Close()
		shore.Close()
		<-done
		<-done
		es.Close()
		st.Close()
	})
	return &rig{shore: shore, edge: edge, st: st, api: a, q: q, ctx: ctx, unit: unit, contract: loadContract(t)}
}

func jpegFrame(t *testing.T, n int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(x*3 + n*7), uint8(y*4 + n*11), uint8(n * 13), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (s *rig) request(t *testing.T, method, url, token, typ string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(s.ctx, method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if typ != "" {
		req.Header.Set("Content-Type", typ)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

func (s *rig) getJSON(t *testing.T, path, schema string) []byte {
	t.Helper()
	r, b := s.request(t, "GET", s.shore.URL+path, operator, "", nil)
	if r.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", path, r.StatusCode, b)
	}
	s.contract.check(t, schema, b)
	return b
}

func (s *rig) push(t *testing.T, n int, multipartBody bool) string {
	t.Helper()
	jpg := jpegFrame(t, n)
	data, typ, path := jpg, "image/jpeg", "/api/v1/ingest/frames?source=push-camera"
	if multipartBody {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		p, _ := mw.CreateFormField("meta")
		_, _ = io.WriteString(p, `{"source":"push-camera","captured_at":"2026-11-05T01:02:03Z","sensor_type":"ir","geo":{"lat":12,"lon":34,"alt":5,"heading":90},"tags":{"subtype":"patrol boat"}}`)
		p, _ = mw.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="frame"; filename="frame.jpg"`}, "Content-Type": {"image/jpeg"}})
		_, _ = p.Write(jpg)
		_ = mw.Close()
		data, typ, path = b.Bytes(), mw.FormDataContentType(), "/api/v1/ingest/frames"
	}
	r, b := s.request(t, "POST", s.edge.URL+path, operator, typ, data)
	if r.StatusCode != 202 {
		t.Fatalf("push %d: %s", r.StatusCode, b)
	}
	s.contract.response(t, "/api/v1/ingest/frames", "post", 202, "application/json", b)
	var result ingest.Result
	_ = json.Unmarshal(b, &result)
	return result.ID
}

func until(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out waiting for edge-to-shore upload")
}

func (s *rig) waitImage(t *testing.T, id string) {
	t.Helper()
	until(t, func() bool {
		it, err := s.st.Get(s.ctx, id)
		return err == nil && it.Form(pb.FormKind_FORM_KIND_FULL) != nil && it.Form(pb.FormKind_FORM_KIND_FULL).Complete && it.Features != nil
	})
}

func (s *rig) stream(t *testing.T, last string) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, 8*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", s.shore.URL+"/api/v1/stream", nil)
	req.Header.Set("Authorization", "Bearer "+operator)
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		t.Fatal("stream unavailable", res.StatusCode)
	}
	t.Cleanup(func() { cancel(); res.Body.Close() })
	return res, bufio.NewReader(res.Body), cancel
}

func readEvent(t *testing.T, c contract, reader *bufio.Reader) (string, map[string]any) {
	t.Helper()
	var id string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("SSE read:", err)
		}
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		}
		if strings.HasPrefix(line, "data: ") {
			data := []byte(strings.TrimPrefix(line, "data: "))
			c.check(t, "StreamEvent", data)
			var event map[string]any
			_ = json.Unmarshal(data, &event)
			if event["cursor"] != id {
				t.Fatal("SSE cursor disagrees with payload")
			}
			return id, event
		}
	}
}

func TestProdContractLiveIO(t *testing.T) {
	s := newRig(t)
	res, reader, cancel := s.stream(t, "")
	id := s.push(t, 1, true)
	s.waitImage(t, id)
	cursor, event := readEvent(t, s.contract, reader)
	if event["item"].(map[string]any)["id"] != id {
		t.Fatal("wrong streamed item")
	}
	cancel()
	res.Body.Close()
	b := s.getJSON(t, "/api/v1/items?limit=1", "ItemPage")
	var page struct {
		Items []api.Item `json:"items"`
		Next  string     `json:"next_cursor"`
	}
	_ = json.Unmarshal(b, &page)
	if len(page.Items) != 1 || page.Items[0].ID != id {
		t.Fatalf("wrong list %s", b)
	}
	b = s.getJSON(t, "/api/v1/items/"+id, "Item")
	var item api.Item
	_ = json.Unmarshal(b, &item)
	if item.Source != "push-camera" || item.Geo == nil || item.Geo.Alt == nil || *item.Geo.Alt != 5 || item.SensorType != "ir" || item.CapturedAt.Format(time.RFC3339) != "2026-11-05T01:02:03Z" {
		t.Fatalf("metadata lost %s", b)
	}
	for _, form := range []string{"thumb", "full"} {
		r, b := s.request(t, "GET", s.shore.URL+"/api/v1/items/"+id+"/image?form="+form, operator, "", nil)
		s.contract.response(t, "/api/v1/items/{id}/image", "get", r.StatusCode, r.Header.Get("Content-Type"), b)
		if r.StatusCode != 200 {
			t.Fatalf("image %d: %s", r.StatusCode, b)
		}
		if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	if again := s.push(t, 1, false); again != id {
		t.Fatal("exact duplicate not folded")
	}
	res, reader, cancel = s.stream(t, cursor)
	defer cancel()
	defer res.Body.Close()
	next, _ := readEvent(t, s.contract, reader)
	a, _ := strconv.ParseInt(cursor, 10, 64)
	n, _ := strconv.ParseInt(next, 10, 64)
	if n <= a {
		t.Fatalf("resume replayed consumed cursor: %s <= %s", next, cursor)
	}
	s.getJSON(t, "/api/v1/health", "ShoreHealth")
	r, b := s.request(t, "GET", s.edge.URL+"/api/v1/health", operator, "", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	s.contract.check(t, "EdgeHealth", b)
	r, b = s.request(t, "GET", s.shore.URL+"/api/v1/openapi.yaml", operator, "", nil)
	if r.StatusCode != 200 || !bytes.Equal(b, apidocs.Spec) {
		t.Fatal("served OpenAPI differs from validated spec")
	}
}

func TestProdContractPushErrors(t *testing.T) {
	s := newRig(t)
	jpg := jpegFrame(t, 9)
	for _, tc := range []struct {
		token, typ, query string
		data              []byte
		status            int
	}{
		{"", "image/jpeg", "?source=camera", jpg, 401},
		{"wrong", "image/jpeg", "?source=camera", jpg, 401},
		{operator, "image/jpeg", "", jpg, 400},
		{operator, "image/jpeg", "?source=camera", []byte("corrupt"), 400},
		{operator, "text/plain", "?source=camera", jpg, 415},
		{operator, "image/jpeg", "?source=camera", bytes.Repeat([]byte("x"), (1<<20)+1), 413},
	} {
		r, body := s.request(t, "POST", s.edge.URL+"/api/v1/ingest/frames"+tc.query, tc.token, tc.typ, tc.data)
		if r.StatusCode != tc.status {
			t.Fatalf("got %d want %d", r.StatusCode, tc.status)
		}
		s.contract.response(t, "/api/v1/ingest/frames", "post", r.StatusCode, r.Header.Get("Content-Type"), body)
	}
}

func TestProdContractReplacementImageAvailability(t *testing.T) {
	s := newRig(t)
	id := s.push(t, 18, false)
	s.waitImage(t, id)
	cursor, err := s.st.EventCursor(s.ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream, reader, cancel := s.stream(t, strconv.FormatInt(cursor, 10))
	defer cancel()
	defer stream.Body.Close()
	data := jpegFrame(t, 19)
	hash := sha256.Sum256(data)
	chunk := &pb.Chunk{ItemId: id, Form: pb.FormKind_FORM_KIND_FULL, TotalBytes: int64(len(data)), ContentHash: hex.EncodeToString(hash[:]), Mime: "image/jpeg", Data: data[:len(data)/2]}
	if _, done, err := s.st.WriteChunk(s.ctx, "contract-edge", chunk, time.Now()); err != nil || done {
		t.Fatal("partial replacement", done, err)
	}
	r, body := s.request(t, "GET", s.shore.URL+"/api/v1/items/"+id+"/image?form=full", operator, "", nil)
	if r.StatusCode != 404 {
		t.Fatal("incomplete replacement served", r.StatusCode)
	}
	s.contract.response(t, "/api/v1/items/{id}/image", "get", r.StatusCode, r.Header.Get("Content-Type"), body)
	_, event := readEvent(t, s.contract, reader)
	for _, f := range event["item"].(map[string]any)["forms"].([]any) {
		form := f.(map[string]any)
		if form["name"] == "full" && form["available"] != false {
			t.Fatal("reset availability not streamed")
		}
	}
	chunk.Offset = int64(len(data) / 2)
	chunk.Data = data[len(data)/2:]
	if _, done, err := s.st.WriteChunk(s.ctx, "contract-edge", chunk, time.Now()); err != nil || !done {
		t.Fatal("completed replacement", done, err)
	}
	r, body = s.request(t, "GET", s.shore.URL+"/api/v1/items/"+id+"/image?form=full", operator, "", nil)
	if r.StatusCode != 200 || !bytes.Equal(body, data) {
		t.Fatal("replacement image missing/corrupt", r.StatusCode)
	}
	s.contract.response(t, "/api/v1/items/{id}/image", "get", r.StatusCode, r.Header.Get("Content-Type"), body)
}

func TestProdContractSpoolPaginationAuthAndUpdates(t *testing.T) {
	s := newRig(t)
	first := s.push(t, 2, false)
	s.waitImage(t, first)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ingest.Watch(ctx, s.q, dir, ingest.Metadata{Source: "spool-camera"}, 1<<20, 10*time.Millisecond)
	}()
	t.Cleanup(func() { cancel(); <-done })
	_ = os.WriteFile(filepath.Join(dir, "frame.jpg.json"), []byte(`{"source":"spool-camera","captured_at":"2020-01-01T00:00:00Z","sensor_type":"eo"}`), 0600)
	_ = os.WriteFile(filepath.Join(dir, "frame.jpg"), jpegFrame(t, 3), 0600)
	until(t, func() bool {
		files, _ := filepath.Glob(filepath.Join(dir, "processed", "*.jpg"))
		return len(files) == 1
	})
	until(t, func() bool { rs, _ := s.st.Receipts(s.ctx, 0, "", 10); return len(rs) == 2 })
	page := s.getJSON(t, "/api/v1/items?limit=1", "ItemPage")
	var p struct {
		Items []api.Item `json:"items"`
		Next  string     `json:"next_cursor"`
	}
	_ = json.Unmarshal(page, &p)
	if len(p.Items) != 1 || p.Items[0].ID != first {
		t.Fatal(string(page))
	}
	page = s.getJSON(t, "/api/v1/items?since="+p.Next+"&limit=1", "ItemPage")
	_ = json.Unmarshal(page, &p)
	if len(p.Items) != 1 || p.Items[0].Source != "spool-camera" {
		t.Fatal("capture time disturbed receipt pagination", string(page))
	}
	s.waitImage(t, p.Items[0].ID)
	for _, query := range []string{"source=spool-camera", "tier=P1", "tier=P4"} {
		s.getJSON(t, "/api/v1/items?"+query, "ItemPage")
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/items/" + first, "/api/v1/items/" + first + "/image", "/api/v1/stream", "/api/v1/health", "/api/v1/openapi.yaml"} {
		for _, token := range []string{"", "wrong"} {
			r, b := s.request(t, "GET", s.shore.URL+path, token, "", nil)
			if r.StatusCode != 401 {
				t.Fatal("unauthenticated route", path, r.StatusCode)
			}
			s.contract.check(t, "Error", b)
		}
	}
	for _, path := range []string{"/api/v1/items?since=-1", "/api/v1/items?since=NaN", "/api/v1/items?limit=501", "/api/v1/items?tier=P0", "/api/v1/stream?since=999999999", "/api/v1/items/" + first + "/image?form=fact"} {
		r, b := s.request(t, "GET", s.shore.URL+path, operator, "", nil)
		if r.StatusCode != 400 {
			t.Fatal(path, r.StatusCode, string(b))
		}
		s.contract.check(t, "Error", b)
	}
	for _, path := range []string{"/api/v1/items/missing", "/api/v1/items/" + first + "/image?form=crop"} {
		r, b := s.request(t, "GET", s.shore.URL+path, operator, "", nil)
		if r.StatusCode != 404 {
			t.Fatal(path, r.StatusCode, string(b))
		}
		s.contract.check(t, "Error", b)
	}
	max, err := s.st.EventCursor(s.ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, reader, cancelStream := s.stream(t, strconv.FormatInt(max, 10))
	defer cancelStream()
	defer res.Body.Close()
	if err := s.st.SetOverride(s.ctx, first, "patrol boat", "contract-operator"); err != nil {
		t.Fatal(err)
	}
	_, event := readEvent(t, s.contract, reader)
	if len(event["item"].(map[string]any)["decisions"].([]any)) != 1 || event["event"] != "updated" {
		t.Fatal("override not streamed")
	}
	if err := s.st.SetPolicy(s.ctx, &pb.Policy{Id: "contract-policy", IssuedAtUnixMs: time.Now().UnixMilli(), Interest: &pb.Interest{TierLabels: map[string]int32{"patrol boat": 2}}}); err != nil {
		t.Fatal(err)
	}
	b := s.getJSON(t, "/api/v1/items/"+first, "Item")
	var item api.Item
	_ = json.Unmarshal(b, &item)
	if item.Tier != "P2" || len(item.DirectiveMatches) != 1 {
		t.Fatal("policy pin missing", string(b))
	}
}

func TestProdContractStreamHeartbeatAndClientBound(t *testing.T) {
	s := newRig(t)
	res, reader, cancel := s.stream(t, "")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "heartbeat") {
			break
		}
	}
	cancel()
	res.Body.Close()
	// Use real HTTP streams to exercise the shared concurrent-client bound.
	var streams []*http.Response
	var cancels []context.CancelFunc
	for range 64 {
		r, _, c := s.stream(t, "")
		streams = append(streams, r)
		cancels = append(cancels, c)
	}
	r, b := s.request(t, "GET", s.shore.URL+"/api/v1/stream", operator, "", nil)
	if r.StatusCode != 429 {
		t.Fatal("client bound", r.StatusCode, string(b))
	}
	s.contract.check(t, "Error", b)
	for i, r := range streams {
		cancels[i]()
		r.Body.Close()
	}
}
