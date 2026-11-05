# Product deployment

The **current live demo** is [`compose/demo.compose.yml`](compose/demo.compose.yml).
Run `make compose-demo` from the repository root after provisioning clips and
models; see [the demo runbook](../docs/DEMO.md).

| File | Purpose |
|---|---|
| `Dockerfile` | Product Go images: edge, shore, operator; plus demo credential initialization |
| `../edgeml/Dockerfile` | Python inference sidecar; live demo enables tracking |
| `compose/demo.compose.yml` | Live product demo: sidecar, edge, shore, operator UI, initialization |
| `compose/docker-compose.yml` | Alternate edge/shore + Toxiproxy topology, optional PIV operator; requires explicitly provisioned credentials and uses research frame archives |
| `compose/degrade.sh` | Link-shaping helper |
| `zarf/` | Fielding package manifests and validation tools |

Build contexts explicitly include product inputs. The live demo mounts the
product scoring library, shared catalog rules, demo scenario, and external
models. It does not mount `research/`.
