# ghost-seeker
## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.
- **[Iterative reprioritization workbench](ui-mockup/reprioritize.html)** – standalone live preview on 80 seeded synthetic images: editable rules, scored specificity suggestions, image corrections and tier pins, version comparison/undo, and simulated apply. Open directly in a browser; no server or dependencies. Use ←/→ or J/K to focus images, ↑/↓/X to propose corrections, and Ctrl+Z / Ctrl+Shift+Z to undo/redo.
