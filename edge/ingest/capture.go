package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"strconv"
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func enrichCapture(it *pb.Item, data []byte) {
	if it.Features == nil {
		it.Features = &pb.Features{}
	}
	f := it.Features
	f.Tags = maps.Clone(f.Tags)
	if f.Tags == nil {
		f.Tags = map[string]string{}
	}
	tags := f.Tags
	synthetic := []string{}
	if tags["synthetic"] != "" {
		synthetic = strings.Split(tags["synthetic"], ",")
	}
	put := func(key, value string, synth bool) {
		if _, ok := tags[key]; !ok {
			tags[key] = value
			if synth {
				synthetic = append(synthetic, key)
			}
		}
	}
	h := sha256.Sum256(append([]byte(it.Source+":"), data...))
	if len(data) == 0 {
		h = sha256.Sum256([]byte(it.Source + ":" + it.Id))
	}
	u := float64(binary.BigEndian.Uint16(h[:2])) / 65535
	if it.Geo == nil {
		it.Geo = &pb.Geo{Lat: 36.95 + u*.01, Lon: -76.05 + float64(h[2])*.0001, HeadingDeg: float64(h[3]) * 360 / 256}
		synthetic = append(synthetic, "geo")
		put("heading", fmt.Sprintf("%.2f", it.Geo.HeadingDeg), true)
	} else {
		put("heading", fmt.Sprintf("%.2f", it.Geo.HeadingDeg), false)
	}
	label := "unclassified contact"
	conf := "0.70"
	classificationSynthetic := true
	if len(f.Detections) > 0 && f.Detections[0].Label != "" {
		classificationSynthetic = false
		label = f.Detections[0].Label
		conf = strconv.FormatFloat(float64(f.Detections[0].Confidence), 'f', 3, 32)
	}
	put("subtype", label, classificationSynthetic)
	put("subtype_conf", conf, classificationSynthetic)
	put("unknownness", strconv.FormatFloat(float64(f.Unknownness), 'f', 3, 32), false)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		put("width_px", strconv.Itoa(cfg.Width), false)
		put("height_px", strconv.Itoa(cfg.Height), false)
	} else {
		put("width_px", "1920", true)
		put("height_px", "1080", true)
	}
	if len(synthetic) > 0 {
		tags["synthetic"] = strings.Join(synthetic, ",")
	}
}
