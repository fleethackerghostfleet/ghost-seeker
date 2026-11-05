package shorebridge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// statefulShore keeps the last SetPolicy like shore/console does (layers kept,
// Interest/EMCON flattened at write time) so successive directives build on
// each other the way they do against the real shore.
type statefulShore struct {
	mu     sync.Mutex
	policy *pb.Policy
	items  []*pb.ItemView
	server *httptest.Server
}

func newStatefulShore(t *testing.T, initial *pb.Policy) *statefulShore {
	t.Helper()
	s := &statefulShore{policy: initial}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case consolePath + "GetPolicy":
			s.mu.Lock()
			current := s.policy
			s.mu.Unlock()
			writeProtoResponse(t, w, &pb.GetPolicyResponse{Policy: current})
		case consolePath + "SetPolicy":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var request pb.SetPolicyRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Errorf("decode SetPolicy request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stored := proto.Clone(request.Policy).(*pb.Policy)
			stored.Id = "p-" + time.Now().Format("150405.000")
			if len(stored.Layers) > 0 {
				stored = policy.Flatten(stored, "", time.Now())
			}
			s.mu.Lock()
			s.policy = stored
			s.mu.Unlock()
			writeProtoResponse(t, w, &pb.SetPolicyResponse{Policy: stored, Conflicts: stored.Conflicts})
		case consolePath + "GetStatus":
			writeProtoResponse(t, w, &pb.GetStatusResponse{NodeId: "SHORE-1"})
		case consolePath + "ListItems":
			s.mu.Lock()
			items := s.items
			s.mu.Unlock()
			writeProtoResponse(t, w, &pb.ListItemsResponse{Items: items})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *statefulShore) stored() *pb.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.Clone(s.policy).(*pb.Policy)
}

func postDirective(t *testing.T, handler http.Handler, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/directives", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/directives %s: status=%d body=%s", body, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode directive response: %v body=%s", err, rec.Body.String())
	}
	return out
}

func getJSON(t *testing.T, handler http.Handler, path string, dst any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %s: %v body=%s", path, err, rec.Body.String())
	}
}

func effective(p *pb.Policy) (suppress, boost []string, emcon pb.EmconMode) {
	return p.GetInterest().GetSuppressLabels(), p.GetInterest().GetBoostLabels(), p.GetEmcon()
}

func sameLabels(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.ToLower(got[i]) != want[i] {
			return false
		}
	}
	return true
}

