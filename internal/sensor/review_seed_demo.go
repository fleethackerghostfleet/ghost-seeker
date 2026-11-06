//go:build !prod

package sensor

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func DemoReviewSeed(now time.Time) command.Item {
	rng := rand.New(rand.NewSource(now.UnixNano()))
	for _, image := range catalog.Library["UAV-3"] {
		for _, det := range image.Dets {
			if det.Cls != "marine mammal" {
				continue
			}
			item := command.Item{ID: fmt.Sprintf("review_%x", now.UnixNano()), SourceID: image.Src,
				Img: image.File, TS: now, ReviewFirst: true, Edge: .9, KB: 1200}
			for _, detection := range image.Dets {
				item.Dets = append(item.Dets, command.Detection{
					Class: detection.Cls,
					Box:   command.Box{X: detection.B.X, Y: detection.B.Y, W: detection.B.W, H: detection.B.H},
					Conf:  math.Round(randBetween(rng, .55, .98)*100) / 100,
					Attrs: makeAttributes(rng, detection.Cls),
					Ship:  detection.Ship,
				})
			}
			command.Rescore(&item, "maritime", nil)
			command.EnsureCapture(&item)
			return item
		}
	}
	panic("demo requires marine mammal imagery")
}
