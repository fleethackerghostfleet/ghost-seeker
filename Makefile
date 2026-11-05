GO ?= go
BIN := bin

.PHONY: all gen build test sim run-shore run-edge compose clean demo demo-clips demo-compare demo-shore demo-ui demo-edge demo-models demo-sidecar

all: build

gen:            ## regenerate Go from proto (needs buf, protoc-gen-go, protoc-gen-connect-go)
	buf lint && buf generate

build:
	$(GO) build -o $(BIN)/ ./cmd/...

test:
	$(GO) vet ./... && $(GO) test ./...

sim: build      ## FIFO vs prioritized on a contested SATCOM trace, 90 simulated minutes
	$(BIN)/sim -trace satcom_contested -duration 90m -out sim-results.json

run-shore: build
	$(BIN)/shore -data data/shore -listen :8080

run-edge: build ## replay the frame zips through the mock analyzer, 1 frame / 2 s
	$(BIN)/edge -data data/edge -listen :8081 -shore http://localhost:8080 -frames frames -period 2s

DEMO := scenarios/ghostfleet_demo

demo: demo-clips demo-compare ## download the 5 fps clips, run naive vs hash vs sightings over the link presets (headless, virtual time)
	@echo; echo "Live demo: 'make demo-sidecar', 'make demo-shore', 'make demo-ui', 'make demo-edge' in four terminals, open http://localhost:8090 (see docs/DEMO.md)"

demo-clips:      ## fetch the demo clips from GCS into $(DEMO)/frames (gitignored; needs gcloud auth)
	$(DEMO)/download.sh

demo-compare: build
	$(DEMO)/compare.sh

demo-shore: build
	$(BIN)/shore -data data/shore -listen :8080

demo-ui: build   ## operator UI on live shore data + the shaped link the edge talks through (:18080)
	$(BIN)/ghost-seeker -addr :8090 -shore http://localhost:8080 -edge http://localhost:8081 -link-listen :18080

MODELS ?= $(HOME)/models/D
DET_MODEL ?= yolo11n-ft-640-v1   # workstream G fine-tune (edgeml/reports/G_detector.md); D's COCO export: yolo11n-640
DET_GCS   ?= gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/G/$(DET_MODEL)
PY ?= python

demo-models:     ## fetch the sidecar models: G's fine-tuned YOLO11n ($(DET_MODEL)) + D's MobileCLIP-S1
	mkdir -p $(MODELS)/mobileclip-s1
	gcloud storage cp -r $(DET_GCS) $(MODELS)/
	gcloud storage cp gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/meta.json gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/model.onnx gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/text_cache.json $(MODELS)/mobileclip-s1/

demo-sidecar:    ## edgeml inference sidecar on :9090 (pip install -e "edgeml[tracking]" onnxruntime first)
	$(PY) -m edgeml.sidecar --listen :9090 --detector $(MODELS)/$(DET_MODEL)/model.onnx --embed $(MODELS)/mobileclip-s1 --threads 4 --fps 5

demo-edge: build ## replay the demo clips at 5 fps through the shaped link, one item per sighting (INFER=mock DEDUP=hash for the stub)
	$(BIN)/edge -data data/edge -listen :8081 -shore http://localhost:18080 -scenario $(DEMO)/scenario.json -period 200ms -infer $(or $(INFER),http://localhost:9090) -dedup $(or $(DEDUP),sightings)

compose:
	docker compose -f deploy/compose/docker-compose.yml up --build

clean:
	rm -rf $(BIN) data sim-results.json
