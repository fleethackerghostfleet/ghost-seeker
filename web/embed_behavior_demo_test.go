//go:build !prod

package web

import (
	"embed"
	"errors"
	"io/fs"
	"testing"
)

func TestHandlerFailsAtStartupWhenDemoArchiveMissing(t *testing.T) {
	original := files
	files = embed.FS{}
	defer func() { files = original }()
	defer func() {
		failure := recover()
		err, ok := failure.(error)
		if !ok || !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing embedded archive panic=%v", failure)
		}
	}()
	Handler()
}
