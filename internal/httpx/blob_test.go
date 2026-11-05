package httpx

import (
	"bytes"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBlobContentTypeSniffsAndAllowlists(t *testing.T) {
	html := []byte("<html><script>alert(1)</script></html>")
	for _, tc := range []struct {
		declared string
		data     []byte
		want     string
		inline   bool
	}{
		{"text/html", pngBytes(t), "image/png", true},           // sender lies about an image
		{"image/jpeg", html, "application/octet-stream", false}, // sender lies about HTML
		{"image/svg+xml", []byte("<svg onload=alert(1)/>"), "application/octet-stream", false},
		{"application/json", []byte(`{"frames":3}`), "application/json", true},
		{"application/json", html, "application/octet-stream", false},
		{"text/plain", []byte("hello"), "text/plain; charset=utf-8", true},
		{"text/plain", []byte{0xff, 0xfe, 0xfd}, "application/octet-stream", false},
		{"", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, "image/jpeg", true},
	} {
		got, inline := BlobContentType(tc.declared, tc.data)
		if got != tc.want || inline != tc.inline {
			t.Errorf("BlobContentType(%q, %q) = %q,%v want %q,%v", tc.declared, tc.data[:min(len(tc.data), 12)], got, inline, tc.want, tc.inline)
		}
	}
}

func TestServeBlobHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	ServeBlob(w, "text/html", []byte("<script>1</script>"))
	h := w.Header()
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("octet headers: %v", h)
	}
	w = httptest.NewRecorder()
	ServeBlob(w, "image/jpeg", pngBytes(t))
	if h := w.Header(); h.Get("Content-Type") != "image/png" || h.Get("Content-Disposition") != "inline" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("image headers: %v", h)
	}
}