func TestTwoTimedDirectivesKeepTheirOwnExpiry(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	console := NewConsole(bridge)
	handler := console.Handler()

	postDirective(t, handler, `{"text":"ignore cargo ships for 30 minutes"}`)
	postDirective(t, handler, `{"text":"go quiet for 1 minute"}`)

	stored := shore.stored()
	if stored.ExpiresAtUnixMs != 0 {
		t.Fatalf("whole-policy expiry=%d, want 0 (edge must not fall back to default)", stored.ExpiresAtUnixMs)
	}
	if len(stored.Layers) != 1 || len(stored.Layers[0].Rules) != 2 {
		t.Fatalf("layers=%+v, want one operator layer with one rule per directive", stored.Layers)
	}
	cargo, quiet := stored.Layers[0].Rules[0], stored.Layers[0].Rules[1]
	now := time.Now()
	if cargo.ExpiresAtUnixMs < now.Add(29*time.Minute).UnixMilli() || cargo.ExpiresAtUnixMs > now.Add(31*time.Minute).UnixMilli() {
		t.Fatalf("cargo rule expiry=%d, want about 30 minutes (was re-timed by the EMCON directive?)", cargo.ExpiresAtUnixMs)
	}
	if emcon := stored.Layers[0].Emcon; emcon == nil || emcon.Mode != pb.EmconMode_EMCON_MODE_SILENT ||
		emcon.ExpiresAtUnixMs < now.Add(50*time.Second).UnixMilli() || emcon.ExpiresAtUnixMs > now.Add(70*time.Second).UnixMilli() ||
		quiet.ExpiresAtUnixMs != emcon.ExpiresAtUnixMs {
		t.Fatalf("EMCON=%+v quiet rule=%+v, want SILENT for about 1 minute", emcon, quiet)
	}

	// Before the shorter expiry: both apply. After it: EMCON NORMAL, cargo still suppressed.
	before := policy.Flatten(stored, "", now.Add(30*time.Second))
	if s, _, e := effective(before); !sameLabels(s, "cargo ship") || e != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("at +30s suppress=%v emcon=%v, want [cargo ship] SILENT", s, e)
	}
	after := policy.Flatten(stored, "", now.Add(2*time.Minute))
	if s, _, e := effective(after); !sameLabels(s, "cargo ship") || e != pb.EmconMode_EMCON_MODE_NORMAL {
		t.Fatalf("at +2m suppress=%v emcon=%v, want [cargo ship] NORMAL", s, e)
	}
	later := policy.Flatten(stored, "", now.Add(31*time.Minute))
	if s, _, _ := effective(later); len(s) != 0 {
		t.Fatalf("at +31m suppress=%v, want none", s)
	}

	// The shore view agrees: one listed directive per rule, each with its deadline.
	var directives []command.Directive
	getJSON(t, handler, "/api/directives", &directives)
	if len(directives) != 2 || directives[0].ExpiresAtUnixMs != cargo.ExpiresAtUnixMs || directives[1].ExpiresAtUnixMs != quiet.ExpiresAtUnixMs ||
		directives[0].Text != "ignore cargo ships for 30 minutes" || directives[1].Text != "go quiet for 1 minute" ||
		len(directives[0].Rules) != 1 || directives[0].Rules[0].DirectiveID != cargo.Id {
		t.Fatalf("directives=%+v", directives)
	}
	var view struct {
		Policy LivePolicy `json:"policy"`
	}
	getJSON(t, handler, "/api/policy", &view)
	until := time.UnixMilli(cargo.ExpiresAtUnixMs).UTC().Format("15:04Z")
	if !strings.Contains(view.Policy.Readback, "Deprioritize (not delete): cargo ship. Until "+until+".") || view.Policy.EMCON.Mode != "SILENT" ||
		view.Policy.ExpiresAtUnixMs != quiet.ExpiresAtUnixMs {
		t.Fatalf("policy view=%+v, want per-rule readback until %s and SILENT", view.Policy, until)
	}

	// Rendering the same stored policy after the EMCON lapsed drops it from the view.
	console.mu.Lock()
	lapsed := console.policyView(stored, now.Add(2*time.Minute))
	console.mu.Unlock()
	if lapsed.EMCON.Mode != "NORMAL" || !sameLabels(lapsed.Suppress, "cargo ship") || lapsed.ExpiresAtUnixMs != cargo.ExpiresAtUnixMs ||
		!strings.Contains(lapsed.Readback, "Until "+until+".") || strings.Contains(lapsed.Text, "go quiet") {
		t.Fatalf("view after EMCON lapse=%+v", lapsed)
	}
}

