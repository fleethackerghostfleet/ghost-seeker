FROM python:3.11-slim
WORKDIR /app
COPY research/nl-priority/vision/THIRD_PARTY_NOTICES.md /app/THIRD_PARTY_NOTICES.md
COPY research/nl-priority/vision/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt
COPY edgeml/src/edgeml/embed/zeroshot.py /app/edgeml/embed/zeroshot.py
COPY research/edgeml/src/edgeml_research/nl_priority /app/edgeml_research/nl_priority
ENV PYTHONPATH=/app PYTHONUNBUFFERED=1 OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=1
EXPOSE 8098
ENTRYPOINT ["python", "-m", "edgeml_research.nl_priority.server"]
CMD ["--models", "/models/mobileclip", "--listen", "0.0.0.0:8098", "--threads", "2"]
