# Authentication audit

Scope: `internal/opauth`, `internal/pivauth`, `internal/unitauth`, edge live
ingestion, shore SyncService, shorebridge, and operator/admin bearer gates.
This is a source and synthetic-fixture audit, not a deployed-service assessment.

## Findings and disposition

Severity is conditional on the reachability requirements in the table. The five
fixes below implement the explicitly approved policies; issuer/audience migration
and length-oblivious token comparison are deliberately out of scope.

| Location | Finding | Severity | Reachability | Disposition |
| --- | --- | --- | --- | --- |
| `internal/pivauth/auth.go`, `Authenticate` | `ExtKeyUsageAny` chain verification plus a separate leaf check admitted a clientAuth leaf from a serverAuth-only issuer. | High, conditional | PIV mode; a chain-trusted restricted issuer must issue a certificate for an authorized identity, with usable revocation information. | **Fixed:** filter verified paths to those sharing clientAuth or Microsoft smartcard-logon purpose across the entire path, including constrained roots. Unrestricted/Any issuers do not narrow the intersection. Existing explicit leaf EKU and digitalSignature requirements remain. |
| `internal/pivauth/auth.go`, `ocspStatus` | Embedded responder certificates were signature-checked but not authorized for OCSP signing or checked for current validity. | High, conditional | Configured OCSP must deliver a response signed by such a certificate. No deployed responder compromise or interception was tested. Default OCSP-off configuration does not take this path. | **Fixed:** verify the response signature against the issuing CA or a currently valid, directly CA-issued delegate with explicit OCSPSigning EKU. Invalid responses remain OCSP errors and fall back to CRLs. |
| `internal/opauth/session.go`, `issue` | A new login ID left the presented previous session usable, including after logout of the replacement ID. | Medium | A successful session-issuing login presenting an existing cookie. | **Fixed:** delete only the presented prior session and binding when issuing its replacement; other devices' sessions remain valid. Failed logins do not revoke sessions. |
| `internal/opauth/session.go`, `Wrap` | Separate validity/role lookups could observe logout between them and default a formerly authenticated reviewer to operator. | Medium, conditional | Reviewer sessions. The executable restricts review-link mode to non-prod simulation/dataset operation. The probe demonstrated identity escalation in `/api/auth`, not a successful mutating-request exploit. | **Fixed:** read validity and explicit role under one lock, fail closed on missing/empty bindings, and reuse that snapshot for identity, guards and principal context. Issued operator sessions now have explicit bindings. |
| `internal/unitauth/auth.go`, `authenticate` | Shore accepted a signed token with future `iat` that edge rejected. | Low | Requires an otherwise valid signed/provisioned unit token. | **Fixed:** shore uses the same optional issued-at validation and zero skew as edge. Missing `iat` remains supported. |
| `internal/unitauth/auth.go` | HS256 is pinned; `exp` is required; optional `nbf` and now `iat` are validated. No issuer/audience requirement exists. JWT `kid` does not select keys. | Informational / policy | Required unit auth uses one configured signing key, role `unit`, nonempty subject and matching request edge ID. | Algorithm pinning, key source and issuer/audience contract unchanged. Tests explicitly reject `none` on both edge and shore. |
| `internal/unitauth/auth.go`, `ConfiguredTokenActive`; `edge/ingest/live.go` | Edge parses the configured token without signature verification because it has no shore signing key. | Informational / trust boundary | Live ingest first requires exact equality to the provisioned token; arbitrary caller JWTs do not reach the lifetime check as accepted credentials. | Unchanged. This is a provisioned bearer credential check, not a replacement for shore signature verification. |
| `internal/opauth/opauth.go`, `Equal`; bearer/admin/review checks | `subtle.ConstantTimeCompare` protects equal-length comparisons, but mismatched lengths return early. | Low residual | Bearer/login endpoints can potentially disclose configured credential length, not a matching secret prefix. | Intentionally unchanged by approval. No ordinary string-equality comparison of bearer/admin credentials was found. Public subjects, roles, certificate fingerprints and pagination tokens are not secret-token comparisons. |
| `internal/pivauth/auth.go`, `revocation`, `Reload` | Missing/stale CRLs fail closed by default; OCSP errors/Unknown fall back to CRLs. Explicit CRL grace and retention of last-good reload state are documented policies. | Informational / policy | PIV mode; optional OCSP, grace and reload settings affect behavior. | Unchanged. A valid configured OCSP Good response remains authoritative over CRLs; this audit did not change precedence or fail-open/closed policy. |
| `internal/pivauth/auth.go`, `upn`, `identity` | Identity comes from signed SAN otherName with the expected Microsoft UPN OID/tag structure, then documented CN/fingerprint fallbacks. Policy OIDs are optional/configured and checked by x509 path verification. | No demonstrated bypass | Chain-trusted certificate and exact users-file identity mapping required. | No identity-normalization or fallback policy change. |
| `internal/opauth/session.go`, `certs.go`; `internal/httpx/security.go` | Cookies are 256-bit random IDs, HttpOnly, SameSite=Strict, Secure on TLS. Current-session logout deletes its record and binding. SameOrigin wraps both executable app modes. | No additional demonstrated bypass | Browser/session endpoints; TLS-terminating proxy header handling remains a deployment responsibility. | Flags and CSRF policy unchanged. PIV sessions remain certificate-bound and revalidated on every request; a present valid card can authenticate again after logout, as documented. |
| `internal/shorebridge/bridge.go`; `internal/server/policy.go`; edge/shore command wiring | Bridge calls inject the configured credential; proxying strips browser Cookie and Authorization. Admin editing requires explicit admin enablement and bearer re-authentication, not just a session. | Informational | Browser bridge routes use outer Sessions/PIV; edge/shore APIs use bearer gates; SyncService has its separate unit interceptor. | No weakening of route gates. Legacy ReverseProxy Director wiring still permits a caller's `Connection: Authorization` to strip the injected credential, causing upstream auth failure rather than granting access; transport/proxy modernization is separate work. |
| Auth error responses and first-party logging | Invalid operator/unit credentials produce generic errors. PIV exposes diagnostic failures and the certificate holder's own identity after certificate trust checks. | No demonstrated cross-user enumeration | An untrusted caller cannot select arbitrary signed PIV identities to probe the users file. | No deliberate raw bearer/admin token logging found in reviewed first-party paths. PIV audit records contain identity, role, path, result and reason, not Authorization/cookie values. External upstream logs/responses were not audited. |

