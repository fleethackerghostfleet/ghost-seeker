package command

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type CaptureMeta struct {
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	HeadingDeg  float64  `json:"headingDeg"`
	AltM        float64  `json:"altM,omitempty"`
	WidthPx     int      `json:"widthPx"`
	HeightPx    int      `json:"heightPx"`
	ContentHash string   `json:"contentHash"`
	Subtype     string   `json:"subtype"`
	SubtypeConf float64  `json:"subtypeConf"`
	Unknownness float64  `json:"unknownness"`
	TrackID     string   `json:"trackId"`
	TrackFrames int      `json:"trackFrames"`
	FOVDeg      float64  `json:"fovDeg,omitempty"`
	Synthetic   []string `json:"synthetic"`
}

type imageMeta struct {
	hash string
	w, h int
}

var imageCache sync.Map

func imageMetadata(name string) imageMeta {
	if v, ok := imageCache.Load(name); ok {
		return v.(imageMeta)
	}
	m := imageMeta{}
	if name != "" && filepath.IsLocal(name) {
		_, file, _, _ := runtime.Caller(0)
		paths := []string{filepath.Join("web/static/imagery", name), filepath.Join(filepath.Dir(file), "../../web/static/imagery", name)}
		for _, path := range paths {
			if data, err := os.ReadFile(path); err == nil {
				m.hash = fmt.Sprintf("%x", sha256.Sum256(data))
				if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
					m.w, m.h = cfg.Width, cfg.Height
				}
				break
			}
		}
	}
	imageCache.Store(name, m)
	return m
}

// EnsureCapture fills absent capture metadata; existing captures remain authoritative.
func EnsureCapture(it *Item) {
	if it.Capture != nil {
		return
	}
	key := it.SourceID + ":" + it.Img
	if it.Img == "" {
		key += fmt.Sprintf(":%d", it.Seed)
	}
	h := sha256.Sum256([]byte(key))
	drift := math.Sin(float64(it.Frame)*.01) * .0002
	c := &CaptureMeta{Lat: 36.95 + drift, Lon: -76.33 + drift, HeadingDeg: float64(h[0]) * 360 / 256, AltM: 18, WidthPx: 1920, HeightPx: 1080, ContentHash: fmt.Sprintf("%x", h), Subtype: "unclassified contact", SubtypeConf: .65 + float64(h[1])*.001, Unknownness: .15 + float64(h[2])*.001, TrackID: fmt.Sprintf("track-%x", h[:8]), TrackFrames: 1, FOVDeg: 45,
		Synthetic: []string{"lat", "lon", "headingDeg", "altM", "widthPx", "heightPx", "contentHash", "subtype", "subtypeConf", "unknownness", "trackId", "trackFrames", "fovDeg"}}
	switch it.SourceID {
	case "IR-HE":
		c.Lat, c.Lon = 36.96+drift, -76.05+drift
	case "BY-12":
		c.Lat, c.Lon, c.AltM = 36.96+drift, -76.05+30/(60*math.Cos(36.96*math.Pi/180))+drift, 3
	case "UAV-3":
		a := float64(it.Frame) * .01
		c.Lat, c.Lon, c.AltM = 36.98+.02*math.Sin(a), -75.95+.025*math.Cos(a), 450
	case "SKY-1":
		c.Lat, c.Lon = 36.82+drift, -76.03+drift
	case "SAR-7":
		c.Lat, c.Lon, c.AltM = 36.9+drift, -75.8+drift, 550000
	}
	remove := func(names ...string) {
		for _, name := range names {
			for i, s := range c.Synthetic {
				if s == name {
					c.Synthetic = append(c.Synthetic[:i], c.Synthetic[i+1:]...)
					break
				}
			}
		}
	}
	if len(it.Dets) > 0 {
		d := it.Dets[0]
		if d.Class != "" {
			c.Subtype = d.Class
			remove("subtype")
		}
		if d.Ship != nil && d.Ship.Type != "" {
			c.Subtype, c.SubtypeConf = d.Ship.Type, d.Ship.Conf
			remove("subtype", "subtypeConf")
		}
		if d.Class == "unknown" {
			c.Unknownness = .85
		}
	}
	if it.Live != nil {
		c.Unknownness = it.Live.Unknownness
		remove("unknownness")
		if it.Live.Subtype != "" {
			c.Subtype = it.Live.Subtype
			remove("subtype")
		}
	}
	m := imageMetadata(it.Img)
	if m.hash != "" {
		c.ContentHash = m.hash
		remove("contentHash")
	}
	if m.w > 0 {
		c.WidthPx, c.HeightPx = m.w, m.h
		remove("widthPx", "heightPx")
	}
	it.Capture = c
}
