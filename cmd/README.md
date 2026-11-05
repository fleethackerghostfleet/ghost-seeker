# Product commands

`make build` builds these commands into `bin/`. The Docker images build from
this directory and do not compile research tools.

| Command | Role |
|---|---|
| `edge` | Ingest, infer, prioritize, retain, and transmit camera observations |
| `shore` | Receive observations and serve the shore ConsoleService |
| `ghost-seeker` | Operator UI and API; live shore bridge and demo link shaper |
| `mint-unit-token` | Unit credentials; also used by the demo initialization container |
| `policy-keygen` | Deployment utility for optional signed policies |

The simulator, benchmark, offline retuner, and test certificate generator are
in [`research/cmd/`](../research/cmd/). Use `make build-research` for those.
