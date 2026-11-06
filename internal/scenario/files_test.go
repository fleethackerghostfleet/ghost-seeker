package scenario

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryScenarioFramePaths(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "scenarios", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no repository scenarios found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			sc, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(sc.Sets) == 0 {
				t.Fatal("scenario has no frame sets")
			}
			for i, set := range sc.Sets {
				t.Run(filepath.Base(set.Path), func(t *testing.T) {
					info, err := os.Stat(set.Path)
					if err != nil {
						t.Fatalf("set %d frame path: %v", i, err)
					}
					if info.IsDir() {
						return
					}
					f, err := os.Open(set.Path)
					if err != nil {
						t.Fatal(err)
					}
					header := make([]byte, len("version https://git-lfs.github.com/spec/v1"))
					_, err = io.ReadFull(f, header)
					f.Close()
					if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
						t.Fatal(err)
					}
					if string(header) == "version https://git-lfs.github.com/spec/v1" {
						t.Skipf("frame archive is a Git LFS pointer; run git lfs pull --include frames/%s", filepath.Base(set.Path))
					}
					if strings.HasSuffix(set.Path, ".zip") {
						archive, err := zip.OpenReader(set.Path)
						if err != nil {
							t.Fatal(err)
						}
						archive.Close()
					}
				})
			}
		})
	}
}
