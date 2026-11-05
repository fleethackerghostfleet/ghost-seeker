// Package imglimit decodes images only after checking their declared
// dimensions, so a tiny crafted file cannot make a node allocate gigabytes.
package imglimit

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	_ "image/gif"  // register decoders the nodes accept
	_ "image/jpeg" // (sightings are JPEG; PNG/GIF appear in test fixtures)
	_ "image/png"
)

// MaxPixels is the default cap: 50 MP covers 8K stills (33 MP) with margin.
const MaxPixels = 50_000_000

// ErrTooLarge is returned (wrapped) when the header exceeds the cap.
var ErrTooLarge = errors.New("image exceeds pixel cap")

// Check parses only the header and errors if the image is empty or larger
// than maxPixels (0 = MaxPixels).
func Check(data []byte, maxPixels int) (image.Config, string, error) {
	if maxPixels <= 0 {
		maxPixels = MaxPixels
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return cfg, "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return cfg, format, fmt.Errorf("invalid image dimensions %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Width > maxPixels || cfg.Height > maxPixels || cfg.Width*cfg.Height > maxPixels {
		return cfg, format, fmt.Errorf("%w: %dx%d > %d", ErrTooLarge, cfg.Width, cfg.Height, maxPixels)
	}
	return cfg, format, nil
}

// Decode is image.Decode guarded by Check.
func Decode(data []byte, maxPixels int) (image.Image, string, error) {
	if _, _, err := Check(data, maxPixels); err != nil {
		return nil, "", err
	}
	return image.Decode(bytes.NewReader(data))
}
