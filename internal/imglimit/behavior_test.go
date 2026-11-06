package imglimit

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestPixelCapsWithFullyEncodedImages(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 8, 3))
			img.Set(1, 1, color.RGBA{R: 200, A: 255})
			var b bytes.Buffer
			var err error
			switch format {
			case "png":
				err = png.Encode(&b, img)
			case "jpeg":
				err = jpeg.Encode(&b, img, nil)
			case "gif":
				err = gif.Encode(&b, img, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			control, actual, err := image.Decode(bytes.NewReader(b.Bytes()))
			if err != nil || actual != format || control.Bounds() != img.Bounds() {
				t.Fatalf("invalid control: format=%q err=%v", actual, err)
			}
			for _, cap := range []int{25, 24, 0, -1} {
				cfg, actual, err := Check(b.Bytes(), cap)
				if err != nil || actual != format || cfg.Width != 8 || cfg.Height != 3 {
					t.Fatalf("cap=%d config=%+v format=%q err=%v", cap, cfg, actual, err)
				}
				decoded, actual, err := Decode(b.Bytes(), cap)
				if err != nil || actual != format || decoded.Bounds() != img.Bounds() {
					t.Fatalf("cap=%d decode format=%q err=%v", cap, actual, err)
				}
			}
			if _, _, err := Check(b.Bytes(), 23); !errors.Is(err, ErrTooLarge) {
				t.Fatalf("header cap error=%v", err)
			}
			if decoded, actual, err := Decode(b.Bytes(), 23); !errors.Is(err, ErrTooLarge) || decoded != nil || actual != "" {
				t.Fatalf("decode cap result=%v %q %v", decoded, actual, err)
			}
		})
	}
}

var dimensionFixtureOnce sync.Once

func TestRejectsInvalidDimensionsFromRegisteredDecoder(t *testing.T) {
	const magic = "N1DIM!"
	dimensionFixtureOnce.Do(func() {
		image.RegisterFormat("test-invalid-dimensions", magic,
			func(io.Reader) (image.Image, error) { return nil, errors.New("pixel decoder ran") },
			func(r io.Reader) (image.Config, error) {
				var data [len(magic) + 2]byte
				if _, err := io.ReadFull(r, data[:]); err != nil {
					return image.Config{}, err
				}
				return image.Config{Width: int(data[len(magic)]), Height: int(data[len(magic)+1]), ColorModel: color.RGBAModel}, nil
			})
	})
	for _, dims := range [][2]byte{{0, 1}, {1, 0}, {0, 0}} {
		data := append([]byte(magic), dims[:]...)
		cfg, format, err := Check(data, 10)
		if err == nil || !strings.Contains(err.Error(), "invalid image dimensions") || errors.Is(err, ErrTooLarge) || format != "test-invalid-dimensions" || cfg.Width != int(dims[0]) || cfg.Height != int(dims[1]) {
			t.Fatalf("invalid config=%+v format=%q err=%v", cfg, format, err)
		}
		if _, _, err := Decode(data, 10); err == nil || !strings.Contains(err.Error(), "invalid image dimensions") {
			t.Fatalf("guard did not precede pixel decoding: %v", err)
		}
	}
}

func TestValidHeaderDoesNotHideTruncatedImagePayload(t *testing.T) {
	data := pngHeader(4, 3)
	if cfg, format, err := Check(data, 12); err != nil || format != "png" || cfg.Width != 4 || cfg.Height != 3 {
		t.Fatalf("valid header=%+v %q %v", cfg, format, err)
	}
	if decoded, _, err := Decode(data, 12); err == nil || errors.Is(err, ErrTooLarge) || decoded != nil {
		t.Fatalf("truncated payload=%v %v", decoded, err)
	}
	for _, bad := range [][]byte{nil, {}, []byte("unknown codec")} {
		if _, _, err := Check(bad, 12); err == nil || errors.Is(err, ErrTooLarge) {
			t.Fatalf("bad header=%v", err)
		}
	}
}
