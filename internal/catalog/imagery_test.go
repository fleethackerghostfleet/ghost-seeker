package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryImages(t *testing.T) {
	sources := make(map[string]Source, len(Sources))
	for _, source := range Sources {
		sources[source.ID] = source
	}

	for sourceID, images := range Library {
		source, ok := sources[sourceID]
		if !ok {
			t.Errorf("library references unknown source %q", sourceID)
			continue
		}
		for _, image := range images {
			if _, err := os.Stat(filepath.Join("..", "..", "web", "static", filepath.FromSlash(image.File))); err != nil {
				t.Errorf("image %q: %v", image.File, err)
			}
			for _, detection := range image.Dets {
				if _, ok := Classes[detection.Cls]; !ok {
					t.Errorf("image %q has unknown class %q", image.File, detection.Cls)
				}
				if _, ok := Pools[source.Scene][detection.Cls]; !ok {
					t.Errorf("image %q class %q is not in the %q source pool", image.File, detection.Cls, source.Scene)
				}
				box := detection.B
				if box.X < 0 || box.Y < 0 || box.W <= 0 || box.H <= 0 || box.X+box.W > 480 || box.Y+box.H > 300 {
					t.Errorf("image %q class %q has out-of-bounds box %+v", image.File, detection.Cls, box)
				}
			}
		}
	}

	for _, source := range Sources {
		if len(Library[source.ID]) == 0 {
			t.Errorf("source %q has no library images", source.ID)
		}
	}
}
