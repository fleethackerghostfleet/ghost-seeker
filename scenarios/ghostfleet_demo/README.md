# ghostfleet_demo

Demo scenario for the hackathon: 2–3 of the team's 5 fps clips replayed through edge + shore.

- `download.sh` — fetch the clips (MOT layout) from the `demo-data-v1` GitHub release (GCS fallback) into `frames/` (gitignored, never commit images).
- `scenario.json` — the replay (`sets` point at `frames/<clip>/img1`; `source` must equal the clip
  directory name so the sim can find `gt/gt.txt`), plus a policy event at 150 s.
- `compare.sh` — headless three-way comparison via `cmd/sim -dedup naive|hash|sightings -bps ...`;
  writes `out/compare.{json,md}`. Latest committed numbers: `RESULTS.md`.
- Live stack: `make demo-shore`, `make demo-ui`, `make demo-edge` (see `docs/DEMO.md`).
