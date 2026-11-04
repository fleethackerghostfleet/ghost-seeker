package ingest

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"

	"golang.org/x/image/draw"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// Image forms are JPEG. Lossy WebP would save roughly another quarter of the
// bytes, but Go has no pure-Go lossy WebP encoder (x/image/webp only
// decodes) and the edge is built without CGO. The store sniffs each form's
// bytes for its Mime, so a WebP encoder can be dropped in here later
// without touching the scheduler, link or shore.

// thumbForm downscales img to fit within ThumbWidth × ThumbHeight (never
// upscaling) and encodes it at ThumbQual.
func (g *Ingester) thumbForm(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := fit(b.Dx(), b.Dy(), g.cfg.ThumbWidth, g.cfg.ThumbHeight)
	var dst draw.Image
	if _, gray := img.(*image.Gray); gray {
		dst = image.NewGray(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	// CatmullRom avoids the aliasing ApproxBiLinear leaves on large
	// downscales; aliasing is high-frequency noise that JPEG pays for.
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return encodeJPEG(dst, g.cfg.ThumbQual)
}

// fit scales (w, h) down to fit within (maxW, maxH), keeping aspect ratio.
// A zero bound is unconstrained.
func fit(w, h, maxW, maxH int) (int, int) {
	w, h = max(w, 1), max(h, 1)
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && float64(h)*scale > float64(maxH) {
		scale = float64(maxH) / float64(h)
	}
	return max(int(float64(w)*scale+0.5), 1), max(int(float64(h)*scale+0.5), 1)
}

// cropForm encodes the union of the detection boxes, padded by CropPad of
// the union's size on each side, at native resolution. It returns nil when
// there is nothing to crop to or the crop would cover most of the frame
// (then FULL is the next useful rung and CROP would only duplicate bytes).
func (g *Ingester) cropForm(img image.Image, dets []*pb.Detection) ([]byte, error) {
	r, ok := cropRect(img.Bounds(), dets, g.cfg.CropPad, g.cfg.CropMinConf)
	if !ok {
		return nil, nil
	}
	b := img.Bounds()
	if float64(r.Dx()*r.Dy()) > g.cfg.CropMaxArea*float64(b.Dx()*b.Dy()) {
		return nil, nil
	}
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Copy(dst, image.Point{}, img, r, draw.Src, nil)
		return encodeJPEG(dst, g.cfg.CropQual)
	}
	return encodeJPEG(sub.SubImage(r), g.cfg.CropQual)
}

// cropRect returns the pixel rectangle covering every detection box with
// confidence >= minConf, padded by pad × the union's width/height on each
// side and clamped to bounds. Boxes are normalized (x, y, w, h).
func cropRect(bounds image.Rectangle, dets []*pb.Detection, pad, minConf float64) (image.Rectangle, bool) {
	var x0, y0, x1, y1 float64
	found := false
	for _, d := range dets {
		bx := d.GetBox()
		if bx == nil || bx.W <= 0 || bx.H <= 0 || float64(d.Confidence) < minConf {
			continue
		}
		l, t, r, btm := float64(bx.X), float64(bx.Y), float64(bx.X+bx.W), float64(bx.Y+bx.H)
		if !found {
			x0, y0, x1, y1, found = l, t, r, btm, true
			continue
		}
		x0, y0, x1, y1 = min(x0, l), min(y0, t), max(x1, r), max(y1, btm)
	}
	if !found {
		return image.Rectangle{}, false
	}
	px, py := (x1-x0)*pad, (y1-y0)*pad
	W, H := float64(bounds.Dx()), float64(bounds.Dy())
	rect := image.Rect(
		bounds.Min.X+int((x0-px)*W), bounds.Min.Y+int((y0-py)*H),
		bounds.Min.X+int((x1+px)*W+0.999), bounds.Min.Y+int((y1+py)*H+0.999),
	).Intersect(bounds)
	if rect.Empty() {
		return image.Rectangle{}, false
	}
	return rect, true
}

// fullForm keeps the capture's own bytes when they are already a lossy
// format (re-encoding a JPEG only adds generation loss), and re-encodes
// anything else (PNG, BMP, ...) as JPEG at FullQual when that is smaller.
// FullQual <= 0 always keeps the original bytes, as do captures with
// transparency (JPEG has no alpha channel).
func (g *Ingester) fullForm(data []byte, img image.Image) []byte {
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/webp":
		return data
	}
	if g.cfg.FullQual <= 0 || img == nil {
		return data
	}
	if o, ok := img.(interface{ Opaque() bool }); ok && !o.Opaque() {
		return data
	}
	enc, err := encodeJPEG(img, g.cfg.FullQual)
	if err != nil || len(enc) >= len(data) {
		return data
	}
	return enc
}

func encodeJPEG(img image.Image, q int) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
