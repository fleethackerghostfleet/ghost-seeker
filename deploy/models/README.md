# Verified inference assets

```sh
deploy/models/fetch.sh /path/to/downloads
python3 deploy/models/prepare.py /path/to/downloads/files deploy/models/SHA256SUMS /path/to/empty-model-context
```

The original image-model archive remains pinned to `demo-data-v1`. Optional
`text_model.onnx` and `tokenizer.json` are pinned separately to `models-text-v1`
and installed beside the MobileCLIP-S1 image model. `prepare.py` adds both files
to the model-image context and its SHA256SUMS only when both are present and
match `TEXT_SHA256SUMS`. Missing both retains image-only support; a partial or
corrupted pair fails packaging. An empty output context is required.

`FETCH_TEXT=0` skips text downloads; an image-only sidecar advertises
`openVocabulary=false`. Mount the prepared MobileCLIP directory as the existing
`--embed` directory; no new runtime flag or network/model download is needed.
The production UDS model-image packaging can consume this same prepared context.

Licensing remains unresolved; see THIRD_PARTY_NOTICES.md.
