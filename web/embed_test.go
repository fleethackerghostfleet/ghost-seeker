package web

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerDoesNotListDirectories(t *testing.T) {
	handler := Handler()
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/fonts/", want: http.StatusNotFound},
		{path: "/", want: http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.want {
			t.Errorf("GET %s status = %d, want %d", tc.path, response.Code, tc.want)
		}
	}
}

func TestEmptyOceanArchiveServed(t *testing.T) {
	ocean, err := files.ReadFile("static/imagery/empty-ocean.zip")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(ocean, []byte("version https://git-lfs.github.com/spec/v1\n")) {
		t.Skip("empty-ocean.zip is a Git LFS pointer; run git lfs pull --include web/static/imagery/empty-ocean.zip")
	}
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
