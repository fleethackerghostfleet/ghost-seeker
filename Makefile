GO ?= go
BIN := bin

.PHONY: all gen build test sim run-shore run-edge compose clean

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

compose:
	docker compose -f deploy/compose/docker-compose.yml up --build

clean:
	rm -rf $(BIN) data sim-results.json
