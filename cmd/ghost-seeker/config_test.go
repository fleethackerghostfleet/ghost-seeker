package main

import (
	"crypto/tls"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentConfiguration(t *testing.T) {
	const key = "GS_H4_CONFIG_TEST"
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", false}, {"true", true}, {"TRUE", true}, {"1", true}, {"false", false}, {"0", false}, {"invalid", false}} {
		t.Run("bool/"+tc.value, func(t *testing.T) {
			t.Setenv(key, tc.value)
			if got := envBool(key); got != tc.want {
				t.Fatalf("envBool(%q)=%v, want %v", tc.value, got, tc.want)
			}
		})
	}
	for _, value := range []string{"", "configured", " "} {
		t.Run("string/"+value, func(t *testing.T) {
			t.Setenv(key, value)
			want := value
			if value == "" {
				want = "fallback"
			}
			if got := envOr(key, "fallback"); got != want {
				t.Fatalf("envOr=%q, want %q", got, want)
			}
		})
	}
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", 5 * time.Second}, {"250ms", 250 * time.Millisecond}, {"1m30s", 90 * time.Second}, {"0", 0}, {"-1s", -time.Second}} {
		t.Run("duration/"+tc.value, func(t *testing.T) {
			t.Setenv(key, tc.value)
			if got := envDuration(key, 5*time.Second); got != tc.want {
				t.Fatalf("envDuration=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnvDurationRejectsMalformed(t *testing.T) {
	if os.Getenv("GS_H4_DURATION_CHILD") == "1" {
		envDuration("GS_H4_DURATION", time.Second)
		t.Fatal("malformed duration accepted")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestEnvDurationRejectsMalformed$")
	cmd.Env = append(os.Environ(), "GS_H4_DURATION_CHILD=1", "GS_H4_DURATION=not-a-duration")
	output, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || !strings.Contains(string(output), "invalid GS_H4_DURATION: expected duration") {
		t.Fatalf("malformed duration: err=%v output=%s", err, output)
	}
}

func TestShaperTargetAddress(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://shore.example/path?q=1", "shore.example:80"},
		{"https://shore.example/path", "shore.example:443"},
		{"https://shore.example:9443/path", "shore.example:9443"},
		{"http://[::1]", "[::1]:80"},
		{"https://[::1]:8443", "[::1]:8443"},
	} {
		got, err := shaperTarget(tc.url)
		if err != nil || got != tc.want {
			t.Errorf("shaperTarget(%q)=%q/%v, want %q", tc.url, got, err, tc.want)
		}
	}
	for _, value := range []string{"http://shore.example:bad-port", "http://%zz", "://"} {
		if _, err := shaperTarget(value); err == nil {
			t.Errorf("malformed shaper URL accepted: %q", value)
		}
	}
}

func TestServeReturnsListenerErrors(t *testing.T) {
	for _, config := range []*tls.Config{nil, {MinVersion: tls.VersionTLS12}} {
		// An invalid numeric port fails locally, before opening any socket.
		if err := serve("127.0.0.1:-1", http.NotFoundHandler(), config); err == nil {
			t.Fatal("listener error suppressed")
		}
	}
}
