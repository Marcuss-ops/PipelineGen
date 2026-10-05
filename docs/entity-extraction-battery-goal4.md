# Goal 4 — end-to-end entity extraction + overlay validation battery

Status: **hermetic goal complete** (24-script battery green). The live/deployment
half is listed under [Not reachable from a checkout](#not-reachable-from-a-checkout).

## Goal

Do not test a single script. Give the generate endpoint a battery of topics
spanning many different domains and verify the **whole chain** keeps recognising
entities and binding them to the right overlays, with measured precision/recall
instead of a manual look at the JSON:

```text
TOPIC → generate endpoint → SCRIPT → entity extraction → entity normalization
      → timestamp / scene association → overlay generation → VIDEO
```

Definition of done: *"I can hand the generate endpoint dozens of different
topics and automatically get scripts, correct primary entities, temporal
association and final overlays with no manual intervention, with measured
precision/recall and identifiable error cases."*

## Executable certification

| Artifact | Purpose |
|---|---|
| `internal/capabilities/scripts/entity_battery_corpus*.go` | Ground truth: 24 scripts / 12 categories, per-scene narration text, the RAW extractor output (including hard cases), expected entities and rejected noise |
| `internal/capabilities/scripts/entity_battery_harness_test.go` | Runs each script through the production chain and computes the metrics + report |
| `internal/capabilities/scripts/certification_entity_battery_test.go` | The level-1/level-2 gates and the global precision/recall/F1 certificate |
| `internal/capabilities/scripts/entity_name_expansion_test.go` | Focused pin for the name-collapse defect the battery found |

All three run inside the existing `script` component
(`config/verify-components.json` → `./internal/capabilities/scripts/...`), so
`make verify-script` / `make verify-main` execute them with no registry change.

```bash
go test ./internal/capabilities/scripts/ -run 'EntityBattery|Certification_EntityBattery' -v
```

## Corpus

24 scripts, 2 per category. This is an integration/projection contract corpus,
not a labeled corpus of model predictions: the fixture injects predefined raw
extractor outputs. Its result table below certifies grounding, normalization,
deduplication, timing and overlay projection only; it must not be cited as
Rust VisualNER or statistical NER precision/recall.


`WWE`, `WNBA/NBA`, `celebrities`, `automotive`, `technology`, `history`,
`geopolitics`, `business`, `science`, `crime/news`, `immigration`, `cinema`.

Each script deliberately mixes easy and hard cases:

| Case | Example in the corpus |
|---|---|
| PERSON / ORG / GPE | `Elon Musk`, `Tesla`, `Berlin` |
| repeated entity, partial mention | `Roman Reigns` … `Reigns`, `Musk`, `Cook`, `Nolan` |
| ambiguous name | `Jordan`, `Apple`, `Washington`, `Fever`, `Stock` |
| composite name | `New York City`, `European Union`, `Indiana Fever`, `Large Hadron Collider` |
| apostrophe / accent | `L'Oréal`, `São Paulo`, `Beyoncé`, `Jürgen`, `Françoise`, `António` |
| type-vocabulary variants | `LOCATION`/`CITY`/`COUNTRY` → `GPE`, `COMPANY`/`ORGANIZATION` → `ORG` |
| secondary entities | single-word `CONCEPT`, `EVENT`, `WORK_OF_ART` |
| noise that must be rejected | hallucinated `ghost champion`, `VISUAL_SUBJECT`, `KEYWORD`, multi-word editorial `CONCEPT` |

## What the harness actually exercises

Only the external participants are stubbed (LLM text, TTS + word timing, Rust
master mix, RenderingGen queue) — exactly like the existing vertical-slice
certifications. Everything under test is production code:

```text
GenerationEnvelopeV2 → BuildGenerateRequest
  → runner scene generation
  → VidRush segment entities (extraction seam)
  → applyVidRushPrepareProjections → projectEntityAnnotations  (grounding + type normalization + dedup)
  → applySegmentEntityResults                                  (typed per-scene aggregate)
  → compileResultEntityTimeline                                (timestamp / scene association)
  → compileResultOverlayPlan                                   (entity cards + asset promotion)
```

## Success criteria — certified

| Criterion | Gate |
|---|---|
| generate endpoint accepts every topic | `TestEntityBattery_GenerateEndpointAcceptsEveryTopic` |
| every request reaches the extraction plane automatically | same (partitioned request + `NeedsSemanticEnrichment`) |
| PERSON / ORG / GPE recognised and typed | per-script ground-truth comparison |
| same entity not duplicated under a variant name | `Duplicates == 0` + the variant detector |
| secondary entities never contaminate primaries | primary/secondary separation gate |
| every entity associated with the correct script point | overlay `StartMs == occurrence.AudioStartUS/1000` |
| no invented entities | every detection grounded verbatim in its scene text |
| repeated entities handled | one occurrence per entity per scene, `once_per_scene` |
| overlays appear when the entity is mentioned | one entity overlay per certified occurrence |
| the correct overlay is bound to the correct entity | overlay `EntityRef` id/name/type/scene equality |
| the system keeps working across topics | all 24 scripts in a single run |

## Result (2026-09-14, hermetic)

```text
24 scripts tested

Generate endpoint        24/24
Entity extraction        24/24
Entity timing            24/24
Overlay generation       24/24

entities_expected   135
entities_detected   135
correct_entities    135
missed_entities     0
false_entities      0
duplicate_entities  0

projection precision       100.0%
projection recall          100.0%
projection F1              100.0%
```

Every individual script also reports `Generate / Entities / Timing / Overlay`
as `PASS`, so a failure names the script and the level instead of just the
global percentage.

## Defect found and fixed by this goal

`expandPersonName` (the surname → full-name normalization) flushed its candidate
buffer on every space, so it could never join `Musk` to `Elon Musk`. A scene
whose raw extractor output carried both `Elon Musk` and a later `Musk` produced
**two** entities with two different `StableEntityID`s — the same person
duplicated under a slightly different name, plus a spurious overlay for a name
the narration never spoke on its own. Fixed in
`internal/capabilities/scripts/entity_annotations.go`: name runs now join
adjacent capitalized tokens (bounded), and a candidate is accepted only when it
occurs verbatim in the scene text, so a canonical identity is never invented.
Pinned by `entity_name_expansion_test.go`.

## Challenger setup and evaluation

Install the pinned requirements in the existing Python 3.10 runtime used by
`scripts.services.embedding_server`, then start or restart that service with
the challenger model configured:

```bash
.cache/ner-python/venv/bin/python -m pip install --require-hashes \
  -r scripts/services/embedding_server/ner-models.requirements.txt
PIPELINEGEN_NER_MODEL=xx_ent_wiki_sm .cache/ner-python/venv/bin/python \
  -m scripts.services.embedding_server --host 127.0.0.1 --port 8001
```

The spaCy challenger is opt-in; Rust VisualNER remains the default and no backend
is silently substituted. Configure PipelineGen with `external.visualner_backend: spacy` and
`external.spacy_ner_url: "http://127.0.0.1:8001"` (or the equivalent
`VELOX_VISUALNER_BACKEND` / `VELOX_SPACY_NER_URL` environment variables).
The sidecar exposes `POST /ner/extract`; missing model, bad protocol, or invalid
source spans fail closed. The Go adapter converts Python Unicode-codepoint
offsets to UTF-8 byte offsets before the shared source-grounding validator.

After supplying the independently annotated corpus in `ner-corpus.v1` format,
compare backends separately. Keep each `text` field bounded to 100,000 Unicode
codepoints; gold offsets are UTF-8 byte offsets and must align to rune
boundaries. `entity_count: 0` requests the Rust backend’s default of three, so
the evaluator uses the explicit upper-bound request when comparing backends.
Each normalized corpus needs at least 600 adjudicated scenes and at least 100
scenes in each supported language. Empty-entity scenes are allowed but count
against that language’s scene minimum.

```json
{
  "version": "ner-corpus.v1",
  "annotation_method": "human_double_annotation_adjudicated",
  "annotation_guidelines": "v1",
  "annotators": ["annotator-a", "annotator-b"],
  "cases": [
    {"id": "en-001", "language": "en", "text": "Tesla launched in 2020.",
     "entities": [{"text": "Tesla", "label": "ORG", "start": 0, "end": 5}]}
  ]
}
```

The example shows the schema only; do not use it as a substitute for the full
human-adjudicated corpus.


```bash
go run ./cmd/ner-eval --backend rust --corpus /path/to/annotated-corpus.json --iterations 3

go run ./cmd/ner-eval --backend spacy --spacy-url http://127.0.0.1:8001 \
  --corpus /path/to/annotated-corpus.json --iterations 3
```

Each report includes its corpus SHA-256, per-language exact span+type and
boundary scores, label scores, failed scene IDs, hallucination/invalid-offset
rates, invalid/ungrounded output counts, cold-start and warm latency percentiles,
throughput, and process/sidecar peak RSS. Defaults follow the synthetic benchmark
protocol: 20 full-corpus warm-up passes and 200 measured passes; override with
`--warmup` or `--iterations` for smaller smoke runs. Cold-start latency is a
separate first request and is excluded from warm-up and measured statistics.
The evaluator rejects inputs above 100,000 Unicode codepoints before backend calls.
Keep each JSON report outside the repository with the annotation provenance.
Do not promote either backend based on the 24-script projection fixture or an
unlabeled smoke test. Whisper remains confined to acquired clip transcripts;
generated narration uses Edge TTS timing.

## Not reachable from a checkout

These criteria are not certified by this hermetic projection battery. Some
require live services/hardware; statistical NER quality additionally requires
a separate human-annotated, representative multilingual corpus:

1. **Real NER backend quality** — fixture `Raw` entities are simulated; they
   are not predictions or independently verified human gold. Run `cmd/ner-eval`
   against a separate, manually annotated `ner-corpus.v1` covering the six
   supported languages before reporting model precision/recall or promoting a
   challenger. Generated narration uses Edge TTS word timing; Whisper applies
   only when acquiring source clip transcripts, never to generated narration.
2. **Final video / shadow correctness** — overlay rendering is stubbed at the
   RenderingGen queue boundary. Closing measurement: render the 24 topics on a
   real GPU/Chronon deployment and diff each overlay against the expected
   entity/occurrence window (this is level 2 in the live sense: overlay appears
   in the frame while the entity is spoken, correct duration, no wrong overlap).
3. **Throughput at 20–30 scripts in one batch** — the harness runs the scripts
   sequentially in one process. Closing measurement: a live batch run reporting
   per-topic generate/extract/render wall times. `phrase_impact` remains a
   separate subsystem and is outside this NER acceptance surface.
4. **Cross-scene surname-only unification** — a person named by surname only in
   a *later* scene (whose text never contains the full name) is a distinct
   identity: the TEXT gate requires the canonical name verbatim in the scene.
   Unifying it requires a run-scoped alias contract that grounds on the mention
   span instead of the canonical name. The corpus keeps its partial-mention
   cases inside a scene where the full name is spoken, which is the supported
   and contract-correct shape.
5. **Function-word rejection** (`the`, `a`, …) is owned upstream by the
   extractor through `LexiconRegistry.EntityBlocklist`
   (`config/lexicons/en/entity_blocklist.txt`), not by this projection layer.
   The corpus therefore does not simulate function words as extractor output.