## Evidence and reachability

`cmd/ghost-seeker/main.go` constructs the PIV authenticator, installs its TLS
configuration, and calls `Sessions.WithCerts`. `Sessions.Wrap` dispatches to
`wrapCerts`, which calls `Authenticate` before the protected handler. These are
shared production/default paths. The original synthetic restricted-issuer probe
passed ordinary client-auth verification for an unconstrained issuer, failed it
for a serverAuth-only issuer, but obtained operator access from the original
authenticator in both cases. Stale-CRL controls still rejected access.

`revocation` calls `ocspStatus` only with an explicit OCSP URL and treats Good as
authoritative. The original synthetic responder probe accepted clientAuth-only
and expired delegates despite a revoked-leaf CRL; disabling OCSP rejected that
same leaf. Tests now require rejection via the existing CRL fallback. They also
cover valid delegation, direct issuer signatures with/without an embedded issuer,
future certificates, foreign issuers, bad signatures and a valid CRL fallback.

The session tests use actual wrapped login/logout/protected requests. They verify
that an invalid login leaves the previous session intact, replacement revokes the
presented ID, another device stays authenticated, and logout revokes the new ID.
A deterministic response-header hook completes logout on another goroutine after
the reviewer authentication snapshot but before identity serialization; the
response retains reviewer identity, never defaults to operator, and subsequent
writes with the logged-out cookie fail. Missing and empty role bindings fail
closed on identity and write paths.

Unit JWT regression tests compare edge and shore handling of missing, past and
future `iat`, future `nbf`, and unsigned `none` tokens. Existing integration tests
cover signatures, expiry, roles, subjects and per-message edge identity matching.

## Regression tests

- `internal/pivauth/chain_usage_test.go`: restricted issuer bypass, unrestricted
  issuers, clientAuth, smartcard-only leaf/path, mixed-purpose rejection, Any issuer.
- `internal/pivauth/ocsp_responder_test.go`: responder authority, validity and CRL fallback.
- `internal/opauth/session_rotation_test.go`: prior-ID revocation and other-device isolation.
- `internal/opauth/session_snapshot_test.go`: deterministic logout interleaving and missing roles.
- `internal/unitauth/temporal_test.go`: edge/shore temporal parity and rejection of `none`.

No production contract tests, build gates, demo boundaries, or authentication
configuration defaults were weakened. These tests use synthetic keys/certificates
and loopback fixture servers, not production credentials or external services.
