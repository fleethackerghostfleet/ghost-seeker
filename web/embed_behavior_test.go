package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestStaticAssetGetHeadAndRangeContract(t *testing.T) {
	want, err := files.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 16 {
		t.Fatal("asset fixture is too short for range tests")
	}
	handler := Handler()
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/auth.js?version=hostile%3Cscript%3E", nil))
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), want) || !strings.Contains(get.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("asset status=%d mime=%q bytes=%d", get.Code, get.Header().Get("Content-Type"), get.Body.Len())
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/auth.js", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(want)) {
		t.Fatalf("HEAD status=%d bytes=%d headers=%v", head.Code, head.Body.Len(), head.Header())
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/auth.js", nil)
	rangeRequest.Header.Set("Range", "bytes=0-15")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || !bytes.Equal(ranged.Body.Bytes(), want[:16]) || ranged.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-15/%d", len(want)) {
		t.Fatalf("range status=%d headers=%v bytes=%d", ranged.Code, ranged.Header(), ranged.Body.Len())
	}
	rangeRequest.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(want)+10))
	unsatisfiable := httptest.NewRecorder()
	handler.ServeHTTP(unsatisfiable, rangeRequest)
	if unsatisfiable.Code != http.StatusRequestedRangeNotSatisfiable || unsatisfiable.Header().Get("Content-Range") != fmt.Sprintf("bytes */%d", len(want)) {
		t.Fatalf("invalid range status=%d headers=%v", unsatisfiable.Code, unsatisfiable.Header())
	}
}

func TestStaticHandlerMissingAssetsAndDirectories(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/missing-file.js", "/fonts/", "/brand/", "/missing-directory/", "/imagery/", "/does-not-exist%3Cscript%3E.js"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s status=%d", path, w.Code)
			}
			if strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "Directory listing") {
				t.Fatalf("unsafe missing-file response=%q", w.Body.String())
			}
		})
	}
}
