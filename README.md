# ghost-seeker

## Running the app

Run with Go 1.22 or later: `go run ./cmd/ghost-seeker`, then open http://localhost:8080.
Use `-addr`, `-retain`, and `-backfill` for the listen address, item retention, and startup historical images.

- **Live Feed** — browse prioritized sensor imagery, inspect detections, and set priority.
- **Review** — `1`–`4` set priority, Space accepts, X dismisses, C sends to commander, →/S skips, Z undoes, N toggles night mode.
- **Reprioritize** — live iterative workbench: debounced previews, weighted/source/time-scoped editable chips, scored specificity suggestions, image corrections, tier pins, version comparison/revert, Ctrl+Z / Ctrl+Shift+Z. J/K or ←/→ move image focus; ↑/↓/X propose corrections. Apply publishes the draft and pins to all browser sessions. Active directives, the base-model selector, and effective object weights remain on the page.

Preview adds draft rules on top of currently active directives without mutating server state. Apply adds a new active directive; Reset clears only the local draft. The feed continues streaming during iteration. Imagery is synthetic; keyword/regex parsing does not use an LLM. All server state is in memory and resets on restart.

The [standalone design reference](ui-mockup/reprioritize.html) retains 80 seeded synthetic images and simulated apply; open it directly without a server.
