package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerDoesNotListDirectories(t *testing.T) {
	handler := Handler()
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/fonts/", want: http.StatusNotFound},
		{path: "/", want: http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.want {
			t.Errorf("GET %s status = %d, want %d", tc.path, response.Code, tc.want)
		}
	}
}
