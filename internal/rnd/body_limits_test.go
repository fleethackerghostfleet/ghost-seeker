package rnd

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGradeBodyOverflowReturns413WithoutSavingGrade(t *testing.T) {
	dir, logs := fixture(t)
	s, err := OpenSession(dir, logs, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, body := range []string{`{"id":"` + strings.Repeat("x", 4096) + `","tier":1}`, `{"id":"a","tier":1}` + strings.Repeat(" ", 4096)} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/grade", strings.NewReader(body))
		r.Host = "localhost"
		s.Handler().ServeHTTP(w, r)
		if w.Code != 413 || len(s.State().Grades) != 0 {
			t.Fatalf("status=%d grades=%v", w.Code, s.State().Grades)
		}
	}
}
