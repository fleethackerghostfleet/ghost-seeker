//go:build prod

package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestProdContractNoDemoImagery(t *testing.T) {
	if _, err := fs.Stat(files, "static/imagery"); err == nil {
		t.Fatal("prod build embeds static/imagery")
	}
	for _, path := range []string{"/imagery/empty-ocean/sec_0001.jpg", "/imagery/library/x.jpg"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, response.Code)
		}
	}
	entries, err := os.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "imagery" {
			continue
		}
		if _, err := fs.Stat(files, "static/"+entry.Name()); err != nil {
			t.Errorf("prod embed is missing console asset %s; update embed_prod.go", entry.Name())
		}
	}
}
