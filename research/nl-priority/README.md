# Priority Lab — research only

A local image-prioritization experiment using **IBM Granite 4.0 Nano 350M
Instruct (Q4_K_M)** for language and **Apple MobileCLIP-S1** for visual matching.
The page compares the existing production scorer and scheduler before and after
an instruction. Apply and undo are explicit; production services are untouched.

## Start on a Mac

Install Docker with Compose, then from the repository root:

```sh
./research/nl-priority/run.sh
```

Open **http://localhost:8097**. The first run downloads checksummed model weights,
exports matching CLIP encoders, and builds CPU containers. Export uses PyTorch
only during setup; runtime vision inference uses ONNX. Initial setup needs an
internet connection and substantially more memory than runtime. Subsequent
fully offline starts use the already provisioned models and built images:

```sh
./research/nl-priority/run.sh offline
./research/nl-priority/run.sh logs
./research/nl-priority/run.sh down
```

`NL_PORT` changes the localhost port. `NL_MODELS_DIR` changes model storage.
Runtime containers have an internal-only network; only the page is exposed on
localhost. Research state lives in its own named volume and survives shutdown.
Models and generated queues are ignored by Git. No paid GitHub features or cloud
inference are required.

**Validation status:** see [RESULTS.md](RESULTS.md). Native CPU execution was
validated on a Mac. Docker was unavailable on the implementation host, so
container startup is provided but has not been end-to-end validated there.
The 95% language-quality threshold is an acceptance target, not a claimed result.

## Bring actual imagery

The lab accepts **detected object crops**, or frames with an existing detector's
crop box. It does not add a new detector or pretend that every new label can be
found in a full frame. A JSONL manifest contains one record per crop:

```json
{"id":"sighting-1","path":"crops/boat.jpg","label":"boat","confidence":0.82,"subtype":"fishing boat","unknownness":0.24,"source":"camera-1"}
```

Optional fields: `box: [x1,y1,x2,y2]`, `capturedAtUnixMs`, `labelOverride`,
`overrideBy`, and `tomerScore`. Pass original inference metadata; never use ground
truth as a scoring input. Missing evidence stays unknown. Tomer scores must come
from the existing algorithm; select EdgeML when they are unavailable.

Place the manifest and crops beneath `research/nl-priority/inputs/`, with image
paths relative to the manifest. Encode them and populate the isolated queue:

```sh
mkdir -p research/nl-priority/data
# The main stack must already be running.
docker compose -f research/nl-priority/compose.yml --profile tools run --rm ingest \
  --manifest /input/crops.jsonl --vision-url http://vision:8098 \
  --output /output/queue.json --backend-url http://app:8097
```

Alternatively import the generated `queue.json` using **Import queue JSON** on
the page. Imports append unique item IDs, with a total limit of 1,000 items. Duplicate IDs reject the batch. Each item
contains the matching encoder identity, its image embedding, a display thumbnail,
and the original classification/unknownness metadata.

Try `prioritize submarines`, `deprioritize cargo ships`, or `prioritize rescue
boats`. Review the interpretation and the ranking/delivery differences, then
Apply. New concepts are matched visually; weak matches are withheld. This is
expected, particularly on small or blurry crops. The displayed similarity is not
a calibrated probability. Human labels override model labels. Unknown-object
protection remains active, including when an instruction deprioritizes a class.

Everyday requests such as `look for big ships`, `find fishing vessels`, or
`I am interested in cargo ships` also express visual interests. Granite interprets
these as boosts; no prioritization keyword is required. Review the proposed
category and priority change before Apply.

Instructions support boosts, suppression, changing emphasis, removing rules,
reset and undo. Short references such as `less of those` need an unambiguous
existing interest. Source/geographic/time conditions, behavior inferred from a
single crop, and changes to unknown/human protection are outside this version.
The preview's readback is important: a tiny model can misunderstand an instruction.

