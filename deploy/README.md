# Deployment transport configuration

Shore and edge support opt-in server TLS 1.3. Neither `deploy/compose/` nor
`deploy/zarf/` enables it by default; their existing listener ports, URLs and
health probes remain HTTP. The optional Compose PIV operator profile is separate.

With existing unit/operator credentials provisioned (see
[Fielding](../docs/FIELDING.md#sync-authentication)), a direct deployment can use:

```sh
./bin/shore -listen :8080 -tls-cert /run/tls/shore.pem -tls-key /run/tls/shore.key
./bin/edge -shore https://shore.example:8080 -shore-ca /run/tls/ca-bundle.pem \
  -listen :8081 -tls-cert /run/tls/edge.pem -tls-key /run/tls/edge.key
./bin/ghost-seeker -shore https://shore.example:8080 -edge https://edge.example:8081 \
  -shore-ca /run/tls/ca-bundle.pem -link-listen ""
```

The operator command above configures **upstream** TLS only; use its existing
PIV/mTLS mode or an approved HTTPS proxy for the browser-facing listener.
On shore/edge, `GS_TLS_CERT` and `GS_TLS_KEY` are equivalent to the server flags.
On edge and the live operator, `GS_SHORE_CA` is equivalent to `-shore-ca`; the
operator uses that bundle for both consoles and its shore blob/REST/SSE proxy.
No CA bundle means system trust roots; supplied PEM roots are added to them.
An unpaired server cert/key, invalid key pair or unusable CA file fails startup.
No insecure verification switch is provided. Listener TLS does not replace unit
JWT authentication, operator credentials or policy-signature checks.

To enable this in Compose/Kubernetes, supply read-only certificate/key/CA mounts
through approved secret handling, add the flags/env, change upstream URLs to
`https://`, and change HTTP health probes to HTTPS. Confirm that the probes'
TLS verification behavior meets site policy. Cover the actual URL hostnames/IPs
in certificate SANs, including any Toxiproxy/operator TCP shaper host used by
the edge; the shaper passes TLS through without terminating it. Adjust network
policies, browser CA trust and TLS termination to the deployment. Restrict key
permissions, arrange renewal, and restart processes after replacing files.

Node consoles use same-origin browser requests; install the private CA in the
browser/OS trust store. The ship console's policy actions use its scheduler link
to shore, not a separate HTTP proxy. Inference sidecars, camera MJPEG and model
compiler connections do not use `-shore-ca`.
