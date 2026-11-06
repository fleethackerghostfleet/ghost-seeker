//go:build !prod

package shaper

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

type scriptedConn struct {
	reader             io.Reader
	written            bytes.Buffer
	readHook           func([]byte) (int, error)
	writeErr           error
	readSizes          []int
	closed, halfClosed int
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	c.readSizes = append(c.readSizes, len(p))
	if c.readHook != nil {
		return c.readHook(p)
	}
	return c.reader.Read(p)
}
func (c *scriptedConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.written.Write(p)
}
func (c *scriptedConn) Close() error                   { c.closed++; return nil }
func (c *scriptedConn) CloseWrite() error              { c.halfClosed++; return nil }
func (*scriptedConn) LocalAddr() net.Addr              { return nil }
func (*scriptedConn) RemoteAddr() net.Addr             { return nil }
func (*scriptedConn) SetDeadline(time.Time) error      { return nil }
func (*scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (*scriptedConn) SetWriteDeadline(time.Time) error { return nil }

func TestPipeChunksAndAccountsWithoutSockets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bps   int64
		chunk int
	}{
		{"minimum", 8, 64}, {"normal", 80000, 1000}, {"maximum", 16000000, 64 << 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New("unused", tc.bps)
				payload := bytes.Repeat([]byte("x"), tc.chunk+3)
				src := &scriptedConn{reader: bytes.NewReader(payload)}
				dst := &scriptedConn{}
				s.pipe(context.Background(), dst, src, &pacer{})
				if !bytes.Equal(dst.written.Bytes(), payload) || s.Bytes() != int64(len(payload)) || dst.halfClosed != 1 {
					t.Fatalf("forwarded=%d bytes=%d halfClose=%d", dst.written.Len(), s.Bytes(), dst.halfClosed)
				}
				for _, size := range src.readSizes {
					if size != tc.chunk {
						t.Fatalf("read chunk=%d want=%d", size, tc.chunk)
					}
				}
			})
		})
	}
}

func TestPipeFailureAndOutageDoNotCountUnwrittenBytes(t *testing.T) {
	for _, mode := range []string{"down", "read-error", "write-error", "outage-after-read", "canceled-pacer"} {
		t.Run(mode, func(t *testing.T) {
			s := New("unused", 80000)
			src := &scriptedConn{reader: bytes.NewReader([]byte("frame"))}
			dst := &scriptedConn{}
			ctx := context.Background()
			p := &pacer{}
			switch mode {
			case "down":
				s.SetBps(0)
			case "read-error":
				src.readHook = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
			case "write-error":
				dst.writeErr = io.ErrClosedPipe
			case "outage-after-read":
				src.readHook = func(b []byte) (int, error) { s.SetBps(0); return copy(b, "frame"), nil }
			case "canceled-pacer":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				p.next = time.Now().Add(time.Hour)
			}
			s.pipe(ctx, dst, src, p)
			if s.Bytes() != 0 || dst.written.Len() != 0 || dst.halfClosed != 0 {
				t.Fatalf("failure forwarded bytes=%d written=%d halfClose=%d", s.Bytes(), dst.written.Len(), dst.halfClosed)
			}
			if mode == "down" && len(src.readSizes) != 0 {
				t.Fatal("read during outage")
			}
		})
	}
}

func TestNormalPublicRateAndOutageConnectionLifecycle(t *testing.T) {
	s := NewNormal("unused")
	d := s.Distribution()
	for _, tc := range []struct {
		z    float64
		want int64
	}{{-100, 0}, {0, MeanBps}, {100, MaxBps}} {
		if got := d.Rate(tc.z); got != tc.want {
			t.Fatalf("Rate(%v)=%d want=%d", tc.z, got, tc.want)
		}
	}
	a, b := &scriptedConn{}, &scriptedConn{}
	s.track(a, true)
	s.track(b, true)
	s.SetBps(-1)
	if s.Bps() != 0 || a.closed != 1 || b.closed != 1 {
		t.Fatalf("outage=%d closes=%d/%d", s.Bps(), a.closed, b.closed)
	}
	s.track(a, false)
	s.track(b, false)
	s.SetBps(0)
	if a.closed != 1 || b.closed != 1 || len(s.conns) != 0 {
		t.Fatal("untracked connections closed again")
	}
	down := &scriptedConn{}
	s.handle(context.Background(), down)
	if down.closed != 1 || len(down.readSizes) != 0 {
		t.Fatal("down link accepted input")
	}
	if err := s.ListenAndServe(context.Background(), "127.0.0.1:-1"); err == nil {
		t.Fatal("invalid listen address accepted")
	}
}
