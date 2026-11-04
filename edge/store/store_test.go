package store

import (
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestMimeFor(t *testing.T) {
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	cases := []struct {
		k    pb.FormKind
		t    pb.ItemType
		data []byte
		want string
	}{
		{pb.FormKind_FORM_KIND_FACT, pb.ItemType_ITEM_TYPE_IMAGE, []byte(`{"id":"x"}`), "application/json"},
		{pb.FormKind_FORM_KIND_THUMB, pb.ItemType_ITEM_TYPE_IMAGE, jpg, "image/jpeg"},
		{pb.FormKind_FORM_KIND_CROP, pb.ItemType_ITEM_TYPE_IMAGE, webp, "image/webp"},
		{pb.FormKind_FORM_KIND_FULL, pb.ItemType_ITEM_TYPE_IMAGE, png, "image/png"},
		{pb.FormKind_FORM_KIND_FULL, pb.ItemType_ITEM_TYPE_IMAGE, make([]byte, 64), "application/octet-stream"},
		{pb.FormKind_FORM_KIND_FULL, pb.ItemType_ITEM_TYPE_TELEMETRY, jpg, "application/octet-stream"},
	}
	for _, c := range cases {
		if got := mimeFor(c.k, c.t, c.data); got != c.want {
			t.Errorf("mimeFor(%v, %v) = %q, want %q", c.k, c.t, got, c.want)
		}
	}
}
