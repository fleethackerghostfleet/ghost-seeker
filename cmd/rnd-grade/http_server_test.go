package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGraderHTTPTimeouts(t *testing.T) {
	s := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 30*time.Second || s.WriteTimeout != 30*time.Second || s.IdleTimeout != 120*time.Second || s.MaxHeaderBytes != 1<<20 {
		t.Fatalf("unexpected server bounds: %+v", s)
	}
}

func TestGraderCutsOffIncompleteHeaders(t *testing.T) {
	called := make(chan struct{}, 1)
	s := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called <- struct{}{} }))
	s.ReadHeaderTimeout = 25 * time.Millisecond
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	defer func() { s.Close(); <-done }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(900 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Incomplete: "); err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(conn)
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("header cutoff did not close connection: %v", err)
	}
	select {
	case <-called:
		t.Fatal("handler reached before headers completed")
	default:
	}
}

func TestGraderCutsOffIncompleteBody(t *testing.T) {
	readErr := make(chan error, 1)
	s := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readErr <- err
	}))
	s.ReadTimeout = 25 * time.Millisecond
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	defer func() { s.Close(); <-done }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(900 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
			t.Fatalf("body read error=%v, want timeout", err)
		}
	case <-time.After(900 * time.Millisecond):
		t.Fatal("body read did not time out")
	}
}