func TestOpenEndedDirectiveSurvivesTimedOne(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	handler := NewConsole(bridge).Handler()

	postDirective(t, handler, `{"text":"prioritize submarines"}`)
	postDirective(t, handler, `{"text":"ignore cargo ships for 5 minutes"}`)

	stored := shore.stored()
	if len(stored.Layers) != 1 || len(stored.Layers[0].Rules) != 2 || stored.Layers[0].Rules[0].ExpiresAtUnixMs != 0 {
		t.Fatalf("layers=%+v, want an open-ended submarine rule and a timed cargo rule", stored.Layers)
	}
	now := time.Now()
	if s, b, _ := effective(policy.Flatten(stored, "", now)); !sameLabels(s, "cargo ship") || !sameLabels(b, "submarine") {
		t.Fatalf("now suppress=%v boost=%v", s, b)
	}
	if s, b, _ := effective(policy.Flatten(stored, "", now.Add(6*time.Minute))); len(s) != 0 || !sameLabels(b, "submarine") {
		t.Fatalf("+6m suppress=%v boost=%v, want submarine boost to outlive the timed rule", s, b)
	}
	if s, b, _ := effective(policy.Flatten(stored, "", now.Add(48*time.Hour))); len(s) != 0 || !sameLabels(b, "submarine") {
		t.Fatalf("+48h boost=%v, want open-ended directive never to expire", b)
	}
	var directives []command.Directive
	getJSON(t, handler, "/api/directives", &directives)
	if len(directives) != 2 || directives[0].ExpiresAtUnixMs != 0 || directives[1].ExpiresAtUnixMs == 0 {
		t.Fatalf("directives=%+v", directives)
	}
}

func TestEmconOnlyTimedDirectiveExpiresAlone(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	console := NewConsole(bridge)
	handler := console.Handler()

	postDirective(t, handler, `{"text":"ignore birds"}`)
	out := postDirective(t, handler, `{"text":"go quiet for 2 minutes"}`)
	if rb, _ := out["readback"].(string); !strings.Contains(rb, "EMCON SILENT for 2 min.") {
		t.Fatalf("readback=%v", out["readback"])
	}
	stored := shore.stored()
	now := time.Now()
	if stored.ExpiresAtUnixMs != 0 || stored.Emcon != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("stored=%+v, want SILENT with no whole-policy expiry", stored)
	}
	if _, _, e := effective(policy.Flatten(stored, "", now.Add(3*time.Minute))); e != pb.EmconMode_EMCON_MODE_NORMAL {
		t.Fatalf("+3m emcon=%v, want NORMAL", e)
	}
	if s, _, _ := effective(policy.Flatten(stored, "", now.Add(3*time.Minute))); !sameLabels(s, "bird") {
		t.Fatalf("+3m suppress=%v, want the open-ended bird rule intact", s)
	}
	console.mu.Lock()
	view := console.policyView(stored, now.Add(3*time.Minute))
	console.mu.Unlock()
	if view.EMCON.Mode != "NORMAL" || view.ExpiresAtUnixMs != 0 || view.Text != "ignore birds" {
		t.Fatalf("view after EMCON lapse=%+v", view)
	}

	// Removing the EMCON directive by id lifts SILENT and keeps the other rule.
	id, _ := out["id"].(string)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/directives/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored = shore.stored()
	if s, _, e := effective(policy.Flatten(stored, "", now)); e != pb.EmconMode_EMCON_MODE_NORMAL || !sameLabels(s, "bird") {
		t.Fatalf("after delete suppress=%v emcon=%v", s, e)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/directives/"+id, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE status=%d, want 404", rec.Code)
	}
}

func TestSameOperatorNewestDirectiveWinsALabel(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	handler := NewConsole(bridge).Handler()

	postDirective(t, handler, `{"text":"ignore cargo ships and birds for 30 minutes"}`)
	postDirective(t, handler, `{"text":"prioritize cargo ships"}`)

	stored := shore.stored()
	if len(stored.Layers) != 1 || len(stored.Layers[0].Rules) != 2 {
		t.Fatalf("layers=%+v", stored.Layers)
	}
	older, newer := stored.Layers[0].Rules[0], stored.Layers[0].Rules[1]
	if !sameLabels(older.SuppressLabels, "bird") || !sameLabels(newer.BoostLabels, "cargo ship") {
		t.Fatalf("older=%+v newer=%+v, want cargo ship moved from the older suppress to the newer boost", older, newer)
	}
	if s, b, _ := effective(policy.Flatten(stored, "", time.Now())); !sameLabels(s, "bird") || !sameLabels(b, "cargo ship") {
		t.Fatalf("suppress=%v boost=%v", s, b)
	}

	// Naming the only label of an older rule drops that rule entirely.
	postDirective(t, handler, `{"text":"prioritize birds"}`)
	stored = shore.stored()
	if len(stored.Layers[0].Rules) != 2 || len(stored.Layers[0].Rules[0].SuppressLabels) != 0 && stored.Layers[0].Rules[0].Id == older.Id {
		t.Fatalf("rules=%+v, want the emptied older rule dropped", stored.Layers[0].Rules)
	}
	if s, b, _ := effective(policy.Flatten(stored, "", time.Now())); len(s) != 0 || !sameLabels(b, "cargo ship", "bird") {
		t.Fatalf("suppress=%v boost=%v", s, b)
	}
}

