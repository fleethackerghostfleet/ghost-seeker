# Multi-stage: one image, three entrypoints (edge, shore, sim). CPU-only,
# static binaries, no CGO (SQLite is pure Go).
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/ ./cmd/...

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/edge /out/shore /out/sim /usr/local/bin/
COPY --from=build /src/ui /ui
WORKDIR /data
USER nonroot
ENTRYPOINT ["/usr/local/bin/edge"]
