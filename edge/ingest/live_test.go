package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/gif"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const liveTestToken = "synthetic-live-test-operator"

func runningQueue(t *testing.T, capacity int) *Queue {
	t.Helper()
	g, _ := newFormsIngester(t, DefaultConfig())
	q := NewQueue(capacity)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = g.Run(ctx, q) }()
	t.Cleanup(func() { cancel(); <-done })
	return q
}

func requestFrame(h http.Handler, data []byte, contentType, query, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/ingest/frames"+query, bytes.NewReader(data))
	r.Header.Set("Content-Type", contentType)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func multipartFrame(t *testing.T, data []byte, meta string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, err := w.CreateFormField("meta")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(p, meta)
	p, err = w.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="frame"; filename="frame.jpg"`}, "Content-Type": {"image/jpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), w.FormDataContentType()
}

func TestLivePushValidationAndDuplicate(t *testing.T) {
	q := runningQueue(t, 2)
	h := q.Handler(liveTestToken, "synthetic-unit-token", 1<<20)
	jpg := encJPEG(t, scene(40, 30))
	var gifBytes bytes.Buffer
	_ = gif.Encode(&gifBytes, image.NewGray(image.Rect(0, 0, 10, 10)), nil)
	cases := []struct {
		name, typ, query, token string
		data                    []byte
		status                  int
	}{
		{"no auth", "image/jpeg", "?source=cam-a", "", jpg, 401},
		{"wrong auth", "image/jpeg", "?source=cam-a", "invalid", jpg, 401},
		{"source required", "image/jpeg", "", liveTestToken, jpg, 400},
		{"source unsafe", "image/jpeg", "?source=%3Cscript%3E", liveTestToken, jpg, 400},
		{"bad timestamp", "image/jpeg", "?source=cam-a&captured_at=bad", liveTestToken, jpg, 400},
		{"non image", "text/plain", "?source=cam-a", liveTestToken, jpg, 415},
		{"corrupt", "image/jpeg", "?source=cam-a", liveTestToken, []byte("bad"), 400},
		{"mismatch", "image/png", "?source=cam-a", liveTestToken, jpg, 400},
		{"gif", "image/jpeg", "?source=cam-a", liveTestToken, gifBytes.Bytes(), 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := requestFrame(h, tc.data, tc.typ, tc.query, tc.token)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	data, typ := multipartFrame(t, jpg, `{"source":"cam-a","captured_at":"2026-11-05T12:13:14Z","sensor_type":"ir","geo":{"lat":12,"lon":34,"alt":7,"heading":90},"tags":{"sensor":"forward"}}`)
	w := requestFrame(h, data, typ, "", liveTestToken)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var first, second Result
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	w = requestFrame(h, jpg, "image/jpeg", "?source=cam-a", "synthetic-unit-token")
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if w.Code != 202 || first.ID == "" || first.Duplicate || first.ID != second.ID || !second.Duplicate {
		t.Fatalf("duplicate result: %+v %+v: %d", first, second, w.Code)
	}
	w = requestFrame(h, encPNG(t, scene(11, 12)), "image/png", "?source=cam-b", liveTestToken)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	q.Health(w, httptest.NewRequest("GET", "/api/v1/health", nil))
	var health struct {
		Sources map[string]SourceStats `json:"sources"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &health)
	s := health.Sources["push:cam-a"]
	if s.Accepted != 2 || s.Processed != 2 || s.Duplicates != 1 || s.LastFrame == nil {
		t.Fatal(w.Body.String())
	}
}

