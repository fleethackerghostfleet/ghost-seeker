package shorebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"strings"
)

// ---- console RPC helpers ----

// WithToken sets the operator bearer token the bridge presents to the shore
// and edge consoles (and to shore blobs it proxies).
func (b *Bridge) WithToken(token string) *Bridge {
	b.token = token
	return b
}

func (b *Bridge) authorize(r *http.Request) {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
}

func (b *Bridge) call(ctx context.Context, base, method string, req, resp proto.Message) error {
	body, err := protojson.Marshal(req)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, base+consolePath+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/json")
	b.authorize(hr)
	res, err := b.client.Do(hr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return &consoleCallError{method: method, statusCode: res.StatusCode, status: res.Status,
			body: strings.TrimSpace(string(raw))}
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, resp)
}

type consoleCallError struct {
	method     string
	statusCode int
	status     string
	body       string
}

func (e *consoleCallError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.method, e.status, e.body)
}

func (e *consoleCallError) message() string {
	var response struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(e.body), &response); err == nil {
		if response.Message != "" {
			return response.Message
		}
		if response.Error != "" {
			return response.Error
		}
	}
	if e.body != "" {
		return e.body
	}
	return http.StatusText(e.statusCode)
}

func writeShoreAuthError(w http.ResponseWriter, err error) bool {
	var upstream *consoleCallError
	if !errors.As(err, &upstream) || (upstream.statusCode != http.StatusUnauthorized &&
		upstream.statusCode != http.StatusForbidden) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(upstream.statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": upstream.message()})
	return true
}
