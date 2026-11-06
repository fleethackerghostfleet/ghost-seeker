package shorebridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ---- HTTP ----

func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/shore/snapshot", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		snap := b.snap
		b.mu.Unlock()
		if snap == nil {
			snap = []byte(`{"objects":[],"shore":{"ok":false,"error":"no data yet"}}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(snap)
	})
	mux.HandleFunc("GET /api/shore/stream", b.stream)
	mux.HandleFunc("POST /api/shore/policy", b.setPolicy)
	mux.HandleFunc("POST /api/shore/emcon", b.setEMCON)
	mux.HandleFunc("POST /api/shore/pull", b.requestPull)
	mux.HandleFunc("POST /api/shore/override", b.override)
	mux.HandleFunc("GET /api/shore/link", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, b.linkStatus()) })
	b.registerDemoRoutes(mux)
	if u, err := url.Parse(b.shore); err == nil {
		// The browser's session cookie stays on this origin; the shore sees
		// the operator bearer token instead.
		rp := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.SetURL(u)
			proxyForwardedHeaders(pr)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			b.authorize(pr.Out)
		}}
		rp.Transport = b.client.Transport
		rp.ModifyResponse = func(res *http.Response) error {
			if res.Header.Get("Content-Security-Policy") == "" {
				res.Header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
			}
			res.Header.Set("X-Content-Type-Options", "nosniff")
			return nil
		}
		mux.Handle("GET /blob/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Outer middleware already set nosniff; the upstream copy would duplicate it.
			w.Header().Del("X-Content-Type-Options")
			rp.ServeHTTP(w, r)
		}))
		// The live operator app's token/PIV session protects this read-only
		// proxy. It exposes shore's durable contract, never the UI overlay.
		mux.Handle("GET /api/v1/", rp)
	}
	return mux
}

func proxyForwardedHeaders(pr *httputil.ProxyRequest) {
	headers := pr.In.Header.Clone()
	for _, connection := range headers.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if values, ok := headers[name]; ok {
			pr.Out.Header[name] = values
		}
	}
	// Match the legacy X-Forwarded-For append without generating host/proto headers.
	if clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
		prior, ok := pr.Out.Header["X-Forwarded-For"]
		if ok && prior == nil {
			return
		}
		if len(prior) > 0 {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		pr.Out.Header.Set("X-Forwarded-For", clientIP)
	}
}

func (b *Bridge) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan []byte, 4)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	last := b.snap
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}()
	send := func(buf []byte) {
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", buf)
		fl.Flush()
	}
	if last != nil {
		send(last)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case buf := <-ch:
			send(buf)
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func decodeJSONBody(reader io.Reader, dst any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
	return nil
}
