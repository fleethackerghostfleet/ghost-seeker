# ghost-seeker

## Algorithm baseline

[`algorithm/README.md`](algorithm/README.md) describes the CPU-only image/video scoring, configurable default prioritization, and offline byte-budget evaluation. Image evidence is cached separately from policy, so operator overrides can rerank observations without repeating inference. Detection annotations enter evaluation only.

[`Ranking and selection`](algorithm/RANKING_AND_SELECTION.md) explains the agreed separation: rank images with duplicate suppression, then select from that list using an absolute bandwidth allowance such as **64 kbps over 60 seconds = 480 KB**. It also identifies where the current combined selector still needs to be separated.

## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.
