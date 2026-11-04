#!/usr/bin/env sh
# Fetch the 5 fps demo clips (MOT layout: img1/*.jpg, gt/gt.txt, seqinfo.ini)
# from the team bucket into scenarios/ghostfleet_demo/frames/ (gitignored).
# Needs `gcloud auth login` as a lab student account (skill fleet-hackathon-gcp).
set -eu
BUCKET=${BUCKET:-gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps}
DEST=${DEST:-$(dirname "$0")/frames}
CLIPS=${CLIPS:-"Submarine_surfacing Cruise_ship_Mediterranean_sunset_off_Italy Spotted_A_Submarine"}
mkdir -p "$DEST"
for c in $CLIPS; do
  if [ -d "$DEST/$c/img1" ] && [ -n "$(ls "$DEST/$c/img1" 2>/dev/null | head -1)" ]; then
    echo "have $c"; continue
  fi
  echo "fetching $c"
  gcloud storage cp -r "$BUCKET/$c" "$DEST/" --no-user-output-enabled
done
ls "$DEST"
