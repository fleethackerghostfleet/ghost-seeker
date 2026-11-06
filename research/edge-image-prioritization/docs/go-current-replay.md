> These reviewed notes describe the private experiment archive. This Git package contains portable code and condensed evidence; raw run directories mentioned below are intentionally external. See the package README for current commands and file mappings.

# Separate replay at current main

The materially changed scheduler/store/scoring code justified a second isolated replay at **`1a6382ed554c63466c6fc34fafa1bbf0bb70b659`**. The historical snapshot/results are unchanged. The exact current revision was fetched through GitHub's authenticated tarball API into `current-snapshot/`; 140 required Go/module/support files have recorded SHA256 values. No source checkout, shared branch or production file was modified.

The same Go 1.23.4 toolchain and task-local caches are compatible with the current `go.mod`. The identical four-case research harness compiled against the new APIs **without adaptation**. All six encoded JPEG fixtures and two generated FACTs retain the same 161,754 unique application bytes. Current results use the actual current edge store, scheduler, virtual link, shore service and shore store.

## Results from the four identical fixtures

| Case | Current charged wire bytes | Current virtual drain time | JPEG forms received by 60 s |
|---|---:|---:|---:|
| 9.6 kbps, constant | 180,794 | 169.262 s | 4/6 |
| 9.6 kbps, 2–12 s outage | 188,324 | 199.037 s | 3/6 |
| 64 kbps, constant | 165,423 | 23.678 s | 6/6 |
| 64 kbps, 2–12 s outage | 207,350 | 52.419 s | 6/6 |

All eight application forms eventually reached shore and their bytes matched. Constant-link wire/timing outcomes match the historical fixture. Outage runs use **137 additional bytes at 9.6 kbps** and **273 at 64 kbps** relative to the historical version, with roughly 0.114 s and 0.034 s extra virtual time. They retain the same complete-form counts at 60 s. These small outcomes do not establish equivalence for other policies or inputs.

The link simulator file itself is unchanged, but `edge/link/link.go` now decays its estimated rate further during repeated failures. The scheduler, store and scoring changes were also present. This replay does not isolate a single source-code change as the cause of the byte difference.

## The current scene branch is pull-only for FULL

A separate branch-control test uses the first real image's THUMB/FULL bytes with **synthetic `event=scene` metadata and no detections**. This deliberately tests transport policy, not whether that image is empty or whether a detector should classify it as a scene. The current default scheduler sends FACT and THUMB, while FULL remains entirely unsent through **61.23 s** of virtual observation.

The test then calls the actual `Scheduler.Pull` API with `Form=FULL` and an explicit requester. FULL reaches shore at **68.728125 s**, and its complete ACK reaches the edge at **69.043250 s**; all **51,872 payload bytes** match the source. Thus ordinary image fixtures still drain FULL automatically, while the explicitly tagged scene branch waits for a pull. This distinction must be preserved in any adapter or future benchmark.

## Verification and scope

The targeted current packages passed **23 existing test functions plus four research replay subcases**. The separately executed scene/pull test also passed. The main JSON log has 28 pass events because it includes the research parent test; the scene log adds one. Independent audits verified all 140 current source hashes and all 53 historical hashes, exact exchange charges, whole-form receipts, ACK ordering and fixture bytes. The scene audit additionally confirms that no FULL chunk was sent before the pull.

The fixture calls the in-process shore service through `link.Sim`. It **does not exercise HTTP authentication middleware or HTTP compression**. Current `edge/link/connect.go` adds gzip and bearer-token behavior. Existing targeted tests check client gzip and token attachment, but these passing tests do not turn the image replay into end-to-end authentication validation. Likewise, uncompressed protobuf-plus-framing charges from `link.Sim` are not measured physical or compressed HTTP wire traffic.

This remains a two-image, virtual-clock queue-drain integration fixture. It does not reproduce the source clips' duration, the Python ranking/arrival rules, radio losses generally, sustained laptop performance, object recognition, or operator usefulness. The result provides a concrete compatibility and accounting check for a future real scheduler replay, not a production optimization recommendation.

## Reproduction and evidence

- `run_current_validation.py`, `current-test-execution.json`, `current-go-test.jsonl`: exact current package-test command and environment.
- `current-snapshot/edge/scheduler/research_scene_test.go`, `current-scene-test-execution.json`, `current-scene-test.jsonl`: controlled scene/default/pull check.
- `current-results/`: four complete event logs and scene-pull chronology.
- `audit_current.py`, `current-audit.json`: independent current accounting and source checks.
- `compare_current.py`, `current-comparison-audit.json`: historical/current deltas and independent scene audit.
- `current-snapshot-manifest.json`: source revision, archive hash and 140 file hashes.

Exclude downloaded toolchains, dependency/build caches and the raw repository tarball from the compact report package. Keep the source snapshots, harnesses, manifests, fixtures and logs.
