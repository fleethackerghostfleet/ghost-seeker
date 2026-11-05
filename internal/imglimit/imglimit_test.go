package imglimit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// pngHeader declares w x h without carrying pixel data: the image bomb shape.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 2
	var ln [4]byte
	binary.BigEndian.PutUint32(ln[:], 13)
	b.Write(ln[:])
	b.WriteString("IHDR")
	b.Write(ihdr)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	b.Write(crc[:])
	return b.Bytes()
}

func TestCheckRejectsOversize(t *testing.T) {
	bomb := pngHeader(100_000, 100_000)
	if _, _, err := Check(bomb, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("100k x 100k: %v", err)
	}
	if _, _, err := Decode(bomb, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Decode must not touch pixels: %v", err)
	}
	if _, _, err := Check(pngHeader(8000, 6250), 0); err != nil {
		t.Fatalf("50 MP exactly must pass: %v", err)
	}
	if _, _, err := Check(pngHeader(8000, 6251), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("just over: %v", err)
	}
	if _, _, err := Check(pngHeader(1<<31-1, 2), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("overflow-prone dims: %v", err)
	}
	if _, _, err := Check(pngHeader(64, 64), 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("custom cap: %v", err)
	}
	if _, _, err := Check([]byte("not an image"), 0); err == nil || errors.Is(err, ErrTooLarge) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestDecodeSmall(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	img, format, err := Decode(b.Bytes(), 0)
	if err != nil || format != "png" || img.Bounds().Dx() != 4 {
		t.Fatalf("decode: %v %q", err, format)
	}
}
