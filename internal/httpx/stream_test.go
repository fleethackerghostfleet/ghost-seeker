package httpx

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

type stalledStream struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *stalledStream) SetWriteDeadline(deadline time.Time) error { w.deadline = deadline; return nil }
func (w *stalledStream) Write(p []byte) (int, error)               { return 0, io.ErrClosedPipe }
func (w *stalledStream) WriteString(p string) (int, error)         { return w.Write([]byte(p)) }

func TestStreamDeadlineAndFailedWriteEviction(t *testing.T) {
	writer := &stalledStream{ResponseRecorder: httptest.NewRecorder()}
	if WriteStream(writer, "blocked") {
		t.Fatal("failed write must evict subscriber")
	}
	if until := time.Until(writer.deadline); until <= 0 || until > 10*time.Second {
		t.Fatalf("missing bounded write deadline: %v", writer.deadline)
	}
	normal := httptest.NewRecorder()
	if !WriteStream(normal, "event: test\n\n") || !normal.Flushed {
		t.Fatal("normal stream did not flush")
	}
}
