package shorebridge

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
)

func (c *Console) ensureClass(label string) {
	if label == "" {
		return
	}
	if _, ok := catalog.Classes[label]; ok {
		return
	}
	if _, ok := c.classes[label]; ok {
		return
	}
	c.classes[label] = catalog.Class{Group: guessGroup(label), Label: titleCase(label)}
}

func guessGroup(label string) string {
	switch {
	case strings.Contains(label, "bird"), strings.Contains(label, "buoy"):
		return "clutter"
	case strings.Contains(label, "aircraft"), strings.Contains(label, "helicopter"), strings.Contains(label, "drone"), strings.Contains(label, "plane"):
		return "aircraft"
	case strings.Contains(label, "person"), strings.Contains(label, "people"), strings.Contains(label, "mammal"), strings.Contains(label, "whale"), strings.Contains(label, "dolphin"), strings.Contains(label, "debris"):
		return "other"
	case label == "unknown":
		return "unknown"
	default:
		return "vessel"
	}
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r, size := utf8.DecodeRuneInString(w)
		words[i] = strings.ToUpper(string(r)) + w[size:]
	}
	return strings.Join(words, " ")
}

func (c *Console) ensureSource(id string) string {
	if !ids.ValidName(id) {
		id = "camera"
	}
	if _, ok := c.sources[id]; ok {
		return id
	}
	c.sources[id] = catalog.Source{
		ID: id, Name: strings.ReplaceAll(id, "_", " "), Scene: "open", Mode: "eo",
		Loc: "Ship camera · " + id, HW: "Edge node + inference sidecar", EM: "YOLO11n detector · MobileCLIP zero-shot subtype · sighting tracker",
		Link: "Ship→shore link (shaped)", Rate: "5 fps replay, one item per sighting",
	}
	return id
}

func (c *Console) sourceList() []catalog.Source {
	out := make([]catalog.Source, 0, len(c.sources))
	for _, s := range c.sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// cameras builds the Cameras strip: one entry per source, showing the latest
// object-free heartbeat frame, else the newest contact's image.
func (c *Console) cameras(objs []*Object) []Camera {
	type agg struct {
		cam           Camera
		scene, latest *Object
	}
	cams := map[string]*agg{}
	for _, o := range objs {
		if o.Best == nil {
			continue
		}
		src := c.ensureSource(o.Best.Source)
		a := cams[src]
		if a == nil {
			a = &agg{cam: Camera{Source: src, Name: c.sources[src].Name}}
			cams[src] = a
		}
		if o.LastMs > a.cam.LastMs {
			a.cam.LastMs = o.LastMs
		}
		if o.Scene {
			if a.scene == nil || o.LastMs > a.scene.LastMs {
				a.scene = o
			}
		} else {
			a.cam.Contacts++
			if a.latest == nil || o.LastMs > a.latest.LastMs {
				a.latest = o
			}
		}
	}
	out := make([]Camera, 0, len(cams))
	for _, a := range cams {
		v := a.scene
		a.cam.Heartbeat = v != nil
		if v == nil {
			v = a.latest
		}
		if v != nil {
			b := v.Best
			view := &CameraView{ItemID: b.ID, ImageForm: b.ImageForm, LastMs: v.LastMs, Pull: pullState(b.Forms["full"], a.cam.Heartbeat)}
			if f, ok := b.Forms["full"]; ok {
				view.Full = command.LiveForm{State: f.State, Bytes: f.Bytes, Received: f.Received}
			}
			if b.Image != "" {
				view.Image = fmt.Sprintf("%s?v=%d", b.Image, b.Forms[b.ImageForm].Bytes)
			}
			a.cam.View = view
		}
		out = append(out, a.cam)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}
