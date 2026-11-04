// Package httpx has the small HTTP plumbing both nodes share: CORS for the
// HTML console, blob serving, static UI.
package httpx

import (
	"net/http"
	"os"
)

// CORS allows a UI served from anywhere (file://, another port) to call the
// JSON API. Fine for a closed shipboard network; tighten for production.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// StaticUI serves dir at / if it exists (the HTML team's subdir), else a
// one-line index pointing at the API.
func StaticUI(mux *http.ServeMux, dir string, apiHint string) {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		mux.Handle("/", http.FileServer(http.Dir(dir)))
		return
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<pre>ghost-seeker " + apiHint + "\nAPI: POST /ghostseeker.v1.ConsoleService/&lt;Method&gt; (JSON)\nBlobs: GET /blob/{item_id}/{form}\n</pre>"))
	})
}
