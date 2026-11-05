# Frame archives

The six ZIPs are already Git LFS objects (`*.zip` in `.gitattributes`).
`manifest.json` records local SHA-256/LFS OIDs and the original GCS inventory's
object generations, MD5s and sizes. Five archives have an exact byte-for-byte
MD5/size match in that inventory, including the SMD archive under `raw/`.

Do not remove the LFS objects yet: the GCS ZIP with the Cargo Ship Uncut View
name is not byte-identical to the local archive. An extraction with equivalent
frames is not an archival checksum match. Preserve the local archive until an
independent immutable upload is verified. No Git history rewrite is needed.

For new large datasets, prefer versioned GCS objects and a checksum/generation
manifest rather than adding more LFS payloads. Existing originals remain intact.
See `research/edgeml/reports/DATA_CLEANUP.md` for the audit and clean selection manifests.

The manifest retains the original `frames/` paths as provenance. The same
archive names now live here under `research/frames/`; their Git LFS OIDs and
checksums are unchanged.