Simple command verbs use the fast parser. Category names outside the known
vocabulary still go through Granite: it can resolve an equivalent term to an
existing category, or preserve the full phrase as a candidate concept. For
example, `prioritize fishing vessels` should interpret the category as
`fishing boat` rather than create a competing experimental label. There is no
hardcoded synonym table. Granite also extracts qualifiers such as size and color;
a candidate category that omits a reported qualifier cannot replace the original
phrase. The constrained response can select only a supplied
category or retain the original phrase; malformed or unavailable responses
cannot change active state. This validates the response structure, not its
semantic truth. Preview displays resolved terms, the number of priorities
changed, and warnings when no current images match. Apply remains explicit.

## Native development

Use Go matching the root `go.mod`, Python 3.11, and a CPU llama.cpp build at
`45b6604b20f17b7d7638042306c0cd1b6af5999c`. The Granite Dockerfile records its build
flags. From the repository root:

```sh
python3 -m venv research/nl-priority/.venv
. research/nl-priority/.venv/bin/activate
pip install -r research/nl-priority/vision/requirements-export.txt
python research/nl-priority/tools/fetch_granite.py research/nl-priority/models
export PYTHONPATH=edgeml/src:research/edgeml/src
python -m edgeml_research.nl_priority.export \
  --output research/nl-priority/models/mobileclip \
  --cache-dir research/nl-priority/models/cache
```

Run these in separate terminals (same virtual environment and `PYTHONPATH`):

```sh
llama-server --model research/nl-priority/models/granite-4.0-350m-Q4_K_M.gguf \
  --alias granite-4.0-350m --host 127.0.0.1 --port 8099 \
  --ctx-size 2048 --threads 2 --threads-batch 2 --parallel 1 \
  --batch-size 128 --ubatch-size 128 --n-gpu-layers 0 --cache-ram 0 --no-webui

python -m edgeml_research.nl_priority.server \
  --models research/nl-priority/models/mobileclip --listen 127.0.0.1:8098

go run ./research/cmd/nl-priority --listen 127.0.0.1:8097 \
  --data research/nl-priority/data/state --llm-url http://127.0.0.1:8099 \
  --vision-url http://127.0.0.1:8098 --web-dir research/nl-priority/web
```

Use the same ingest module and command as above with native paths and localhost
URLs. The importer embeds each actual crop once; commands reuse those embeddings.
The server's `--bandwidth-bps` defaults to 128,000 bits/second. Delivery projections
use the actual production `BuildPlan` over ten five-second ticks, including
metadata, chunking, and fidelity order. Image bytes are represented by their
sizes: these traces are simulated transfers, not actual shore receipts.

## APIs and integration

See [INTEGRATION.md](INTEGRATION.md) for the research API, data boundaries, and
promotion steps. The research Go package imports production scoring/scheduling;
production never imports research. The standard product images are unchanged.

## Repeat verification

```sh
pip install pytest
go test -race ./research/nlpriority ./edge/scoring ./edge/scheduler
PYTHONPATH=edgeml/src:research/edgeml/src python -m pytest \
  research/edgeml/tests/test_nl_priority*.py research/nl-priority/tools/test_tools.py
python -m unittest discover -s tools -p 'test_layout.py'
python research/nl-priority/tools/eval_commands.py --url http://127.0.0.1:8097 \
  --corpus research/nl-priority/eval/commands.json --output research/nl-priority/data/commands.json
```

With the native Granite service running, the targeted semantic integration test
uses real model responses (including legacy literal-rule resolution):

```sh
GRANITE_TEST_URL=http://127.0.0.1:8099 go test ./research/nlpriority \
  -run TestLiveGraniteCategoryResolution -count=1 -v
```

Use `tools/benchmark.py --help` for measured aggregate RSS and warm p95 latency.
Include all three runtime process IDs, verify the actual queue contains 1,000
items, and use distinct concepts to include uncached text encoding. Repeating
existing image embeddings is valid for scale testing, not classification-quality
claims. The labels evaluator requires independent ground-truth labels and reports
per-concept precision, recall and abstention; see [vision documentation](vision/README.md).
