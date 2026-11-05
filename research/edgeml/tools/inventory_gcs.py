#!/usr/bin/env python3
"""Snapshot GCS object metadata (no downloads or writes to the bucket)."""
import argparse
import json
import subprocess
from pathlib import Path

import google.auth
from google.auth.exceptions import DefaultCredentialsError
from google.cloud import storage
from google.oauth2.credentials import Credentials


def credentials():
    try:
        return google.auth.default()[0]
    except DefaultCredentialsError:
        return Credentials(subprocess.check_output(["gcloud", "auth", "print-access-token"], text=True).strip())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True)
    parser.add_argument("--bucket", required=True)
    parser.add_argument("--output", type=Path, required=True, help="New JSONL file")
    args = parser.parse_args()
    client = storage.Client(project=args.project, credentials=credentials())
    count = size = 0
    with args.output.open("x") as stream:
        for blob in client.list_blobs(args.bucket, page_size=1000):
            stream.write(json.dumps({"name": blob.name, "size": blob.size, "generation": blob.generation,
                                     "md5": blob.md5_hash, "crc32c": blob.crc32c,
                                     "updated": blob.updated.isoformat()}) + "\n")
            count += 1
            size += blob.size
    print(json.dumps({"objects": count, "bytes": size}))


if __name__ == "__main__":
    main()
