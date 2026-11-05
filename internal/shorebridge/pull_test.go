package shorebridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestFullSizePullNeedsOperatorAndForwardsOriginal(t *testing.T) {
	var got *pb.RequestPullRequest
	shore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/RequestPull") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		got = &pb.RequestPullRequest{}
		if err := protojson.Unmarshal(raw, got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{}")
	}))
	defer shore.Close()
	h := New(shore.URL, "", nil).Handler()
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/shore/pull", strings.NewReader(body)))
		return rec
	}

	if rec := post(`{"itemId":"i1","form":"full","original":true,"by":"  "}`); rec.Code != http.StatusBadRequest || got != nil {
		t.Fatalf("blank operator: code=%d forwarded=%v", rec.Code, got)
	}
	if rec := post(`{"itemId":"i1","form":"full","original":true,"by":"op1"}`); rec.Code != http.StatusOK {
		t.Fatalf("override: code=%d %s", rec.Code, rec.Body.String())
	}
	if p := got.GetPull(); !p.GetOriginal() || p.GetRequestedBy() != "op1" || p.GetForm() != pb.FormKind_FORM_KIND_FULL || p.GetItemId() != "i1" {
		t.Fatalf("forwarded pull = %v", p)
	}
	got = nil
	if rec := post(`{"itemId":"i2","form":"full"}`); rec.Code != http.StatusOK || got.GetPull().GetOriginal() || got.GetPull().GetRequestedBy() != "operator-ui" {
		t.Fatalf("plain pull: code=%d pull=%v", rec.Code, got.GetPull())
	}
}
