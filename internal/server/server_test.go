package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/edgesim"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func newTestServer(t *testing.T, token string) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(st, fstest.MapFS{"index.html": {Data: []byte("ok")}}, token).Handler())
	t.Cleanup(ts.Close)
	return ts, st, dir
}

func keptFrame(t *testing.T, src string) edgesim.Frame {
	c, _ := catalog.SourceByID(src)
	s := edgesim.NewSensor(c)
	for i := 0; i < 500; i++ {
		if fr := s.Capture(time.Now()); fr.Kept {
			return fr
		}
	}
	t.Fatal("sensor never kept a frame")
	return edgesim.Frame{}
}

func doJSON(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestIngestReviewRescoreAndPersist(t *testing.T) {
	ts, st, dir := newTestServer(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	evRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer evRes.Body.Close()
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(evRes.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<22)
		for sc.Scan() {
			if ev, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				events <- ev
			}
		}
	}()
	waitEvent := func(want string) {
		t.Helper()
		for {
			select {
			case ev := <-events:
				if ev == want {
					return
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %q event", want)
			}
		}
	}
	waitEvent("hello")

	up := &edgesim.Uploader{BaseURL: ts.URL, Client: http.DefaultClient}
	if err := up.Upload(ctx, keptFrame(t, "EO-P7")); err != nil {
		t.Fatal(err)
	}
	waitEvent("image")

	var list []store.Item
	doJSON(t, "GET", ts.URL+"/api/images", nil, &list)
	if len(list) != 1 || list[0].Tier < 1 || list[0].Tier > 4 {
		t.Fatalf("unexpected list: %+v", list)
	}
	id := list[0].ID

	img, err := http.Get(ts.URL + "/api/images/" + id + "/image")
	if err != nil || img.StatusCode != 200 || img.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("blob: %v %v", err, img)
	}
	img.Body.Close()

	var it store.Item
	if code := doJSON(t, "POST", ts.URL+"/api/images/"+id+"/review", store.Review{Manual: 1, Reviewed: true, Sent: true, Action: "cdr"}, &it); code != 200 || it.Tier != 1 || !it.Sent {
		t.Fatalf("review: %d %+v", code, it)
	}
	waitEvent("update")

	var res map[string]any
	if code := doJSON(t, "POST", ts.URL+"/api/directives", map[string]string{"text": "blah"}, &res); code != 422 {
		t.Errorf("unparseable directive should be 422, got %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/api/directives", map[string]string{"text": "ignore warships"}, &res); code != 201 {
		t.Fatalf("directive: %d %v", code, res)
	}
	waitEvent("rescore")
	if code := doJSON(t, "PUT", ts.URL+"/api/model", map[string]string{"model": "nope"}, nil); code != 400 {
		t.Errorf("bad model should be 400, got %d", code)
	}

	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st2.Get(id)
	if err != nil || !got.Sent || got.Manual != 1 || len(st2.Config().Directives) != 1 {
		t.Fatalf("not persisted: %v %+v", err, got)
	}
}

func TestIngestValidationAndToken(t *testing.T) {
	ts, _, _ := newTestServer(t, "s3cret")
	fr := keptFrame(t, "IR-HE")
	if err := (&edgesim.Uploader{BaseURL: ts.URL, Client: http.DefaultClient}).Upload(context.Background(), fr); err == nil {
		t.Error("upload without token should fail")
	}
	if err := (&edgesim.Uploader{BaseURL: ts.URL, Token: "s3cret", Client: http.DefaultClient}).Upload(context.Background(), fr); err != nil {
		t.Errorf("upload with token: %v", err)
	}
	fr.Report.Source = "BOGUS"
	if err := (&edgesim.Uploader{BaseURL: ts.URL, Token: "s3cret", Client: http.DefaultClient}).Upload(context.Background(), fr); err == nil {
		t.Error("unknown source should be rejected")
	}
}
