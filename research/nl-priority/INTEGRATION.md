# Research boundary and production integration

## Data flow

1. Existing detector/algorithm emits a crop and its original label, unknownness,
   optional human override and Tomer score.
2. The matching MobileCLIP image encoder computes a reusable embedding.
3. A command compiler (strict rules, otherwise local IBM Granite) proposes
   validated interest rules. The LLM never sees or processes image pixels.
4. The text encoder compares the base vocabulary and up to16 operator concepts
   against cached image embeddings. Low similarity/margin produces abstention.
5. A scoring copy receives accepted interest markers. Original inference evidence,
   human labels, and unknownness remain immutable.
6. Existing production scorer and scheduler produce the preview. Apply atomically
   records one policy/vocabulary/match revision in isolated research state.

The FP32 MobileCLIP text graph is selected because the evaluated INT8 graph failed
parity. Every exported graph/tokenizer file is hashed in its manifest. The model
identity includes the exact source checkpoint hash; vector length alone is never
accepted as proof of compatibility. Python inference is stateless with respect to
policy: pending previews cannot change another user's active vocabulary.

## HTTP interface (research only)

All requests/responses are JSON. Page and API share one localhost origin.

| Method/path | Purpose |
|---|---|
| `GET /api/health` | Backend liveness/configuration; does not certify model quality |
| `GET /api/state` | Active revision, engine, rules, vocabulary, ranked queue, undo depth |
| `POST /api/compile` | `{text,rules?}` → validated draft; no state mutation |
| `POST /api/preview` | `{text,engine?,basePreviewId?}` → preview ID, source revision, draft, before/after rankings and delivery traces |
| `POST /api/apply` | `{previewId}` → new active state; stale/expired previews rejected |
| `POST /api/undo` | Recovery API restoring previous revision; UI uses previewed `undo` instead |
| `POST /api/items` | `{items:[...]}` → append original evidence with unique IDs and classify against active interests |

The Python service provides `GET /health`, `POST /embed {texts}`, `POST /classify
{concepts,items}`, and `POST /image-embed {images:[{id,imageBase64}]}`. Embeddings
carry `modelId`; mismatches fail explicitly. Classification responses include
similarity, margin, acceptance, experimental status and an abstention reason.

State writes use a temporary file, fsync, and rename. Current state changes only
after successful persistence. Undo retains32 revisions. New arrivals are reclassified against the pending vocabulary at Apply; changes
to the active policy invalidate pending previews. The displayed preview is a
queue snapshot, and incoming images receive the applied revision.
Admission and state operations are serialized; this small research harness is
intentionally bounded to1,000 items and16 added concepts.

## Why a policy-only copy is insufficient

The production Interest scorer intentionally uses loose substring matching. A
plain `rescue boat` boost could therefore match the existing generic `boat` label.
The research adapter uses stable opaque concept markers in scoring copies and
translates score explanations back into readable concepts. Human overrides are
matched exactly and preserved byte-for-byte for other production scorers.

The emitted `Policy`/`Interest` message uses existing fields, but its markers
require this matching adapter. **Do not send the research policy directly to an
unmodified production edge and expect equivalent behavior.** Production policy
layer flattening can also replace embeddings, so vectors cannot simply be injected
into a shore request without integrating the matching text encoder.

## Promotion checklist

- Keep this branch on the research/development side of the existing dev/demo/prod
  workflow; it is stacked on repository cleanup PR96 while that PR remains open.
- Pass the documented language, labeling, resource and throughput acceptance
  gates on representative deployment hardware and independent data.
- Promote the small intent/matching adapter with tests. Reuse the existing signed
  policy/layer/version transport; preserve echelon locks and human overrides.
- Add a coordinated versioned vocabulary activation hook at the real inference
  sidecar and backlog boundary. Persist model identity, reclassify cached embeddings,
  and rescore without rerunning detection or changing original unknownness.
- Connect the shore text-embedding endpoint to the actual matching text encoder;
  the default mock embedding backend is unsuitable for real image vectors.
- Run a shadow comparison of production rankings/delivery plans, then enable an
  explicit operator-controlled feature flag. Disabling it restores the original
  policy and inference path. No production protobuf changes are needed for this
  prototype; any later transport extension requires a separate compatibility review.

Research APIs do not grant production credentials or modify the deployed demo.
