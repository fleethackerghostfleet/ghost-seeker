package sensor

import (
	"fmt"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

type Generator struct {
	frame atomic.Int64
	ids   atomic.Uint64
}

func NewGenerator() *Generator {
	g := &Generator{}
	g.frame.Store(4200)
	return g
}

func (g *Generator) Tick(forceSource *catalog.Source, ts time.Time, model string, directives []command.Directive) (*command.Item, int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	source := forceSource
	if source == nil {
		source = &catalog.Sources[rng.Intn(len(catalog.Sources))]
	}
	extra := int(rng.Float64()*6) + 3
	hz := 0.0
	switch source.Scene {
	case "harbor":
		hz = randBetween(rng, .36, .42)
	case "open":
		hz = randBetween(rng, .34, .46)
	case "sky":
		hz = .9
	}
	var imageFile string
	var detections []command.Detection
	var night *catalog.NightImage
	if images := catalog.NightLibrary[source.ID]; source.Mode == "eo" && len(images) > 0 && rng.Float64() < .2 {
		image := images[rng.Intn(len(images))]
		night = &image
		imageFile = image.File
		detections = command.NightDetections(image)
	} else if images := catalog.Library[source.ID]; len(images) > 0 {
		image := images[rng.Intn(len(images))]
		imageFile = image.File
		detections = make([]command.Detection, 0, len(image.Dets))
		for _, detection := range image.Dets {
			detections = append(detections, command.Detection{
				Class: detection.Cls,
				Box: command.Box{
					X: detection.B.X,
					Y: detection.B.Y,
					W: detection.B.W,
					H: detection.B.H,
				},
				Conf:  math.Round(randBetween(rng, .55, .98)*100) / 100,
				Attrs: makeAttributes(rng, detection.Cls),
			})
		}
	} else {
		n := weightedPick(rng, map[int]float64{0: .6, 1: 4, 2: 3, 3: 1.3})
		detections = make([]command.Detection, 0, n)
		boxes := make([]command.Box, 0, n)
		for i := 0; i < n; i++ {
			class := weightedPick(rng, catalog.Pools[source.Scene])
			box := placeDetection(rng, class, source.Scene, hz, boxes)
			boxes = append(boxes, box)
			detections = append(detections, command.Detection{
				Class: class,
				Box:   box,
				Conf:  math.Round(randBetween(rng, .55, .98)*100) / 100,
				Attrs: makeAttributes(rng, class),
			})
		}
	}
	edge := 0.0
	if len(detections) == 0 {
		edge = randBetween(rng, 0, .05)
	} else {
		edge = edgeScore(rng, detections)
	}
	frame := int(g.frame.Add(int64(rng.Intn(37) + 3)))
	seed := rng.Uint32()
	if edge < EdgeKeep {
		return nil, extra
	}
	up := 0
	switch {
	case contains(source.Link, "Iridium"):
		up = int(math.Round(randBetween(rng, 4000, 9000)))
	case contains(source.Link, "MANET"):
		up = int(math.Round(randBetween(rng, 300, 900)))
	case contains(source.Link, "downlink"):
		up = int(math.Round(randBetween(rng, 60000, 180000)))
	default:
		up = int(math.Round(randBetween(rng, 40, 160)))
	}
	item := &command.Item{
		ID:       fmt.Sprintf("img_%x_%x", time.Now().UnixNano(), g.ids.Add(1)),
		Img:      imageFile,
		Frame:    frame,
		SourceID: source.ID,
		TS:       ts.Truncate(time.Millisecond),
		Seed:     seed,
		HZ:       hz,
		KB:       rng.Intn(28) + 18,
		Edge:     edge,
		Lat: command.Latency{
			Edge: int(math.Round(randBetween(rng, 14, 60))),
			Up:   up,
			Cen:  int(math.Round(randBetween(rng, 70, 210))),
		},
		Dets: detections,
	}
	if night != nil {
		value := night.Night != nil && *night.Night
		item.Night = &value
		item.Basis = night.Basis
		item.Lum = night.Lum
	}
	command.Rescore(item, model, directives)
	return item, extra
}

// EdgeKeep is the on-sensor triage threshold; frames scoring below it are dropped before uplink.
const EdgeKeep = .2

// Attributes draws simulated track attributes for a detection of the given class.
func Attributes(rng *rand.Rand, class string) command.Attributes { return makeAttributes(rng, class) }

// EdgeScore is the low-compute on-sensor triage score for a frame's detections.
func EdgeScore(rng *rand.Rand, detections []command.Detection) float64 {
	return edgeScore(rng, detections)
}

func makeAttributes(rng *rand.Rand, class string) command.Attributes {
	group := catalog.Classes[class].Group
	attrs := command.Attributes{}
	switch {
	case group == "vessel":
		attrs.Moving = rng.Float64() < .5
		attrs.Approaching = attrs.Moving && rng.Float64() < .45
		attrs.Dark = rng.Float64() < .16
		attrs.Loitering = !attrs.Moving && rng.Float64() < .25
	case group == "aircraft" || class == "drone":
		attrs.Moving = true
		attrs.Approaching = rng.Float64() < .35
		attrs.Loitering = class != "civil aircraft" && rng.Float64() < .15
		attrs.Dark = class != "bird" && rng.Float64() < .1
	case class == "person" || class == "debris":
		attrs.Loitering = true
	}
	attrs.New = rng.Float64() < .25
	return attrs
}

func placeDetection(rng *rand.Rand, class, scene string, hz float64, existing []command.Box) command.Box {
	c := catalog.Classes[class]
	for attempt := 0; attempt < 14; attempt++ {
		var box command.Box
		if scene == "aerial" || scene == "sat" {
			length := c.Length * 480 * randBetween(rng, .5, .9)
			if scene == "sat" {
				length *= .6
			}
			breadth := length * math.Min(.32, c.Aspect*.5)
			angle := randBetween(rng, 0, math.Pi)
			cx := randBetween(rng, length/2+6, 480-length/2-6)
			cy := randBetween(rng, length/2*math.Abs(math.Sin(angle))+8, 300-length/2*math.Abs(math.Sin(angle))-8)
			width := math.Abs(length*math.Cos(angle)) + math.Abs(breadth*math.Sin(angle))
			height := math.Abs(length*math.Sin(angle)) + math.Abs(breadth*math.Cos(angle))
			box = command.Box{X: cx - width/2, Y: cy - height/2, W: width, H: height, CX: cx, CY: cy, L: length, B: breadth, A: angle, Top: true}
		} else if catalog.Air[class] {
			length := c.Length * 480 * randBetween(rng, .7, 1.25)
			if scene == "sky" {
				length *= 1.5
			}
			width, height := length, length*c.Aspect
			top, bottom := 10.0, hz*300-12-height
			box = command.Box{X: randBetween(rng, 8, 480-width-8), Y: randBetween(rng, top, math.Max(top+1, bottom)), W: width, H: height}
		} else {
			yb := randBetween(rng, hz+.05, .97)
			scale := (yb - hz) / (1 - hz)
			length := c.Length * 480 * (.35 + scale) * randBetween(rng, .85, 1.15)
			width, height := length, length*c.Aspect
			box = command.Box{X: randBetween(rng, 4, 480-width-4), Y: yb*300 - height, W: width, H: height}
		}
		if boxesDoNotOverlap(box, existing) || attempt == 13 {
			return box
		}
	}
	return command.Box{}
}

func boxesDoNotOverlap(box command.Box, existing []command.Box) bool {
	for _, other := range existing {
		if !(box.X+box.W < other.X || other.X+other.W < box.X || box.Y+box.H < other.Y || other.Y+other.H < box.Y) {
			return false
		}
	}
	return true
}

func edgeScore(rng *rand.Rand, detections []command.Detection) float64 {
	remaining := 1.0
	for _, detection := range detections {
		weight := catalog.EdgeW[catalog.Classes[detection.Class].Group]
		remaining *= 1 - weight*detection.Conf*randBetween(rng, .75, 1)
	}
	return 1 - remaining
}

func weightedPick[T comparable](rng *rand.Rand, weights map[T]float64) T {
	total := 0.0
	for _, weight := range weights {
		total += weight
	}
	selected := rng.Float64() * total
	for value, weight := range weights {
		selected -= weight
		if selected <= 0 {
			return value
		}
	}
	for value := range weights {
		return value
	}
	var zero T
	return zero
}

func randBetween(rng *rand.Rand, min, max float64) float64 {
	return min + rng.Float64()*(max-min)
}

func contains(value, substring string) bool {
	return len(value) >= len(substring) && index(value, substring) >= 0
}

func index(value, substring string) int {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return i
		}
	}
	return -1
}
