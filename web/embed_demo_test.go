//go:build !prod

package web

import (
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmptyOceanArchiveServed(t *testing.T) {
	for _, path := range []string{"/imagery/empty-ocean/sec_0001.jpg", "/imagery/empty-ocean/sec_0010.jpg"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, response.Code)
		}
		cfg, err := jpeg.DecodeConfig(response.Body)
		if err != nil || cfg.Width != 1920 || cfg.Height != 1080 {
			t.Fatalf("native frame: %+v %v", cfg, err)
		}
	}
	for _, path := range []string{"/imagery/empty-ocean/", "/imagery/empty-ocean/missing.jpg"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, response.Code)
		}
	}
}
