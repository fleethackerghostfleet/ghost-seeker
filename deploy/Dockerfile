# CPU-only static binaries, no CGO (SQLite is pure Go).
# golang:1.26.10-alpine (multi-platform index)
ARG GO_IMAGE=mirror.gcr.io/library/golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696
# gcr.io/distroless/static-debian12:nonroot (multi-platform index)
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# alpine:3.20 (demo-init only)
ARG DEMO_IMAGE=mirror.gcr.io/library/alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
FROM ${GO_IMAGE} AS build
# Production profile by default: demo features are compiled out (AGENTS.md).
# Demo compose files build with GO_TAGS="".
ARG GO_TAGS=prod
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out /data && chmod 0770 /data \
    && CGO_ENABLED=0 go build -tags "${GO_TAGS}" -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM ${RUNTIME_IMAGE} AS runtime
COPY --from=build --chown=65532:65532 /data /data
WORKDIR /data
USER 65532:65532

FROM runtime AS shore
COPY --from=build /out/shore /usr/local/bin/shore
COPY --from=build /src/ui /ui
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/shore"]

FROM runtime AS operator
COPY --from=build /out/ghost-seeker /usr/local/bin/ghost-seeker
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/ghost-seeker"]
# Production images are live-only: point -shore at the shore console and
# provide GS_OPERATOR_TOKEN / -operator-token-file (or -auth-mode piv).
CMD ["-addr", ":8090", "-shore", "http://shore:8080"]

FROM runtime AS edge
COPY --from=build /out/edge /usr/local/bin/edge
COPY --from=build /src/ui /ui
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/edge"]

# Laptop demo only (deploy/compose/demo.compose.yml): a one-shot that writes
# the operator token, the HS256 unit key and the edge JWT into a shared
# volume, so `make compose-demo` needs no secrets from the user. Not part of
# the fielding images above.
FROM ${DEMO_IMAGE} AS demo-init
COPY --from=build /out/mint-unit-token /usr/local/bin/mint-unit-token
COPY deploy/compose/demo-init.sh /usr/local/bin/demo-init
ENTRYPOINT ["/bin/sh", "/usr/local/bin/demo-init"]
