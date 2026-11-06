package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestRequestBodyStatus(t *testing.T) {
	large := &http.MaxBytesError{Limit: 4096}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{large, 413}, {fmt.Errorf("trailing input: %w", large), 413},
		{errors.New("invalid JSON"), 400}, {nil, 400},
	} {
		if got := RequestBodyStatus(tc.err); got != tc.want {
			t.Fatalf("%v: status=%d want=%d", tc.err, got, tc.want)
		}
	}
}
