package opauth

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLegacyOperatorAuthConfig(t *testing.T) {
	for _, value := range []string{"", "false", "0", "true", "TRUE", "1", "invalid"} {
		t.Setenv("GS_REQUIRE_OPERATOR_AUTH", value)
		wantError := value != "" && value != "false" && value != "0"
		if err := CheckLegacyOperatorAuth(); (err != nil) != wantError {
			t.Fatalf("%q: %v", value, err)
		}
	}
}

func TestLegacyOperatorAuthStartup(t *testing.T) {
	for _, app := range []string{"edge", "shore", "ghost-seeker"} {
		t.Run(app, func(t *testing.T) {
			t.Setenv("GS_REQUIRE_OPERATOR_AUTH", "true")
			command := exec.Command("go", "run", "../../cmd/"+app, "-help")
			command.Env = os.Environ()
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "GS_REQUIRE_OPERATOR_AUTH is no longer supported") {
				t.Fatalf("did not fail closed: %v %s", err, output)
			}
		})
	}
}

func TestLoginNextControlCharacters(t *testing.T) {
	for _, next := range []string{"/\t/evil.test", "/\n/evil.test", "/\r/evil.test", "/\t\\evil.test"} {
		if got := safeNext(next); got != "/" {
			t.Fatalf("unsafe redirect %q -> %q", next, got)
		}
	}
}
