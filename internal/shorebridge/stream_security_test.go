package shorebridge

import (
	"context"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"net/http/httptest"
	"testing"
)

func TestStreamSubscriberCaps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	bridge := &Bridge{subs: make(map[chan []byte]struct{})}
	console := &Console{subs: make(map[chan sseEvent]struct{})}
	for i := 0; i < httpx.MaxStreamClients; i++ {
		bridge.subs[make(chan []byte)] = struct{}{}
		console.subs[make(chan sseEvent)] = struct{}{}
	}
	for name, handler := range map[string]func(*httptest.ResponseRecorder){
		"shore": func(w *httptest.ResponseRecorder) { bridge.stream(w, request) },
		"edge":  func(w *httptest.ResponseRecorder) { console.stream(w, request) },
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler(w)
			if w.Code != 429 {
				t.Fatalf("subscriber cap bypassed: %d", w.Code)
			}
		})
	}
}
