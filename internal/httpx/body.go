package httpx

import (
	"errors"
	"net/http"
)

// RequestBodyStatus distinguishes an exceeded body cap from malformed input.
func RequestBodyStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
