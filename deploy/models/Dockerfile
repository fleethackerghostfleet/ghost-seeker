ARG BUSYBOX_IMAGE=busybox:1.37.0@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
FROM ${BUSYBOX_IMAGE}
LABEL org.opencontainers.image.source="https://github.com/fleethackerghostfleet/ghost-seeker" \
      org.opencontainers.image.version="0.1.0" \
      org.opencontainers.image.description="Offline YOLO11n and MobileCLIP-S1 model set from demo-data-v1" \
      org.opencontainers.image.licenses="AGPL-3.0-only AND LicenseRef-Apple-MobileCLIP" \
      io.ghost-seeker.license-status="UNRESOLVED: Ultralytics AGPL-3.0 vs Enterprise and Apple ML Research model terms must be cleared before fleet release"
COPY --chown=10001:10001 . /models-src/
USER 10001:10001
