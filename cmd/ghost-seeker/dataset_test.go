//go:build !prod

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/dataset"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/server"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func datasetReplay(t *testing.T, negative bool) *dataset.Replay {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.jpg"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("image"))
	image := dataset.Image{ImageID: "one", DatasetID: "video_one", EvalSplit: "dev", SourceVideo: "clip", SHA256: hex.EncodeToString(digest[:]), File: "one.jpg", Width: 100, Height: 100}
	if !negative {
		image.Detections = []dataset.Detection{{Class: "person", Conf: .99, Box: command.Box{W: 50, H: 50}}}
	}
	replay, err := dataset.NewReplay([]dataset.Image{image}, root, dataset.Seed)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

func TestDatasetTickTransfersAndPublishes(t *testing.T) {
	for _, negative := range []bool{false, true} {
		t.Run(fmt.Sprint(negative), func(t *testing.T) {
			data := store.New(20)
			uplink := newSimulationUplink(data, 8000, func() float64 { t.Fatal("fixed rate sampled"); return 0 })
			data.Add(&command.Item{ID: "pending", KB: 2}, 0)
			app := server.New(data)
			httpServer := httptest.NewServer(app)
			defer httpServer.Close()
			client := httpServer.Client()
			client.Timeout = 5 * time.Second
			response, err := client.Get(httpServer.URL + "/api/stream")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			readEvent := func() (string, string) {
				t.Helper()
				var name, payload string
				for scanner.Scan() {
					line := scanner.Text()
					if line == "" && name != "" {
						return name, payload
					}
					if strings.HasPrefix(line, "event: ") {
						name = strings.TrimPrefix(line, "event: ")
					}
					if strings.HasPrefix(line, "data: ") {
						payload = strings.TrimPrefix(line, "data: ")
					}
				}
				t.Fatalf("stream ended: %v", scanner.Err())
				return "", ""
			}
			if name, _ := readEvent(); name != "snapshot" {
				t.Fatalf("first event = %q", name)
			}
			replay := datasetReplay(t, negative)
			for tick, elapsed := range []time.Duration{time.Second, 10 * time.Second} {
				tickDatasetSimulation(data, replay, app, uplink, 0, time.Unix(int64(tick), 0), elapsed)
				wantSent := int64(1000)
				if tick == 1 {
					wantSent = 2560
				}
				found := false
				for {
					name, payload := readEvent()
					if name == "stats" {
						break
					}
					var update struct {
						Item command.Item `json:"item"`
					}
					if err := json.Unmarshal([]byte(payload), &update); err != nil {
						t.Fatal(err)
					}
					if name != "update" {
						t.Fatalf("expected upload update, got %s", name)
					}
					if update.Item.ID == "pending" {
						found = true
						if update.Item.Upload.Sent != wantSent || update.Item.Upload.Done != (tick == 1) {
							t.Fatalf("unexpected progress: %s", payload)
						}
					}
				}
				if !found {
					t.Fatal("upload stats published without pending item update")
				}
				// Completion may also update the first generated dataset item.
				for {
					name, _ := readEvent()
					if name == "item" || name == "stats" {
						if negative && name != "stats" || !negative && name != "item" {
							t.Fatalf("generation event = %q, negative=%v", name, negative)
						}
						break
					}
				}
			}
			items, stats, _, _ := data.Snapshot()
			if negative && (len(items) != 1 || stats.Drop != 2) {
				t.Fatalf("negative sampling changed: items=%d stats=%+v", len(items), stats)
			}
		})
	}
}

func TestDatasetTickCapturesDespitePendingUploads(t *testing.T) {
	for _, rate := range []int64{-1, 8000, 0} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			data, baseline := store.New(40), store.New(40)
			uplink := newSimulationUplink(data, rate, func() float64 { return 0 })
			oldUplink := newSimulationUplink(baseline, rate, func() float64 { return 0 })
			for i := 0; i < 6; i++ {
				item := command.Item{ID: fmt.Sprint(i), KB: 100000}
				data.Add(&item, 0)
				baseline.Add(&item, 0)
			}
			app := server.New(data)
			replay := datasetReplay(t, false)
			total := time.Duration(0)
			for tick := 0; tick < 20; tick++ {
				elapsed := 700 * time.Millisecond
				total += elapsed
				tickDatasetSimulation(data, replay, app, uplink, 1, time.Unix(0, 0).Add(total), elapsed)
				oldUplink.advance(elapsed)
			}
			items, stats, _, _ := data.Snapshot()
			if len(items) != 26 || stats.Cap != 26 {
				t.Fatalf("capture blocked: items=%d stats=%+v", len(items), stats)
			}
			sent := func(s *store.Store) int64 {
				items, _, _, _ := s.Snapshot()
				var bytes int64
				for _, item := range items {
					bytes += item.Upload.Sent
				}
				return bytes
			}
			bps := rate
			if bps < 0 {
				bps = int64(uplink.distribution.MeanBps)
			}
			want := bps * int64(total/time.Millisecond) / 8000
			if sent(data) != sent(baseline) || sent(data) != want {
				t.Fatalf("bytes: captures=%d baseline=%d want=%d", sent(data), sent(baseline), want)
			}
		})
	}
}

func TestDatasetHistoricalBackfill(t *testing.T) {
	data := store.New(260)
	fillDatasetBacklog(data, datasetReplay(t, false), 200, 1)
	data.AdvanceUploads(24 * time.Hour)
	items, _, _, _ := data.Snapshot()
	if len(items) != 200 {
		t.Fatal("historical backlog blocks generation")
	}
	for _, item := range items {
		if !item.Upload.Done {
			t.Fatalf("historical upload not completed: %+v", item.Upload)
		}
	}
}