func TestDirectivesSecondsApartBothSurvive(t *testing.T) {
	// The console's cached policy is only refreshed by the poller (never run
	// here), so each directive must be layered onto what the shore holds.
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	handler := NewConsole(bridge).Handler()
	postDirective(t, handler, `{"text":"ignore cargo ships","rules":[{"type":"suppress","classes":["cargo ship"]}]}`)
	postDirective(t, handler, `{"text":"prioritize submarines","rules":[{"type":"boost","classes":["submarine"]}]}`)
	if s, b, _ := effective(shore.stored()); !sameLabels(s, "cargo ship") || !sameLabels(b, "submarine") {
		t.Fatalf("suppress=%v boost=%v, want both directives", s, b)
	}
	var view struct {
		Policy LivePolicy `json:"policy"`
	}
	getJSON(t, handler, "/api/policy", &view)
	if !sameLabels(view.Policy.Suppress, "cargo ship") || !sameLabels(view.Policy.Boost, "submarine") || view.Policy.Text != "ignore cargo ships; prioritize submarines" {
		t.Fatalf("cached view not updated from the applied policy: %+v", view.Policy)
	}
}

func TestShoreViewHidesExpiredRulesAndWholePolicyExpiry(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Minute).UnixMilli(), now.Add(time.Hour).UnixMilli()
	layered := &pb.Policy{Id: "p-1", Layers: []*pb.PolicyLayer{{
		Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: DefaultOperator,
		Rules: []*pb.InterestRule{
			{Id: "r-cargo", Text: "ignore cargo ships for 30 minutes", SuppressLabels: []string{"cargo ship"}, ExpiresAtUnixMs: past},
			{Id: "r-sub", Text: "prioritize submarines", BoostLabels: []string{"submarine"}, ExpiresAtUnixMs: future},
		},
		Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: DefaultOperator, Echelon: pb.Echelon_ECHELON_SHIP, ExpiresAtUnixMs: past},
	}}}
	console := NewConsole(New("http://127.0.0.1:0", "", nil))
	console.mu.Lock()
	view := console.policyView(layered, now)
	directives := console.directivesLocked()
	console.mu.Unlock()
	if len(view.Suppress) != 0 || !sameLabels(view.Boost, "submarine") || view.EMCON.Mode != "NORMAL" || view.ExpiresAtUnixMs != future ||
		view.Text != "prioritize submarines" || strings.Contains(view.Readback, "cargo") {
		t.Fatalf("view=%+v, want the expired cargo rule and EMCON hidden", view)
	}
	if len(directives) != 1 || directives[0].ID != "r-sub" || directives[0].ExpiresAtUnixMs != future {
		t.Fatalf("directives=%+v", directives)
	}

	// A legacy flat policy whose whole-policy deadline passed is cleared (the edge is on its default).
	flat := &pb.Policy{Id: "p-2", Text: "ignore birds", Emcon: pb.EmconMode_EMCON_MODE_SILENT,
		Interest: &pb.Interest{SuppressLabels: []string{"bird"}}, ExpiresAtUnixMs: past}
	console.mu.Lock()
	view = console.policyView(flat, now)
	console.mu.Unlock()
	if len(view.Suppress) != 0 || view.EMCON.Mode != "NORMAL" || view.ID != "default" || view.ExpiresAtUnixMs != 0 {
		t.Fatalf("expired flat view=%+v, want cleared", view)
	}
	if next := nextPolicyExpiry(layered, now); next.UnixMilli() != future {
		t.Fatalf("next expiry=%v, want the submarine rule's deadline", next)
	}
}

