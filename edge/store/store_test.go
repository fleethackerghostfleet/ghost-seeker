package store

import (
	"context"
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

func TestReplaceFormRelabelsMime(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	it := &pb.Item{Id: "i1", Type: pb.ItemType_ITEM_TYPE_IMAGE, ContentHash: "h"}
	if _, err := st.Put(ctx, it, map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: jpg}); err != nil {
		t.Fatal(err)
	}
	f, err := st.ReplaceForm(ctx, "i1", pb.FormKind_FORM_KIND_FULL, png)
	if err != nil || f.Mime != "image/png" {
		t.Fatalf("replace: %+v %v", f, err)
	}
	row, err := st.Get(ctx, "i1")
	if err != nil || row.Form(pb.FormKind_FORM_KIND_FULL).Mime != "image/png" {
		t.Fatalf("stored mime: %+v %v", row.Form(pb.FormKind_FORM_KIND_FULL), err)
	}
}
