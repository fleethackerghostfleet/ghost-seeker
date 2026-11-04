# ghost-seeker

## Algorithm baseline

[`algorithm/README.md`](algorithm/README.md) describes the CPU-only image/video scoring, configurable default prioritization, and offline byte-budget evaluation. Image evidence is cached separately from policy, so operator overrides can rerank observations without repeating inference. Detection annotations enter evaluation only.

## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.
