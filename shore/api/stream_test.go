package api_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/opauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/api"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

func TestStreamHTTP2IdleHeartbeat(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := api.New(st)
	a.WriteTimeout = 100 * time.Millisecond
	a.Heartbeat = 400 * time.Millisecond
	server := httptest.NewUnstartedServer(opauth.RequireBearer(operator, a.Handler()))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+operator)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 || response.StatusCode != http.StatusOK {
		t.Fatalf("stream response: %s %s", response.Proto, response.Status)
	}
	reader := bufio.NewReader(response.Body)
	for heartbeats := 0; heartbeats < 2; {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("idle stream closed before heartbeat %d: %v", heartbeats+1, err)
		}
		if strings.TrimSpace(line) == ": heartbeat" {
			heartbeats++
		}
	}
}
