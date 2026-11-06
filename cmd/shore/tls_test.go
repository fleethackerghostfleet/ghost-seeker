package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestUnpairedTLSFlags(t *testing.T) {
	if mode := os.Getenv("GS_TEST_UNPAIRED_TLS"); mode != "" {
		cert, key := "", ""
		if mode == "cert" {
			cert = "server.pem"
		} else {
			key = "server.key"
		}
		os.Args = []string{"shore", "-tls-cert", cert, "-tls-key", key}
		main()
		return
	}
	for _, mode := range []string{"cert", "key"} {
		t.Run(mode, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "-test.run=^TestUnpairedTLSFlags$")
			command.Env = append(os.Environ(), "GS_TEST_UNPAIRED_TLS="+mode)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "-tls-cert and -tls-key must be set together") {
				t.Fatalf("startup did not reject unpaired TLS flags: %s %v", output, err)
			}
		})
	}
}
