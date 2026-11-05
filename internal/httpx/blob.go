package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"
	"unicode/utf8"
)

// BlobContentType decides what a stored form is served as. The sender's
// declared MIME is never trusted: images are sniffed from the bytes and must
// be on the allowlist, FACT records must parse as JSON, text must be UTF-8;
// everything else is application/octet-stream. The second result says whether
// the browser may render it inline.
func BlobContentType(declared string, data []byte) (ctype string, inline bool) {
	switch sniffed := http.DetectContentType(data); sniffed {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/bmp":
		return sniffed, true
	}
	if isAVIF(data) {
		return "image/avif", true
	}
	switch declared {
	case "application/json":
		if json.Valid(data) {
			return "application/json", true
		}
	case "text/plain", "text/plain; charset=utf-8":
		if utf8.Valid(data) {
			return "text/plain; charset=utf-8", true
		}
	}
	return "application/octet-stream", false
}

func isAVIF(b []byte) bool {
	return len(b) >= 12 && string(b[4:8]) == "ftyp" && (string(b[8:12]) == "avif" || string(b[8:12]) == "avis")
}

// ServeBlob writes data with the sniffed/allowlisted Content-Type, nosniff,
// a Content-Disposition (attachment when not renderable) and a CSP that
// keeps a blob from ever executing on the console's origin.
func ServeBlob(w http.ResponseWriter, declared string, data []byte) {
	ctype, inline := BlobContentType(declared, data)
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	if inline {
		h.Set("Content-Disposition", "inline")
	} else {
		h.Set("Content-Disposition", "attachment; filename=\"blob.bin\"")
	}
	_, _ = w.Write(data)
}
