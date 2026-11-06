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

To enable this in Compose, supply read-only certificate/key/CA mounts through
approved secret handling, add the flags/env, change upstream URLs to `https://`,
and change HTTP health probes to HTTPS. For Zarf, use the switches below.
Confirm that the probes' TLS verification behavior meets site policy.
Cover the actual URL hostnames/IPs
in certificate SANs, including any Toxiproxy/operator TCP shaper host used by
the edge; the shaper passes TLS through without terminating it. Adjust network
policies, browser CA trust and TLS termination to the deployment. Restrict key
permissions, arrange renewal, and restart processes after replacing files.

Node consoles use same-origin browser requests; install the private CA in the
browser/OS trust store. The ship console's policy actions use its scheduler link
to shore, not a separate HTTP proxy. Inference sidecars, camera MJPEG and model
compiler connections do not use `-shore-ca`.

## Optional TLS in Zarf (v0.89.0)

The package uses Zarf v0.89's native manifest Go templating (`template: true`).
No shell wrapper, new image or duplicate workload component is needed; the
distroless images do not contain a shell. Empty Secret-name variables omit TLS
flags and volumes entirely, preserving the original plaintext render. The
shore and edge workloads are StatefulSets on this base, not Deployments; their
identity, storage, Service ports and selectors are unchanged.

| Variable | Default | Effect when non-empty |
|---|---|---|
| `SHORE_TLS_SECRET` | empty | Mount an existing `kubernetes.io/tls` Secret on shore, pass `-tls-cert`/`-tls-key`, and use HTTPS readiness/liveness probes |
| `EDGE_TLS_SECRET` | empty | The same listener configuration for edge, independently of shore |
| `SHORE_CA_SECRET` | empty | Mount an existing Secret containing `ca.crt` on edge and operator and pass `-shore-ca` to both |
| `SHORE_URL` | `http://shore.ghost-seeker.svc.cluster.local:8080` | Edge's upstream; this exact default automatically switches to HTTPS with `SHORE_TLS_SECRET` |
| `OPERATOR_SHORE_URL` | `http://shore:8080` | Operator's upstream; this exact default also switches to HTTPS with `SHORE_TLS_SECRET` |

**Custom URLs are preserved**, not rewritten: explicitly set `https://` when
targeting a TLS endpoint (including off-cluster shore). Using a private CA for
an off-cluster shore needs only the CA variable and explicit HTTPS URLs, not a
local listener Secret. Leave `SHORE_CA_SECRET` empty for system-trusted issuers.
The shared bundle can contain multiple roots; the operator also uses it for
edge if an edge upstream is configured. Its packaged `-edge=` remains disabled,
and its browser-facing listener remains HTTP. This is not operator PIV/mTLS.

Provision certificates via your approved PKI. Shore's SANs must cover both the
short `shore` hostname used by the operator and the FQDN used by edge (or change
the URLs/public-host allowlists to match your certificates). Edge's SANs must
cover its actual client/browser hostnames. Create the namespace and Secrets
**before** deploying the TLS-enabled components; certificate contents and private
keys are not packaged in Zarf:

```sh
kubectl create namespace ghost-seeker --dry-run=client -o yaml | kubectl apply -f -
kubectl -n ghost-seeker create secret tls shore-listener-tls \
  --cert=shore.crt --key=shore.key
kubectl -n ghost-seeker create secret tls edge-listener-tls \
  --cert=edge.crt --key=edge.key
kubectl -n ghost-seeker create secret generic shore-client-ca \
  --from-file=ca.crt=ca-bundle.pem

zarf package deploy ./zarf-package-ghost-seeker-amd64-0.1.0.tar.zst --confirm \
  --set-variables SHORE_TLS_SECRET=shore-listener-tls \
  --set-variables EDGE_TLS_SECRET=edge-listener-tls \
  --set-variables SHORE_CA_SECRET=shore-client-ca
```

Names are namespace-local DNS labels (up to 63 characters); names, not key/CA
contents, are Zarf variables. TLS Secrets project `tls.crt` and `tls.key` under
`/run/listener-tls`; CA Secrets project `ca.crt` under `/run/shore-ca`. Both are
read-only mode `0440`, readable by the existing non-root pod's `fsGroup`. A
configured Secret or key is **required**: missing Secrets prevent startup;
invalid certs/keys/CA PEM fail application startup rather than falling back to
HTTP. An omitted CA mount means system roots, never skip-verification.

Listener probes retain `/healthz` and their named port, switching to `HTTPS`
with each listener's Secret. Kubernetes HTTPS probes **skip certificate
verification**; they test liveness/readiness, not trust or SAN correctness.
Application clients still verify chains and hostnames. Validate a successful
authenticated sync/operator request, a rejected untrusted client, probe health
and public-host allowlists in the actual cluster before fielding. Arrange
Secret renewal and explicitly restart shore/edge/operator to reload PEM files.
Removing listener Secret variables restores HTTP; update explicit upstream URLs
at the same time to avoid a mixed-protocol rollout.

`deploy/zarf/check.sh` requires Zarf v0.89+ and PyYAML for native rendering. It
runs the checker tests, validates all eight listener-TLS/CA on/off combinations
(including flags, Secret projections, probe schemes and upstream URLs), and
feeds those actual renders to kubeconform. It does not contact a cluster or
validate the contents/type of pre-provisioned Secrets; do not apply the raw,
unrendered Go-template manifests directly with kubectl.
