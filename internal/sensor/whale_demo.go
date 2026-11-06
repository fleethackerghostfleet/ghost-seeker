//go:build !prod

package sensor

import (
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

var whaleCount atomic.Uint64

// whaleImages are library frames whose contacts are all marine mammals, so the feed card reads as a whale.
func whaleImages() []catalog.LibraryImage {
	var out []catalog.LibraryImage
	for _, source := range catalog.Sources {
		for _, image := range catalog.Library[source.ID] {
			whale := len(image.Dets) > 0
			for _, det := range image.Dets {
				whale = whale && det.Cls == "marine mammal"
			}
			if whale {
				out = append(out, image)
			}
		}
	}
	return out
}

// WhaleFrame is a demo capture of a whale inbound on own-ship's track (strike-avoidance risk),
// so it ranks near the top of the default priority feed without an operator directive.
func (g *Generator) WhaleFrame(ts time.Time, model string, directives []command.Directive) *command.Item {
	images := whaleImages()
	if len(images) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(ts.UnixNano()))
	image := images[int(whaleCount.Add(1)-1)%len(images)]
	var source *catalog.Source
	for i := range catalog.Sources {
		if catalog.Sources[i].ID == image.Src {
			source = &catalog.Sources[i]
		}
	}
	item, _ := g.Tick(source, ts, model, directives)
	item.Img, item.Background, item.Filtered = image.File, false, ""
	item.Night, item.Basis, item.Lum = nil, "", 0
	item.Dets = item.Dets[:0]
	for _, det := range image.Dets {
		item.Dets = append(item.Dets, command.Detection{
			Class: det.Cls,
			Box:   command.Box{X: det.B.X, Y: det.B.Y, W: det.B.W, H: det.B.H},
			Conf:  math.Round(randBetween(rng, .86, .97)*100) / 100,
			Attrs: command.Attributes{Approaching: true, Moving: true, Dark: true, Loitering: true},
		})
	}
	item.Det = nil
	item.Edge = edgeScore(rng, item.Dets)
	command.Rescore(item, model, directives)
	return item
}
