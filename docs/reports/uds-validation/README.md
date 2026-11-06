# Two-cluster UDS validation

## Current result: rc2 release artifacts on fresh clusters

[Full rc2 results](rc2/README.md) ·
[Runbook findings and laboratory deviations](rc2/RUNBOOK_FINDINGS.md) ·
[Redacted command transcript](rc2/logs/command-transcript.log)

Runbook: `origin/prod-uds` **71f42d9** (#272). Release: **uds-v0.1.0-rc2**.
All four named amd64 bundles/packages passed published SHA256 checks before
the old clusters were deleted. Fresh shore/ship Core 1.16.0 clusters were
created; **no application images or packages were built locally in this run**.

- Both clusters finished **25/25 Running and Ready**; split Packages Ready.
- Verified gateway TLS/HTTP2: unauthenticated **401**, authenticated **200**.
  Actual SyncService/Exchange access logs show **HTTP/2, grpc_status=OK**.
- Seven live uploads: initial P1 plus six outage frames. Shore count
  **1 → 1 → 7**, edge queue **0 → 6 → 0**, failures **0 → 2 → 0**.
  Recovery acknowledged **1,211,166 bytes**; backlog manifest arrivals were
  **P1, P2, P3, P4, P4, P4**. This is manifest priority, not strict image
  completion order. The 15-second 64-kbps leg completed no exchange; full drain
  occurred at 16 Mbps.
- With ship internet/DNS blocked, app pods restarted, packaged models loaded,
  and another real JPEG scored (boat **0.77753**) and synchronized: **8/8**
  complete. Blocking finished **OFF**, Docker DNS unblocked, unrelated host
  filter rules restored exactly; shaper was left at **16 Mbps**.

Visually verified screenshots:
- [Shore feed](rc2/img/shore-feed.png)
- [P1 vessel detail](rc2/img/shore-item-detail.png)
- [Link/sync status](rc2/img/shore-link-sync-status.png)

The rc2 directory retains checksums, transcript/configs, fresh-cluster identity
and image inventories, API status lines, inputs/acceptances, counter timeline,
SSE/arrival order, offline restart proof and screenshots. Repeated polling
responses, duplicate command output and transient health/readiness files have
been pruned. Original captures remain in local Git history, not the review diff.

## Supporting evidence: earlier local-build offline package reinstall

This is **not rc2 evidence**. The final local build at `f0121e4` (#270), image
tag `prod-f0121e459e61-20261106170117-9abc79bd`, additionally demonstrated actual
ship-package removal and reinstallation while internet/DNS were blocked.
Three application image references were evicted first; deployment events show
successful pulls from the in-cluster registry at **127.0.0.1:31999**, without
external pull failures. Namespace/PVCs were retained in that observed run;
this is not a general retention guarantee. Models loaded and a real JPEG
scored boat **0.80137** and synchronized after reinstall.

Retained audit evidence in [airgap/final/](airgap/final/):
- [Result](airgap/final/result.json),
  [package removal](airgap/final/remove-ship-app.log),
  [package redeploy](airgap/final/redeploy-ship-app.log)
- [Image inventory before redeploy](airgap/final/images-before-redeploy.log),
  [after redeploy](airgap/final/images-after-redeploy.log),
  [pull events](airgap/final/redeployed-events.log)
- [Model checks](airgap/final/redeployed-model-checks.log),
  [received inference item](airgap/final/redeployed-offline-received-item.json)
- [Sustained low-rate result](airgap/final/sustained-low-rate.json),
  [counter timeline](airgap/final/counter-timeline.json)

That supporting run held **64 kbps for 180.9 seconds**, acknowledged **88,504
additional payload bytes** and manifested a new item, but remained **9/10**
complete until 16 Mbps restored full drain. It proves low-rate progress, not
full-image drain within that window. Final firewall status was OFF.

## Superseded runs and limitations

Earlier Core bootstrap, split local deployments, initial priority-selection
attempts and local console captures established the harness. They found the
host inotify limit requirement, the need for narrow gateway routing and Zarf
registry references/pull secrets, and Docker-proxied DNS escaping DOCKER-USER
alone. Their raw logs/screenshots have been removed from the review tree;
their results are superseded by rc2 except for the offline reinstall proof above.

This is laboratory validation, not fleet certification or an inference
accuracy benchmark. Native edge byte counters reset on restart. The shore-only
UI does not expose the ship queue; its “detected objects” label counts records,
not detector entries. Signatures/SBOM provenance were not verified.

Imagery provenance/hashes are retained in rc2 inputs and the supporting offline
input record. Existing SMD onboard imagery is unknown/academic-research and
quarantined per `edgeml/reports/DATA_CLEANUP.md`; maritime timelapse permissions
are unknown. **Do not publicly redistribute derived screenshots until upstream
permissions are resolved.** Protected preview ports were never reused.
