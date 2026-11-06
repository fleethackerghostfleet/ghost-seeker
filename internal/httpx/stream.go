package httpx

import (
	"io"
	"net/http"
	"time"
)

const MaxStreamClients = 64

func WriteStream(w http.ResponseWriter, data string) bool {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(w, data); err != nil {
		return false
	}
	return controller.Flush() == nil
}
