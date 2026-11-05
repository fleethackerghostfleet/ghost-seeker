package sensor

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestBackgroundFramesRemainVisibleWithoutInventedContacts(t *testing.T) {
	source := catalog.Source{ID: "background-test", Scene: "open", Mode: "eo"}
	catalog.Library[source.ID] = []catalog.LibraryImage{{File: "imagery/empty-ocean/sec_0001.jpg", Background: true}}
	defer delete(catalog.Library, source.ID)
	g := NewGenerator()
	for range 100 {
		item, _ := g.Tick(&source, time.Now(), "maritime", nil)
		if item == nil || !item.Background || len(item.Dets) != 0 || item.Tier != 4 || item.Central != 0 || item.Edge >= EdgeKeep {
			t.Fatalf("empty water must be retained locally at P4 without contacts: %+v", item)
		}
	}
}
