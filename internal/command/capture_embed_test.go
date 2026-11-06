//go:build !prod

package command

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"io/fs"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/web"
)

func TestEmbeddedImageMetadata(t *testing.T) {
	t.Chdir(t.TempDir())
	data, err := fs.ReadFile(web.Imagery(), "eo-p7-02.jpg")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	got := imageMetadata("eo-p7-02.jpg")
	if got.hash != fmt.Sprintf("%x", sha256.Sum256(data)) || got.w != cfg.Width || got.h != cfg.Height {
		t.Fatalf("incorrect embedded metadata: %+v", got)
	}
}
