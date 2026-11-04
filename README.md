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

**Commander's Board** ([http://localhost:8080/cdr](http://localhost:8080/cdr)) is a separate view for the commander's final review. It only shows images the watch floor sent up (`C` / *Send to Commander*), ordered by priority (tier, then command score, then time waiting). For each image it shows the full frame, the contacts, why the image was escalated, and its chain of custody from sensor to watch officer. Decision keys: `1` take action, `2` track, `3` forward to intel, `4` no further action, `B` return to the watch floor (puts the image back in the operator review queue), `/` add a note, `Z` reopen the last decision, `↑`/`↓` move through the list, `Tab` switch between Awaiting and Decided. Decisions are sent with `POST /api/items/{id}/decision` and reach every open view over SSE. For the demo, a simulated watch officer escalates the top unreviewed P1/P2 image about every 15 s; `-auto-cdr 0` turns it off. Escalated images are kept when old items are dropped at the `-retain` limit.

Imagery is synthetic. Directive parsing uses keywords and regular expressions; it does not use an LLM. The app keeps its items and operator state in memory, so they reset when the process stops.
