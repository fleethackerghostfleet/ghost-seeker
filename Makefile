GO ?= go
VERIFY_PYTHON ?= python3
BIN := bin

.PHONY: all gen build test sim run-shore run-edge compose compose-demo compose-demo-ship compose-demo-down compose-demo-logs demo-link clean demo demo-clips demo-ship-clips demo-ocean demo-compare demo-shore demo-ui demo-edge demo-models demo-sidecar images images-save images-load package-check zarf-package demo-token demo-key

all: build

.PHONY: verify
verify:         ## all CI checks locally; fails on missing tools or dirty source
	$(VERIFY_PYTHON) tools/verify.py

GO_IMAGE ?=
PY_IMAGE ?=
GO_BUILD_ARG = $(if $(GO_IMAGE),--build-arg GO_IMAGE=$(GO_IMAGE))
PY_BUILD_ARG = $(if $(PY_IMAGE),--build-arg PY_IMAGE=$(PY_IMAGE))
SHORE_IMAGE := ghost-seeker/shore:0.1.0
EDGE_IMAGE := ghost-seeker/edge:0.1.0
OPERATOR_IMAGE := ghost-seeker/operator:0.1.0
EDGEML_IMAGE := ghost-seeker/edgeml-sidecar:0.1.0
IMAGES := $(SHORE_IMAGE) $(EDGE_IMAGE) $(OPERATOR_IMAGE) $(EDGEML_IMAGE)

gen:            ## regenerate Go from proto (needs buf, protoc-gen-go, protoc-gen-connect-go)
	buf lint && buf generate

build:
	mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/ ./cmd/...

test:
	$(GO) vet ./... && $(GO) test ./...

sim: build      ## FIFO vs prioritized on a contested SATCOM trace, 90 simulated minutes
	$(BIN)/sim -trace satcom_contested -duration 90m -out sim-results.json

run-shore: build demo-key
	$(BIN)/shore -data data/shore -listen :8080 -signing-key-file $(DEV_KEY)

run-edge: build demo-key ## replay the frame zips through the mock analyzer, 1 frame / 2 s
	@umask 077 && $(BIN)/mint-unit-token -id $(EDGE_ID) -ttl 24h -signing-key-file $(DEV_KEY) > $(DEV_UNIT_TOKEN)
	$(BIN)/edge -id $(EDGE_ID) -data data/edge -listen :8081 -shore http://localhost:8080 -frames frames -period 2s -unit-token-file $(DEV_UNIT_TOKEN)

DEMO := scenarios/ghostfleet_demo

demo: demo-ship-clips demo-compare ## download the 5 fps clips, run naive vs hash vs sightings vs tomer over the link presets (headless, virtual time)
	@echo; echo "Live demo: 'make demo-sidecar', 'make demo-shore', 'make demo-ui', 'make demo-edge' in four terminals, open http://localhost:8090 (see docs/DEMO.md)"

demo-clips: demo-ship-clips demo-ocean ## prepare all live clips, including the generated ocean watch

demo-ship-clips: ## fetch the three ship clips (gitignored; GitHub release demo-data-v1 via gh, gcloud fallback)
	$(DEMO)/download.sh

demo-ocean:      ## live-only ocean watch: released crops (demo-data-v1, needs only python3), else regenerated from the LFS ZIP (Pillow)
	@$(PY) $(DEMO)/ocean.py --have-cache $(DEMO)/frames || deploy/demo-data.sh demo-ocean.tar.gz $(DEMO)/frames/.ocean-crops 1 || echo "demo-ocean: released crops unavailable; regenerating from the LFS ZIP" >&2
	$(PY) $(DEMO)/ocean.py $(DEMO)/frames

demo-compare: build
	$(DEMO)/compare.sh

