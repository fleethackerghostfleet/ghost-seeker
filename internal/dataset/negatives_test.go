package dataset

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSampleNegative(t *testing.T) {
	picks := map[string]bool{}
	for pass := 0; pass < 3; pass++ {
		count := 0
		for i := 0; i < 10000; i++ {
			id := fmt.Sprint(i)
			if SampleNegative(id, 0) || !SampleNegative(id, 1) {
				t.Fatal("rate endpoints")
			}
			pick := SampleNegative(id, .15)
			if pass > 0 && picks[id] != pick {
				t.Fatal("unstable sample")
			}
			picks[id] = pick
			if pick {
				count++
			}
		}
		if count < 1300 || count > 1700 {
			t.Fatalf("sample count %d", count)
		}
	}
}

func TestSamplingDoesNotChangeReplay(t *testing.T) {
	root := t.TempDir()
	images := []Image{testImage("one", "video_one"), testImage("two", "video_one")}
	for _, im := range images {
		if err := os.WriteFile(filepath.Join(root, im.File), []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewReplay(images, root, Seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewReplay(images, root, Seed)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		left, right := a.Next(time.Time{}), b.Next(time.Time{})
		SampleNegative(left.ImageID, .15)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("sampling changed replay")
		}
	}
}
