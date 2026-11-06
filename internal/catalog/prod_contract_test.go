//go:build prod

package catalog

import "testing"

func TestProdContractNoDemoLibrary(t *testing.T) {
	for source, images := range Library {
		if len(images) > 0 {
			t.Fatalf("prod build embeds demo imagery for %s", source)
		}
	}
}
