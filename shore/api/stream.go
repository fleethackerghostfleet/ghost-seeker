package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// stream reads at most 32 journal entries per client at a time. There is no
// unbounded publish queue: slow readers time out and resume from durable SQLite.
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("since")
	}
	cursor, err := parseCursor(value)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	max, err := a.Store.EventCursor(r.Context())
	if err != nil {
		fail(w, 503, "store unavailable")
		return
	}
	if cursor > max {
		fail(w, 400, "stream cursor is ahead of this store")
		return
	}
	select {
	case a.clients <- struct{}{}:
		defer func() { <-a.clients }()
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "too many stream clients")
		return
	}
	ctrl := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	flush := func(data string) bool {
		_ = ctrl.SetWriteDeadline(time.Now().Add(a.WriteTimeout))
		if _, err := fmt.Fprint(w, data); err != nil {
			return false
		}
		return ctrl.Flush() == nil
	}
	if !flush("retry: 1000\n: connected\n\n") {
		return
	}
	defer func() { _ = ctrl.SetWriteDeadline(time.Time{}) }()
	poll := time.NewTicker(a.PollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(a.Heartbeat)
	defer heartbeat.Stop()
	for {
		events, err := a.Store.Events(r.Context(), cursor, 32)
		if err != nil {
			return
		}
		for _, event := range events {
			receipt, err := a.Store.Receipt(r.Context(), event.ID)
			if err != nil {
				return
			}
			item, err := a.item(r.Context(), receipt)
			if err != nil {
				return
			}
			id := strconv.FormatInt(event.Sequence, 10)
			data, err := json.Marshal(map[string]any{"cursor": id, "event": event.Kind, "item": item})
			if err != nil {
				return
			}
			if !flush("id: " + id + "\nevent: item\ndata: " + string(data) + "\n\n") {
				return
			}
			cursor = event.Sequence
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !flush(": heartbeat\n\n") {
				return
			}
		default:
		}
		if len(events) == 32 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if !flush(": heartbeat\n\n") {
				return
			}
		}
	}
}