func TestNewerEmconDirectiveSupersedesOlderOne(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	bridge := New(shore.server.URL, "", nil)
	bridge.Compiler = &command.Compiler{}
	handler := NewConsole(bridge).Handler()

	older := postDirective(t, handler, `{"text":"go quiet for 30 minutes"}`)
	newer := postDirective(t, handler, `{"text":"burst mode for 10 minutes"}`)
	stored := shore.stored()
	if len(stored.Layers[0].Rules) != 1 || stored.Layers[0].Rules[0].Id != newer["id"] {
		t.Fatalf("rules=%+v, want only the newer EMCON directive", stored.Layers[0].Rules)
	}
	var directives []command.Directive
	getJSON(t, handler, "/api/directives", &directives)
	if len(directives) != 1 || directives[0].ID != newer["id"] {
		t.Fatalf("directives=%+v", directives)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/directives/"+older["id"].(string), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleting the superseded directive: status=%d, want 404", rec.Code)
	}
	if _, _, e := effective(policy.Flatten(shore.stored(), "", time.Now())); e != pb.EmconMode_EMCON_MODE_BURST {
		t.Fatalf("emcon=%v, want BURST kept", e)
	}

	// The EMCON control takes over: the directive no longer owns the setting,
	// so it leaves the list and removing it cannot cancel the chosen mode.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/policy/emcon", strings.NewReader(`{"mode":"SILENT"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("EMCON control status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored = shore.stored()
	if len(stored.Layers[0].Rules) != 0 || stored.Layers[0].Emcon.Mode != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("after EMCON control rules=%+v emcon=%+v", stored.Layers[0].Rules, stored.Layers[0].Emcon)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/directives", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear status=%d", rec.Code)
	}
	if _, _, e := effective(policy.Flatten(shore.stored(), "", time.Now())); e != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("emcon=%v after clearing directives, want the control's SILENT kept", e)
	}
}

func TestDirectiveOnTimedFlatEmconKeepsItsDeadline(t *testing.T) {
	// A flat policy with Emcon but no EmconSetting (older writers) and a
	// whole-policy deadline: the deadline moves onto the layer EMCON.
	now := time.Now()
	deadline := now.Add(10 * time.Minute).UnixMilli()
	shore := newStatefulShore(t, &pb.Policy{Id: "flat", IssuedBy: "someone", Interest: &pb.Interest{},
		Emcon: pb.EmconMode_EMCON_MODE_SILENT, ExpiresAtUnixMs: deadline})
	handler := NewConsole(New(shore.server.URL, "", nil)).Handler()
	postDirective(t, handler, `{"text":"ignore birds","rules":[{"type":"suppress","classes":["bird"]}]}`)
	stored := shore.stored()
	if stored.ExpiresAtUnixMs != 0 || len(stored.Layers) != 1 || stored.Layers[0].GetEmcon().GetExpiresAtUnixMs() != deadline {
		t.Fatalf("stored=%+v, want the flat deadline on the layer EMCON", stored)
	}
	if _, _, e := effective(policy.Flatten(stored, "", now)); e != pb.EmconMode_EMCON_MODE_SILENT {
		t.Fatalf("emcon now=%v", e)
	}
	if s, _, e := effective(policy.Flatten(stored, "", now.Add(11*time.Minute))); e != pb.EmconMode_EMCON_MODE_NORMAL || !sameLabels(s, "bird") {
		t.Fatalf("+11m emcon=%v suppress=%v, want NORMAL with the new rule intact", e, s)
	}
}
