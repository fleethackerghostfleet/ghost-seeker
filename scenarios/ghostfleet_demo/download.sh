#!/usr/bin/env sh
# Fetch the 5 fps demo clips (MOT layout: img1/*.jpg, gt/gt.txt, seqinfo.ini)
# into scenarios/ghostfleet_demo/frames/ (gitignored). Source: the
# `demo-data-v1` GitHub release (deploy/demo-data.sh; `gh auth login` while the
# repo is private), with the team GCS bucket as the fallback (gcloud).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
BUCKET=${BUCKET:-gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps}
DEST=${DEST:-$here/frames}
DEFAULT_CLIPS="Submarine_surfacing Cruise_ship_Mediterranean_sunset_off_Italy Spotted_A_Submarine"
CLIPS=${CLIPS:-$DEFAULT_CLIPS}
missing=""
for c in $CLIPS; do
  if [ -d "$DEST/$c/img1" ] && [ -n "$(ls "$DEST/$c/img1" 2>/dev/null | head -1)" ]; then
    echo "have $c"
  else
    missing="$missing $c"
  fi
done
if [ -n "$missing" ]; then
  gcs_fallback() { for c in $missing; do echo "fetching $c (gcloud)"; gcloud storage cp -r "$BUCKET/$c" "$DEST/" --no-user-output-enabled; done; }
  # The release archive holds exactly the default clips; anything else comes from GCS.
  if [ "$CLIPS" = "$DEFAULT_CLIPS" ] && "$here/../../deploy/demo-data.sh" demo-frames.tar.gz "$DEST" 1; then
    :
  elif command -v gcloud >/dev/null 2>&1; then
    gcs_fallback
  else
    echo "error: missing clips:$missing (release fetch failed or not in the archive; gcloud not installed)" >&2; exit 1
  fi
fi
ls "$DEST"
