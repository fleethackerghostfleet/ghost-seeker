package retune

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

// Demo creates 300 synthetic reviews, never overwriting an existing log.
func Demo(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := 0; i < 300; i++ {
		r := reviews.Record{ID: fmt.Sprintf("demo-review-%03d", i), ItemID: fmt.Sprintf("demo-item-%03d", i), Reviewer: []string{"Demo Alice", "Demo Bob"}[i%2], Action: "tier", ModelKey: "maritime", Model: catalog.Models["maritime"].Name, HumanTier: 4, ModelTier: 3, ReviewedAt: time.Unix(1700000000+int64(i), 0).UTC(), Detections: []command.Detection{{Class: "debris", Conf: .9}}, Reason: "debris"}
		if err = enc.Encode(r); err != nil {
			return err
		}
	}
	return f.Sync()
}
