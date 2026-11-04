# ghost-seeker

## Running the app

Run the in-memory Go web app with Go 1.22 or later:

```sh
go run ./cmd/ghost-seeker
```

Open [http://localhost:8080](http://localhost:8080). Use `-addr`, `-retain`, and `-backfill` to change the listen address, item retention, or number of historical items generated at startup.

The app has three pages:

- **Live Feed** — browse prioritized sensor imagery, open an item for detections and processing details, and set its priority.
- **Review** — work through items using `1`–`4` to set priority, `Space` to accept the model suggestion, `X` to dismiss, `C` to send to the commander queue, `→` or `S` to skip, and `Z` to undo. `N` toggles night mode.
- **Reprioritize** — choose a command model and add or remove natural-language directives.

Imagery is synthetic. Directive parsing uses keywords and regular expressions; it does not use an LLM. The app keeps its items and operator state in memory, so they reset when the process stops.
