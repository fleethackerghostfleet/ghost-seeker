//go:build !prod

package sensor

import (
	"fmt"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func DemoReviewSeed(now time.Time) command.Item {
	for _, image := range catalog.Library["UAV-3"] {
		for _, det := range image.Dets {
			if det.Cls != "marine mammal" {
				continue
			}
			item := command.Item{ID: fmt.Sprintf("review_%x", now.UnixNano()), SourceID: image.Src,
				Img: image.File, TS: now, ReviewFirst: true, Edge: .9, KB: 1200,
				Dets: []command.Detection{{Class: det.Cls, Conf: .95,
					Box: command.Box{X: det.B.X, Y: det.B.Y, W: det.B.W, H: det.B.H}}}}
			command.Rescore(&item, "maritime", nil)
			command.EnsureCapture(&item)
			return item
		}
	}
	panic("demo requires marine mammal imagery")
}
