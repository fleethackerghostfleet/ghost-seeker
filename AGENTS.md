# Agent and bot instructions (Ghost Seeker)

Read this before changing code or opening a PR. It applies to every human,
bot and AI agent (Devin, Claude, Copilot, CI bots) working in this repository.

## Branch roles

| Branch | Role | Rule |
|---|---|---|
| `demo` | Locked demo build | Never touch: no pushes, merges, PRs or rebases. |
| `dev` | Default branch | All work lands here through pull requests. |
| `main` | Second demo | Changes arrive by PR only. |
| `prod` | Final version deployed to the fleet | Changes arrive by PR only, with `make prod-gate` passing. |
| `prod-uds` | Production on UDS (Unified Defense Stack), branched from `prod` | Changes arrive by PR only, with `make prod-gate` passing. |

Never push directly to `dev`, `main`, `demo`, `prod` or `prod-uds`. Branch from
`origin/dev`, open a PR to `dev`, and do not merge your own PR unless a
maintainer asks you to.

## Production rules

1. **No demo features in production builds.** Production is built with
   `-tags prod` (`make build-prod`, `deploy/Dockerfile`, Zarf images). Demo
   wiring lives in `*_demo.go` (`//go:build !prod`) with stubs in `*_prod.go`
   (`//go:build prod`); never delete demo code (dev and main still run the demo)
   and never reference demo-only code from shared files. New demo features
   (simulation, replay, mock analyzers, presenter shortcuts, demo auth, demo
   imagery) must be compiled out of prod the same way.
2. **Production accepts a live input stream.** ghost-seeker runs only in live
   mode (`-shore`); the edge ingests live sensor input scored by the inference
   sidecar. No synthetic generator, replay or mock fallback in prod.
3. **The output stream is available through the versioned, authenticated API**
   `/api/v1` on shore, documented in `docs/api/openapi.yaml`. Keep the spec and
   its `TestProdContract*` tests in sync with the code; never remove or
   unauthenticate it.
4. **Authentication is a real operator token or PIV.** No dev token
   auto-generation, trusted-peer auto-login or accept-any-token in prod.
5. **The console gates demo-only UI on server capabilities**
   (`/api/config` `features`), not on separate HTML.
6. **`make prod-gate` must pass before any PR to `prod` or `prod-uds`**, and should pass on
   every PR to `dev`. Paste its output in the PR template's prod section.
   Never bypass, skip, weaken or delete the gate, its checks or the
   `TestProdContract*` tests to make a change pass; fix the change instead.
   Changes to the gate, `.github/`, this file and `*_prod.go` need review from
   the code owners (`.github/CODEOWNERS`).

## Verification

- `make test`, `node --test web/*.test.cjs`, `make prod-gate`, `make verify`.
- Shared live previews run on ports 8105, 8106 and 8109; never stop or reuse
  them. Run your own instances on other ports.
- Never print or commit tokens or keys.

See `docs/FIELDING.md` (Production profile and prod gate) for details.
