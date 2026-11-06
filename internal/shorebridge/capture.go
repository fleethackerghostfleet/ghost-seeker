package shorebridge

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func populateCapture(item *command.Item, o *Object) {
	base := *item
	base.Live, base.Dets = nil, nil
	command.EnsureCapture(&base)
	item.Capture = base.Capture
	c := item.Capture
	b := o.Best
	tags := b.Tags
	synthetic := strings.Split(tags["synthetic"], ",")
	mark := func(field, key string) {
		if slices.Contains(synthetic, key) {
			return
		}
		c.Synthetic = slices.DeleteFunc(c.Synthetic, func(s string) bool { return s == field })
	}
	if geo := b.item.GetGeo(); geo != nil {
		c.Lat, c.Lon, c.HeadingDeg = geo.Lat, geo.Lon, geo.HeadingDeg
		mark("lat", "geo")
		mark("lon", "geo")
		mark("headingDeg", "heading")
	}
	for _, field := range []struct {
		key, name string
		dst       *float64
	}{
		{"heading", "headingDeg", &c.HeadingDeg}, {"subtype_conf", "subtypeConf", &c.SubtypeConf}, {"unknownness", "unknownness", &c.Unknownness},
	} {
		if value, err := strconv.ParseFloat(tags[field.key], 64); err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
			*field.dst = value
			mark(field.name, field.key)
		}
	}
	for _, field := range []struct {
		key, name string
		dst       *int
	}{
		{"width_px", "widthPx", &c.WidthPx}, {"height_px", "heightPx", &c.HeightPx}, {"frames", "trackFrames", &c.TrackFrames},
	} {
		if value, err := strconv.Atoi(tags[field.key]); err == nil && value > 0 {
			*field.dst = value
			mark(field.name, field.key)
		}
	}
	if subtype := tags["subtype"]; subtype != "" {
		c.Subtype = subtype
		mark("subtype", "subtype")
	}
	if id := tags["sighting_id"]; id != "" {
		c.TrackID = id
		mark("trackId", "sighting_id")
	}
	if o.FramesKnown && o.Frames > 0 {
		c.TrackFrames = o.Frames
		mark("trackFrames", "frames")
	}
	if hash := b.item.GetContentHash(); hash != "" {
		c.ContentHash = hash
		mark("contentHash", "content_hash")
	}
}
