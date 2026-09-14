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

24 scripts, 2 per category:

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

NER precision       100.0%
NER recall          100.0%
NER F1              100.0%
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

## Not reachable from a checkout

These criteria need live services or hardware and are therefore **not** claimed
by the hermetic battery. Each names its closing measurement:

1. **Real NER provider output** — the corpus simulates the extractor with raw
   `ExtractedEntity` lists. Closing measurement: run the 24 topics through the
   real VidRush/Ollama extractor and compare the produced raw entities against
   the same ground truth.
2. **Final video / shadow correctness** — overlay rendering is stubbed at the
   RenderingGen queue boundary. Closing measurement: render the 24 topics on a
   real GPU/Chronon deployment and diff each overlay against the expected
   entity/occurrence window (this is level 2 in the live sense: overlay appears
   in the frame while the entity is spoken, correct duration, no wrong overlap).
3. **Throughput at 20–30 scripts in one batch** — the harness runs the scripts
   sequentially in one process. Closing measurement: a live batch run reporting
   per-topic generate/extract/render wall times.
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
