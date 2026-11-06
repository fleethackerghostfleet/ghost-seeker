## Summary

<!-- What changed and why. -->

## Testing

<!-- Commands run and results. -->

## prod

Required for any PR to `prod`; recommended for every PR to `dev` (see `AGENTS.md`).

- [ ] No demo feature reachable in `-tags prod` builds (new demo wiring is in `*_demo.go` with a `*_prod.go` stub)
- [ ] Production still accepts the live input stream (ghost-seeker `-shore`, edge live ingest + inference sidecar)
- [ ] Output stream still served by the authenticated, versioned `/api/v1` on shore and documented in `docs/api/openapi.yaml`
- [ ] Auth is a real operator token or PIV (no dev token, trusted peers or accept-any-token)
- [ ] Demo-only UI is gated on `/api/config` `features`
- [ ] `make prod-gate` passes; the gate and `TestProdContract*` tests were not weakened or skipped

<details><summary><code>make prod-gate</code> output</summary>

```
paste the output here
```

</details>
