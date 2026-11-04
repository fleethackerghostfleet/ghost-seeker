# ghost-seeker

## Algorithm baseline

[`algorithm/README.md`](algorithm/README.md) describes the implemented pipeline: interchangeable image providers produce cached evidence, ranking orders the full pool with soft duplicate suppression, and a separate selector applies an absolute payload allowance. Changing bandwidth reuses the same ranking; changing operator policy can rerank evidence without repeating inference. Detection annotations enter evaluation only.

- [`Provider handoff`](algorithm/PROVIDERS.md): object, visual-interest and quality contracts, Python adapters and CLI factories.
- [`Ranking and selection`](algorithm/RANKING_AND_SELECTION.md): base/final scores, stable priority ranks and budget accounting, such as **64 kbps over 60 seconds = 480 KB**.

The built-in providers run on CPU. Custom providers control their own runtime. The current benchmark is offline; live queue management and transmission belong to the platform.

## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.
