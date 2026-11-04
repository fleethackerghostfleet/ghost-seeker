package sensor

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestTickUsesLibraryImageDetections(t *testing.T) {
	var source *catalog.Source
	for i := range catalog.Sources {
		if catalog.Sources[i].ID == "EO-P7" {
			source = &catalog.Sources[i]
			break
		}
	}
	if source == nil {
		t.Fatal("EO-P7 source not found")
	}
	images := catalog.Library[source.ID]
	if len(images) == 0 {
		t.Fatalf("source %q has no library images", source.ID)
	}

	generator := NewGenerator()
	var itemReturned bool
	for attempt := 0; attempt < 500; attempt++ {
		item, _ := generator.Tick(source, time.Now(), "maritime", nil)
		if item == nil {
			continue
		}
		itemReturned = true

		var image *catalog.LibraryImage
		for i := range images {
			if images[i].File == item.Img {
				image = &images[i]
				break
			}
		}
		if image == nil {
			t.Fatalf("item image %q is not in source %q library", item.Img, source.ID)
		}
		if len(item.Dets) < len(image.Dets) || len(item.Dets) > len(image.Dets)+1 {
			t.Fatalf("detection count = %d, want %d for %q", len(item.Dets), len(image.Dets), image.File)
		}
		for i, detection := range item.Dets[:len(image.Dets)] {
			want := image.Dets[i]
			if detection.Class != want.Cls {
				t.Errorf("detection %d class = %q, want %q", i, detection.Class, want.Cls)
			}
			if detection.Box.X != want.B.X || detection.Box.Y != want.B.Y || detection.Box.W != want.B.W || detection.Box.H != want.B.H {
				t.Errorf("detection %d box = %+v, want %+v", i, detection.Box, want.B)
			}
			if detection.Box.Top {
				t.Errorf("detection %d Top = true, want false", i)
			}
			if detection.Conf < .55 || detection.Conf > .98 {
				t.Errorf("detection %d confidence = %.2f, outside [.55, .98]", i, detection.Conf)
			}
		}
		return
	}
	if !itemReturned {
		t.Fatal("no non-dropped library frame returned after 500 ticks")
	}
}

func TestTickFallsBackWithoutLibraryImages(t *testing.T) {
	source := &catalog.Source{ID: "test-source", Scene: "harbor", Link: "Private"}
	generator := NewGenerator()
	for attempt := 0; attempt < 500; attempt++ {
		item, _ := generator.Tick(source, time.Now(), "maritime", nil)
		if item != nil {
			if item.Img != "" {
				t.Fatalf("fallback item image = %q, want empty", item.Img)
			}
			return
		}
	}
	t.Fatal("no non-dropped fallback frame returned after 500 ticks")
}
