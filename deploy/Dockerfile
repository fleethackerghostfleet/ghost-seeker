# CPU-only static binaries, no CGO (SQLite is pure Go).
ARG GO_IMAGE=golang:1.26.10-alpine
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out /data && chmod 0770 /data \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build --chown=65532:65532 /data /data
WORKDIR /data
USER nonroot

FROM runtime AS shore
COPY --from=build /out/shore /usr/local/bin/shore
COPY --from=build /src/ui /ui
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/shore"]

FROM runtime AS operator
COPY --from=build /out/ghost-seeker /usr/local/bin/ghost-seeker
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/ghost-seeker"]
CMD ["-addr", ":8090"]

FROM runtime AS edge
COPY --from=build /out/edge /usr/local/bin/edge
COPY --from=build /src/ui /ui
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/edge"]
