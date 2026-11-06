package ingest

import (
	"context"
	"errors"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
)

// PullMJPEG reconnects an HTTP multipart camera with bounded frames and no CGO.
// The URL is administrator configuration, not an untrusted request parameter.
func PullMJPEG(ctx context.Context, q *Queue, rawURL string, m Metadata, maxBytes int64, fps float64) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("mjpeg-url must be an HTTP(S) URL")
	}
	if fps <= 0 || fps > 60 || math.IsNaN(fps) {
		return errors.New("mjpeg-fps must be in (0, 60]")
	}
	if !ids.ValidName(m.Source) {
		return errors.New("valid mjpeg source required")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFrameBytes
	}
	client := newMJPEGClient(30 * time.Second)
	defer client.CloseIdleConnections()
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		connectedAt := time.Now()
		err := mjpegConnection(ctx, client, q, rawURL, m, maxBytes, time.Duration(float64(time.Second)/fps))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			q.count("mjpeg:"+m.Source, func(s *SourceStats) { s.Failed++ })
		}
		if time.Since(connectedAt) > 10*time.Second {
			backoff = 250 * time.Millisecond
		}
		if err := pause(ctx, backoff); err != nil {
			return err
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
	return ctx.Err()
}

type cameraConn struct {
	net.Conn
	idle time.Duration
}

func (c cameraConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

func newMJPEGClient(idle time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{Transport: &http.Transport{
		ResponseHeaderTimeout: 10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return cameraConn{Conn: conn, idle: idle}, nil
		},
	}}
}

func mjpegConnection(ctx context.Context, client *http.Client, q *Queue, rawURL string, m Metadata, maxBytes int64, period time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errors.New("invalid MJPEG request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("MJPEG connection failed")
	}
	defer resp.Body.Close()
	t, p, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || resp.StatusCode != 200 || t != "multipart/x-mixed-replace" || p["boundary"] == "" {
		return errors.New("expected multipart/x-mixed-replace camera response")
	}
	mr := multipart.NewReader(resp.Body, p["boundary"])
	for ctx.Err() == nil {
		part, err := mr.NextPart()
		if err != nil {
			return err
		}
		t, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if t != "image/jpeg" {
			return errors.New("expected image/jpeg MJPEG frame")
		}
		data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > maxBytes {
			return errors.New("MJPEG frame exceeds byte cap")
		}
		if err := checkType(data, t); err != nil {
			return err
		}
		c, err := m.Capture(data)
		if err != nil {
			return err
		}
		if _, err = q.Push(ctx, c, "mjpeg"); err != nil && !errors.Is(err, ErrBusy) {
			return err
		}
		if err := pause(ctx, period); err != nil {
			return err
		}
	}
	return ctx.Err()
}
