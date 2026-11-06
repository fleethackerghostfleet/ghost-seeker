package shorebridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInferenceCapabilitiesAuthenticatedCachedAndFailClosed(t *testing.T) {
	for _, status := range []int{200, 401, 404, 503} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/ship/inference" || r.Header.Get("Authorization") != "Bearer fixture" {
				t.Error("missing endpoint or operator credential")
			}
			w.WriteHeader(status)
			w.Write([]byte(`{"openVocabulary":true,"fixedLabels":["boat"],"embeddingDim":512}`))
		}))
		provider := New("", srv.URL, nil).WithToken("fixture").InferenceCapabilities()
		for i := 0; i < 2; i++ {
			if got := provider.Capabilities().OpenVocabulary; got != (status == 200) {
				t.Fatalf("status %d capability %v", status, got)
			}
		}
		if calls != 1 {
			t.Fatalf("cache requests %d", calls)
		}
		srv.Close()
	}
	if New("", "", nil).InferenceCapabilities().Capabilities().OpenVocabulary {
		t.Fatal("missing edge advertised arbitrary text")
	}
}