func TestLivePushSizeMetadataAndBackpressure(t *testing.T) {
	q := NewQueue(1)
	jpg := encJPEG(t, scene(40, 30))
	h := q.Handler(liveTestToken, "", 32)
	if w := requestFrame(h, jpg, "image/jpeg", "?source=cam", liveTestToken); w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, typ := multipartFrame(t, jpg, `{"source":"cam"}`)
	if w := requestFrame(h, data, typ, "", liveTestToken); w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, meta := range []string{`{`, `{"source":"cam","geo":{"lat":91,"lon":0}}`, `{"source":"cam","geo":{"lat":0}}`, `{"source":"cam","geo":{"lat":0,"lon":0,"heading":360}}`, `{"source":"cam","sensor_type":"bad type"}`, `{"source":"cam","tags":{"bad key":"x"}}`, strings.Repeat(" ", 65537)} {
		data, typ = multipartFrame(t, jpg, meta)
		if w := requestFrame(q.Handler(liveTestToken, "", 1<<20), data, typ, "", liveTestToken); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(q.slots) != 0 {
		t.Fatal("invalid requests leaked admission slots")
	}
	q.reserve()
	w := requestFrame(q.Handler(liveTestToken, "", 1<<20), jpg, "image/jpeg", "?source=cam", liveTestToken)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	<-q.slots
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := (Metadata{Source: "cam"}).Capture(jpg)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = q.Push(ctx, c, "test")
	// Cancellation after acceptance may leave an admitted capture for the
	// writer; cancellation before acceptance must not add any other slot.
	if len(q.slots) > 1 {
		t.Fatal("unbounded admission")
	}
}

func TestSpoolProcessedFailedAndStable(t *testing.T) {
	q := runningQueue(t, 1)
	dir := t.TempDir()
	for _, sub := range []string{"processed", "failed"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "good.jpg")
	jpg := encJPEG(t, scene(40, 30))
	_ = os.WriteFile(path, jpg, 0600)
	_ = os.WriteFile(path+".json", []byte(`{"source":"sidecar-camera","sensor_type":"eo"}`), 0600)
	if err := spoolFile(context.Background(), q, path, Metadata{Source: "default"}, 1<<20, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "processed", "*.jpg"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	if _, err := os.Stat(files[0] + ".json"); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.png")
	_ = os.WriteFile(bad, []byte("not image"), 0600)
	if err := spoolFile(context.Background(), q, bad, Metadata{Source: "default"}, 1<<20, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "failed", "*.png"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	partial := filepath.Join(dir, "partial.jpg")
	_ = os.WriteFile(partial, jpg[:len(jpg)/2], 0600)
	done := make(chan error, 1)
	go func() {
		done <- spoolFile(context.Background(), q, partial, Metadata{Source: "cam"}, 1<<20, 80*time.Millisecond)
	}()
	time.Sleep(20 * time.Millisecond)
	_ = os.WriteFile(partial, jpg, 0600)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatal("unstable frame consumed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Watch(ctx, q, dir, Metadata{Source: "cam"}, 1<<20, time.Millisecond); err == nil {
		t.Fatal("watch ignored cancellation")
	}
}

func TestMJPEGReconnectFrameCapAndRate(t *testing.T) {
	q := runningQueue(t, 2)
	jpg := encJPEG(t, scene(40, 30))
	var connections atomic.Int32
	cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", `multipart/x-mixed-replace; boundary=`+mw.Boundary())
		for range 2 {
			p, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/jpeg"}})
			_, _ = p.Write(jpg)
		}
		_ = mw.Close()
	}))
	defer cam.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	err := PullMJPEG(ctx, q, cam.URL, Metadata{Source: "camera"}, 1<<20, 10)
	if err == nil || connections.Load() < 2 {
		t.Fatalf("reconnect: %v %d", err, connections.Load())
	}
	q.mu.Lock()
	s := q.stats["mjpeg:camera"]
	q.mu.Unlock()
	if s.Processed < 3 || s.Processed > 7 {
		t.Fatalf("frame-rate cap not respected: %+v", s)
	}
	if err := mjpegConnection(context.Background(), cam.Client(), q, cam.URL, Metadata{Source: "camera"}, 32, time.Millisecond); err == nil {
		t.Fatal("oversize MJPEG frame accepted")
	}
	if err := PullMJPEG(context.Background(), q, "file:///tmp/cam", Metadata{Source: "camera"}, 32, 1); err == nil {
		t.Fatal("file URL accepted")
	}
}
