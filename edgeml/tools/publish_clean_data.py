#!/usr/bin/env python3
"""Publish an audit to a NEW GCS clean prefix and NEW native BigQuery tables.

Original catalogs/data are never edited. A failed/partial publication requires a
different prefix/table suffix; this command does not offer overwrite or replace.
"""
import argparse
import concurrent.futures
import hashlib
import json
import re
from pathlib import Path

from google.cloud import bigquery, storage

from inventory_gcs import credentials


def destinations(bucket, prefix, suffix):
    prefix = prefix.strip("/") + "/"
    if not prefix.startswith("clean/") or any(p in {"", ".", ".."} for p in prefix.rstrip("/").split("/")):
        raise ValueError("Publication prefix must be a new clean/<version>/... prefix")
    if not re.fullmatch(r"[a-zA-Z0-9_]+", suffix):
        raise ValueError("Table suffix must contain only letters, digits and underscores")
    return f"gs://{bucket}/{prefix}", prefix


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--project", required=True)
    parser.add_argument("--bucket", required=True)
    parser.add_argument("--prefix", required=True)
    parser.add_argument("--dataset", default="ghost_fleet")
    parser.add_argument("--table-suffix", default="unused", help="Tables become images_clean_<suffix>, etc.")
    parser.add_argument("--artifacts-only", action="store_true", help="Publish supplemental audit files without BigQuery tables")
    parser.add_argument("--receipt", type=Path, required=True, help="New local JSON receipt")
    args = parser.parse_args()
    uri, prefix = destinations(args.bucket, args.prefix, args.table_suffix)
    auth = credentials()
    gcs = storage.Client(project=args.project, credentials=auth)
    bq = bigquery.Client(project=args.project, credentials=auth)
    bucket = gcs.bucket(args.bucket)
    if args.receipt.exists():
        parser.error("Receipt already exists")
    if next(iter(gcs.list_blobs(args.bucket, prefix=prefix, max_results=1)), None):
        parser.error("GCS destination is not empty; choose another prefix")
    tables = {}
    for name in (() if args.artifacts_only else ("images", "annotations", "datasets")):
        dest = f"{args.project}.{args.dataset}.{name}_clean_{args.table_suffix}"
        from google.api_core.exceptions import NotFound
        try:
            bq.get_table(dest)
        except NotFound:
            pass
        else:
            parser.error(f"BigQuery destination exists: {dest}")
        schema = list(bq.get_table(f"{args.project}.{args.dataset}.{name}").schema)
        if name == "images":
            schema += [bigquery.SchemaField("source_video", "STRING"), bigquery.SchemaField("eval_split", "STRING")]
        tables[name] = (dest, schema)
    files = sorted(p for p in args.input.rglob("*") if p.is_file())
    if not (args.input / "audit.json").exists() or (not args.artifacts_only and not (args.input / "catalog/datasets.jsonl").exists()):
        parser.error("Input must contain a completed audit and clean catalog")

    def upload(path):
        relative = path.relative_to(args.input).as_posix()
        data = path.read_bytes()
        if path.suffix in {".json", ".jsonl"}:
            data = data.replace(b"CLEAN_PREFIX/", uri.encode())
        blob = bucket.blob(prefix + relative)
        blob.upload_from_string(data, if_generation_match=0, timeout=180)
        return {"uri": uri + relative, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                "generation": blob.generation}

    uploads = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
        for i, record in enumerate(pool.map(upload, files), 1):
            uploads.append(record)
            if i % 2000 == 0:
                print(f"Uploaded {i}/{len(files)}", flush=True)
    receipt = {"prefix": uri, "objects": len(uploads), "bytes": sum(r["bytes"] for r in uploads),
               "uploads": uploads, "tables": {}}
    for name, (dest, schema) in tables.items():
        bq.create_table(bigquery.Table(dest, schema=schema))  # fails if another writer won the name
        config = bigquery.LoadJobConfig(schema=schema, source_format=bigquery.SourceFormat.NEWLINE_DELIMITED_JSON,
                                       write_disposition=bigquery.WriteDisposition.WRITE_EMPTY,
                                       create_disposition=bigquery.CreateDisposition.CREATE_NEVER)
        source = uri + (f"catalog/{name}/*.jsonl" if name != "datasets" else "catalog/datasets.jsonl")
        job = bq.load_table_from_uri(source, dest, job_config=config)
        job.result()
        table = bq.get_table(dest)
        receipt["tables"][name] = {"table": dest, "rows": table.num_rows, "job_id": job.job_id}
        print(dest, table.num_rows, flush=True)
    encoded = json.dumps(receipt, indent=2)
    with args.receipt.open("x") as stream:
        stream.write(encoded)
    bucket.blob(prefix + "publication.json").upload_from_string(encoded, if_generation_match=0)
    print("Published:", uri)


if __name__ == "__main__":
    main()