# Local demo auth: the first of shore/edge/ghost-seeker to start writes a random
# operator token to data/operator.token (0600); the others read it, and a
# browser on localhost is signed in automatically. The edge<->shore SyncService
# requires a unit JWT by default: demo-key writes a random HS256 key to
# data/unit.key, demo-shore verifies with it and demo-edge mints its own 24 h
# token from it on every start. See docs/SECURITY.md.
DEV_TOKEN ?= data/operator.token
DEV_KEY ?= data/unit.key
DEV_UNIT_TOKEN ?= data/edge.jwt
EDGE_ID ?= edge-1

demo-key:        ## create the local demo HS256 unit signing key shared by demo-shore / demo-edge
	@test -s $(DEV_KEY) || { mkdir -p $(dir $(DEV_KEY)) && umask 077 && head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > $(DEV_KEY); }

demo-token:      ## print the local demo operator token (for curl: -H "Authorization: Bearer $$(make -s demo-token)")
	@test -s $(DEV_TOKEN) || { mkdir -p $(dir $(DEV_TOKEN)) && umask 077 && head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > $(DEV_TOKEN); }
	@cat $(DEV_TOKEN)

demo-shore: build demo-key
	$(BIN)/shore -data data/shore -listen :8080 -dev-token-file $(DEV_TOKEN) -signing-key-file $(DEV_KEY)

demo-ui: build   ## Patrick's operator console (index.html / cdr.html) on live shore data + the shaped link the edge talks through (:18080)
	$(BIN)/ghost-seeker -addr 127.0.0.1:8090 -shore http://localhost:8080 -edge http://localhost:8081 -link-listen :18080 -dev-token-file $(DEV_TOKEN) -demo-accept-any-token

MODELS ?= $(or $(GS_MODELS),$(HOME)/models/D)
PY ?= python3

demo-models:     ## fetch D's ONNX exports (YOLO11n + MobileCLIP-S1) + H's unknown_calib.npz into $(MODELS) (GitHub release demo-data-v1 via gh, gcloud fallback)
	@ok=1; for f in yolo11n-640/model.onnx yolo11n-640/meta.json mobileclip-s1/model.onnx mobileclip-s1/meta.json mobileclip-s1/text_cache.json; do test -s $(MODELS)/$$f || ok=; done; test -n "$$ok" && echo "have models in $(MODELS)" || \
	  deploy/demo-data.sh models-D.tar.gz $(MODELS) 1 sh -c 'mkdir -p $(MODELS)/mobileclip-s1 && gcloud storage cp -r gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/yolo11n-640 $(MODELS)/ && gcloud storage cp gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/meta.json gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/model.onnx gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/text_cache.json gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D/clip/mobileclip-s1/unknown_calib.npz $(MODELS)/mobileclip-s1/'

demo-sidecar:    ## edgeml inference sidecar on :9090 (pip install -e "edgeml[tracking]" onnxruntime first); SIDECAR_PRIORITIZER=edgeml|tomer|both (both also scores with algorithm/ghost_priority)
	$(PY) -m edgeml.sidecar --listen :9090 --detector $(MODELS)/yolo11n-640/model.onnx --embed $(MODELS)/mobileclip-s1 --threads 4 --fps 5 --prioritizer $(or $(SIDECAR_PRIORITIZER),both)

demo-edge: build demo-key demo-ocean ## replay the demo clips at 5 fps through the shaped link, one item per sighting (INFER=mock DEDUP=hash for the stub; PRIORITIZER=tomer DEDUP=hash for Tomer's image-level ranking)
	@umask 077 && $(BIN)/mint-unit-token -id $(EDGE_ID) -ttl 24h -signing-key-file $(DEV_KEY) > $(DEV_UNIT_TOKEN)
	$(BIN)/edge -id $(EDGE_ID) -data data/edge -listen :8081 -shore http://localhost:18080 -scenario $(DEMO)/live.json -period 200ms -infer $(or $(INFER),http://localhost:9090) -dedup $(or $(DEDUP),sightings) -prioritizer $(or $(PRIORITIZER),edgeml) -dev-token-file $(DEV_TOKEN) -unit-token-file $(DEV_UNIT_TOKEN)

