# Reprioritization design reference

**Status: static design mockup, not served by the app.**
[`reprioritize.html`](reprioritize.html) is the standalone reference linked from
the top-level [README](../README.md#operator-web-app). Open it directly to explore
80 seeded synthetic images, directive previews and simulated apply/history;
its "Apply" action changes only the local mockup baseline, not a running service.

The live equivalent is the operator app's Reprioritize workbench in
[`web/static/workbench.js`](../web/static/workbench.js), hosted by
[`cmd/ghost-seeker`](../cmd/ghost-seeker/main.go) with
[`web/static/index.html`](../web/static/index.html). The app embeds `web/static`,
not this directory; `.dockerignore` also excludes `ui-mockup/`. No checked-in
test, CI job, Makefile target or deployment configuration was found that runs
this mockup. Retain it as a design reference, not as an alternative app entrypoint.
