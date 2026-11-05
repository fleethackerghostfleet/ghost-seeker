#!/usr/bin/env bash
set -euo pipefail

PROJECT="qwiklabs-gcp-02-34a870014566"
DATASET="ghost_fleet"
BUCKET="gs://qwiklabs-gcp-02-34a870014566-ghost-seeker"
GCLOUD_BIN="/home/ubuntu/google-cloud-sdk/bin"
export PATH="${GCLOUD_BIN}:${PATH}"
BQ="${GCLOUD_BIN}/bq"

if ! "$BQ" show "${PROJECT}:${DATASET}" >/dev/null 2>&1; then
  "$BQ" --location=us-central1 mk --dataset "${PROJECT}:${DATASET}"
fi

"$BQ" query --project_id="$PROJECT" --use_legacy_sql=false <<SQL
CREATE OR REPLACE EXTERNAL TABLE \`${PROJECT}.${DATASET}.datasets\` (
  dataset_id STRING,
  name STRING,
  source_type STRING,
  source_url STRING,
  source_revision STRING,
  license STRING,
  viewpoint STRING,
  modality STRING,
  synthetic BOOL,
  annotation_format STRING,
  native_classes ARRAY<STRING>,
  num_images INT64,
  num_annotations INT64,
  bytes INT64,
  raw_prefix STRING,
  images_prefix STRING,
  ingested_at TIMESTAMP,
  ingest_script STRING,
  notes STRING
)
OPTIONS (
  format = 'NEWLINE_DELIMITED_JSON',
  uris = ['${BUCKET}/catalog/datasets.jsonl']
);

CREATE OR REPLACE EXTERNAL TABLE \`${PROJECT}.${DATASET}.images\` (
  image_id STRING,
  dataset_id STRING,
  gcs_uri STRING,
  split STRING,
  width INT64,
  height INT64,
  bytes INT64,
  sha256 STRING,
  source_ref STRING,
  timestamp_s FLOAT64,
  has_annotations BOOL
)
OPTIONS (
  format = 'NEWLINE_DELIMITED_JSON',
  uris = ['${BUCKET}/catalog/images/*.jsonl']
);

CREATE OR REPLACE EXTERNAL TABLE \`${PROJECT}.${DATASET}.annotations\` (
  image_id STRING,
  dataset_id STRING,
  label STRING,
  label_norm STRING,
  box ARRAY<FLOAT64>,
  conf FLOAT64,
  source STRING,
  attrs STRING
)
OPTIONS (
  format = 'NEWLINE_DELIMITED_JSON',
  uris = ['${BUCKET}/catalog/annotations/*.jsonl']
);
SQL
