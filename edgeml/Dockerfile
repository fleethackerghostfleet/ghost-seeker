# python:3.11-slim (multi-platform index)
ARG PY_IMAGE=mirror.gcr.io/library/python@sha256:65bd3d9f1d7fa268dd1909973ba7eb4bdf41f6dcf2cfbe76ba9528b170f3fd79

FROM ${PY_IMAGE} AS builder
ARG WITH_TRACKING=0
WORKDIR /build
COPY edgeml/pyproject.toml ./
COPY edgeml/requirements*.lock ./
COPY edgeml/src ./src
RUN python -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --require-hashes --only-binary=:all: -r requirements-build.lock \
    && if [ "$WITH_TRACKING" = "1" ]; then \
      /opt/venv/bin/pip install --no-cache-dir --require-hashes --only-binary=:all: -r requirements.lock \
      && /opt/venv/bin/pip install --no-cache-dir --require-hashes --no-build-isolation -r requirements-tracking.lock \
      && /opt/venv/bin/pip uninstall -y opencv-python opencv-python-headless \
      && /opt/venv/bin/pip install --no-cache-dir --no-deps --require-hashes --only-binary=:all: -r requirements-headless.lock \
      && rm -rf /opt/venv/lib/python*/site-packages/torch/include; \
    else \
      /opt/venv/bin/pip install --no-cache-dir --require-hashes --only-binary=:all: -r requirements.lock; \
    fi \
    && /opt/venv/bin/pip install --no-cache-dir --no-deps --no-build-isolation . \
    && /opt/venv/bin/python -m pip uninstall --yes setuptools wheel pip

FROM ${PY_IMAGE} AS runtime
ENV PATH="/opt/venv/bin:$PATH" \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    HF_DATASETS_OFFLINE=1 \
    HF_HUB_DISABLE_TELEMETRY=1 \
    DO_NOT_TRACK=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
RUN groupadd --gid 10001 edgeml \
    && useradd --uid 10001 --gid 10001 --create-home --shell /usr/sbin/nologin edgeml \
    && apt-get update \
    && apt-get install --no-install-recommends -y libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && /usr/local/bin/python -m pip uninstall --yes setuptools wheel pip
COPY --from=builder /opt/venv /opt/venv
COPY internal/catalog/colregs_lights.json internal/catalog/colregs_night_config.json /opt/venv/lib/internal/catalog/
WORKDIR /models
USER 10001:10001
EXPOSE 9090
ENTRYPOINT ["python", "-m", "edgeml.sidecar"]
CMD ["--listen", "0.0.0.0:9090", "--mock"]