compose:
	docker compose -f deploy/compose/docker-compose.yml up --build

# The live demo (sidecar + shore + operator UI + edge) in Docker, for a laptop
# with nothing but Docker Desktop: see deploy/compose/demo.compose.yml and
# docs/DEMO.md. Clips and models are fetched once on the host (GitHub release) and
# mounted read-only; credentials are generated inside the stack.
DEMO_COMPOSE := deploy/compose/demo.compose.yml
DEMO_COMPOSE_CMD = GS_MODELS=$(MODELS) docker compose -f $(DEMO_COMPOSE)

compose-demo: demo-ocean ## build + start the live demo in Docker, then open http://localhost:8090 (MODELS=... to point at the models dir)
	@for p in $$(sed -n 's/.*"path": *"\([^"]*\)".*/\1/p' $(DEMO)/live.json); do test -n "$$(ls $(DEMO)/$$p 2>/dev/null | head -1)" || { echo "error: demo clip $(DEMO)/$$p missing or empty — run 'make demo-clips' first (see docs/DEMO.md)"; exit 1; }; done
	@test -s $(MODELS)/yolo11n-640/model.onnx && test -s $(MODELS)/mobileclip-s1/model.onnx || { echo "error: models missing in $(MODELS) — run 'make demo-models' first (or MODELS=/path/to/D)"; exit 1; }
	$(DEMO_COMPOSE_CMD) up --build -d
	@echo; echo "open http://localhost:8090   (logs: make compose-demo-logs; stop + reset: make compose-demo-down)"
	@echo "ship console: make compose-demo-ship   (prints a signed-in http://localhost:8081 URL; contains the demo operator token)"

compose-demo-ship: ## print the ship console URL (signs in with the demo's generated operator token)
	@echo "ship console: http://localhost:8081/#token=$$(docker run --rm --entrypoint cat -v ghost-seeker-demo_demo-secrets:/s:ro ghost-seeker/demo-init:0.1.0 /s/operator.token)"

demo-link:        ## live demo: pin the ship-to-shore link rate, e.g. make demo-link BPS=32000 (BPS=auto restores the automatic link)
	@case "$(BPS)" in auto) body='{"auto":true}';; ''|*[!0-9]*) echo 'usage: make demo-link BPS=<bits/s>|auto' >&2; exit 2;; *) body='{"bps":$(BPS)}';; esac; \
	curl -fsS -X POST -H 'Content-Type: application/json' -H 'Origin: http://localhost:8090' -d "$$body" http://localhost:8090/api/shore/link; echo

compose-demo-logs:
	$(DEMO_COMPOSE_CMD) logs -f

compose-demo-down: ## stop the Docker demo and delete its data + credentials (next up is a fresh replay)
	$(DEMO_COMPOSE_CMD) down -v --remove-orphans

images:
	docker build $(GO_BUILD_ARG) -f deploy/Dockerfile --target shore -t $(SHORE_IMAGE) .
	docker build $(GO_BUILD_ARG) -f deploy/Dockerfile --target edge -t $(EDGE_IMAGE) .
	docker build $(GO_BUILD_ARG) -f deploy/Dockerfile --target operator -t $(OPERATOR_IMAGE) .
	docker build $(PY_BUILD_ARG) -f edgeml/Dockerfile -t $(EDGEML_IMAGE) edgeml

images-save:
	mkdir -p dist
	docker save -o dist/ghost-seeker-images.tar $(IMAGES)

images-load:
	docker load -i dist/ghost-seeker-images.tar

package-check:
	sh deploy/zarf/check.sh

zarf-package:
	mkdir -p dist
	zarf package create deploy/zarf --confirm --output dist

clean:
	rm -rf $(BIN) data sim-results.json
