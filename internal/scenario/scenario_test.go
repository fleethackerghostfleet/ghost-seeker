package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPathsAndPeriod(t *testing.T) {
	for _, period := range []float64{-1, 0, .25} {
		dir := t.TempDir()
		absolute := filepath.Join(dir, "absolute.zip")
		raw, err := json.Marshal(map[string]any{"name": "test", "period_s": period, "sets": []map[string]string{{"Path": "frames.zip"}, {"Path": absolute}}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "scenario.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := period
		if period <= 0 {
			want = 10
		}
		if s.Name != "test" || s.PeriodS != want || s.Sets[0].Path != filepath.Join(dir, "frames.zip") || s.Sets[1].Path != absolute {
			t.Fatalf("loaded = %+v", s)
		}
	}
}

func TestLoadErrorsAndEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
	for _, raw := range []string{"", "{", `{"period_s":"fast"}`} {
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.PeriodS != 10 || len(s.Sets) != 0 {
		t.Fatalf("empty = %+v, %v", s, err)
	}
}

func TestEventMessages(t *testing.T) {
	empty := Event{}
	if p, err := empty.PolicyMsg(); p != nil || err != nil {
		t.Fatalf("empty policy: %v, %v", p, err)
	}
	if p, err := empty.PullMsg(); p != nil || err != nil {
		t.Fatalf("empty pull: %v, %v", p, err)
	}
	if p, err := empty.OverrideMsg(); p != nil || err != nil {
		t.Fatalf("empty override: %v, %v", p, err)
	}
	e := Event{AtS: 1.25, Policy: json.RawMessage(`{"id":"p","interest":{"suppressLabels":["cargo ship"]}}`), Pull: json.RawMessage(`{"itemId":"i","form":"FORM_KIND_FULL"}`), Override: json.RawMessage(`{"itemId":"i","label":"tug"}`)}
	if e.At() != 1250*time.Millisecond {
		t.Fatalf("event time = %v", e.At())
	}
	if p, err := e.PolicyMsg(); err != nil || p.Id != "p" || len(p.Interest.SuppressLabels) != 1 {
		t.Fatalf("policy: %v, %v", p, err)
	}
	if p, err := e.PullMsg(); err != nil || p.ItemId != "i" || p.Form.String() != "FORM_KIND_FULL" {
		t.Fatalf("pull: %v, %v", p, err)
	}
	if p, err := e.OverrideMsg(); err != nil || p.ItemId != "i" || p.Label != "tug" {
		t.Fatalf("override: %v, %v", p, err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{"notAField":true}`)} {
		e := Event{Policy: raw, Pull: raw, Override: raw}
		if _, err := e.PolicyMsg(); err == nil {
			t.Fatal("accepted invalid policy")
		}
		if _, err := e.PullMsg(); err == nil {
			t.Fatal("accepted invalid pull")
		}
		if _, err := e.OverrideMsg(); err == nil {
			t.Fatal("accepted invalid override")
		}
	}
}
