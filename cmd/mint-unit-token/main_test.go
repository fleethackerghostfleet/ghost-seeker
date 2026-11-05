package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

const signingKey = "test-signing-key-at-least-32-bytes-long"

func TestOperatorFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing echelon",
			args: []string{"-id", "TAO-DDG51", "-role", "operator", "-signing-key", signingKey},
			want: "-echelon is required",
		},
		{
			name: "bad role",
			args: []string{"-id", "TAO-DDG51", "-role", "admin", "-signing-key", signingKey},
			want: "invalid -role",
		},
		{
			name: "unknown echelon",
			args: []string{"-id", "TAO-DDG51", "-role", "operator", "-echelon", "submarine", "-signing-key", signingKey},
			want: "unknown echelon",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("run()=%d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	args := []string{"-id", "TAO-DDG51", "-role", "operator", "-echelon", "ship", "-signing-key", signingKey}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("run()=%d, stderr=%q", code, stderr.String())
	}
	auth, err := unitauth.New(true, signingKey)
	if err != nil {
		t.Fatal(err)
	}
	header := make(http.Header)
	header.Set(unitauth.OperatorJWTHeader, strings.TrimSpace(stdout.String()))
	operator, err := auth.OperatorFromHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if operator.ID != "TAO-DDG51" || operator.Echelon != pb.Echelon_ECHELON_SHIP {
		t.Fatalf("operator=%+v", operator)
	}
}
