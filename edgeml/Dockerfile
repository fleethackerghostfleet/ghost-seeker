ARG PY_IMAGE=python:3.11-slim

FROM ${PY_IMAGE} AS builder
ARG WITH_TRACKING=0
WORKDIR /build
COPY pyproject.toml ./
COPY src ./src
RUN python -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir . onnxruntime \
    && if [ "$WITH_TRACKING" = "1" ]; then \
      /opt/venv/bin/pip install --no-cache-dir --index-url https://download.pytorch.org/whl/cpu torch torchvision \
      && /opt/venv/bin/pip install --no-cache-dir '.[tracking]' \
      # boxmot pins the GUI opencv build, whose cv2 needs libGL (absent in slim): keep the headless one at that version
      && cv=$(/opt/venv/bin/pip show opencv-python 2>/dev/null | sed -n 's/^Version: //p') \
      && if [ -n "$cv" ]; then \
        /opt/venv/bin/pip uninstall -y opencv-python opencv-python-headless \
        && /opt/venv/bin/pip install --no-cache-dir --no-deps "opencv-python-headless==$cv"; \
      fi \
      && rm -rf /opt/venv/lib/python*/site-packages/torch/include; \
    fi

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
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /opt/venv /opt/venv
WORKDIR /models
USER 10001:10001
EXPOSE 9090
ENTRYPOINT ["python", "-m", "edgeml.sidecar"]
CMD ["--listen", "0.0.0.0:9090", "--mock"]
