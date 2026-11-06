//go:build prod

package opauth

import (
	"flag"
	"path/filepath"
	"testing"
)

func TestProdContractRealOperatorToken(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	if *DevTokenFileFlag(fs) != "none" || fs.Lookup("dev-token-file") != nil {
		t.Fatal("prod build registers -dev-token-file")
	}
	for _, dev := range []string{"", "none", DefaultDevFile, filepath.Join(t.TempDir(), "operator.token")} {
		if _, _, err := Load("", "", dev); err == nil {
			t.Fatalf("prod build generated a dev token (dev file %q)", dev)
		}
	}
	if tok, dev, err := Load("0123456789abcdef", "", DefaultDevFile); err != nil || dev || tok == "" {
		t.Fatalf("explicit token rejected: %v dev=%v", err, dev)
	}
	s := NewSessions("0123456789abcdef", false)
	if s.dev || s.anyPeer || s.demoAcceptAnyToken {
		t.Fatal("prod sessions enable demo auth")
	}
}
