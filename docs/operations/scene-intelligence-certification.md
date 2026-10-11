# Scene Intelligence · Runtime Certification

52 tests in 8 groups. Every ID below maps to an executable test — no manual
checklist item passes without its command green. Property-based: orderings,
invariants and metamorphic equivalences, never exact magic numbers
(`density = 0.82` is not asserted anywhere).

Run everything:

```bash
cd refactored/rust/pipelinegen-muscles && cargo test scene_intelligence --lib
cd refactored && go test ./internal/kernel/script/ ./internal/capabilities/overlays/ \
  ./internal/capabilities/scripts/ ./internal/capabilities/scripts/overlay/ \
  ./internal/platform/media/rustexec/
cargo bench --bench scene_intelligence   # baseline only, not a gate
```

## 1. Information Density (6)

| ID  | Test | Location |
| --- | ---- | -------- |
| D01/D02 | sparse scores below rich (`sparse < rich`) | `scene_intelligence.rs::cert_density_ordering_sparse_vs_rich` |
| D03 | 5x verbatim repetition adds no novelty (equal scores) | `cert_density_fivefold_repetition_not_proportional`, `density_repetition_does_not_inflate` |
| D04 | same length, more distinct concepts → higher density | `cert_density_new_info_increases_at_same_length` |
| D05 | same content spoken faster → higher load/sec | `script::TestDensityPerSecondRisesWithSpeechRate` |
| D06 | dense scene + 8 candidates → simultaneity cut, constraints kept | `script::TestSIDenseSceneReducesSimultaneity`, `scripts::TestSIX02TenCandidatesCoherentAdmission` |

100-word rule (D-test fondamentale): `cert_density_new_info_increases_at_same_length`
compares equal-length inputs; the distinct-concept text must win on score
and on `new_concepts`.

## 2. Comparison (6)

| ID  | Test | Location |
| --- | ---- | -------- |
| C01 | Ford/Toyota contrast, no invented values | `comparison_qualitative_invents_no_numbers` |
| C02 | "Vendite 2020 vs 2025" temporal, years ≠ quantities | `cert_temporal_comparison_years_as_subjects` |
| C03 | co-mention without comparison → nothing | `comentions_without_marker_link_nothing` (graph side) |
| C04 | qualitative carries no values (gate) | `manifest_rejects_qualitative_with_values`, bridge `TestDecodeSIRejectsQualitativeWithValues` |
| C05 | "non è superiore" keeps `negated` | `cert_negated_comparative_keeps_negation`, overlays `TestSINegatedComparisonNeverPositive` |
| C06 | paraphrase yields equal structure | `cert_paraphrased_comparison_equivalent` |

Structural output contract (`comparison.rs` equivalent): kind, subjects,
dimension, relation `contrast`, `quantitative_values_available=false` for
qualitative — enforced by `validate_manifest`.

## 3. Cause–Effect (6)

| ID  | Test | Location |
| --- | ---- | -------- |
| E01 | asserted causation extracted | `dopo_is_sequence_not_cause` (cause half) |
| E02 | succession ≠ causation | `dopo_is_sequence_not_cause` (sequence half) |
| E03 | negated cause → no positive relation | `cert_negated_cause_yields_no_positive_relation` |
| E04 | attribution + conditional kept, confidence discounted | `cert_attributed_hypothetical_keeps_attribution`, overlays `TestSIHypotheticalCausalGated` (score halved) |
| E05 | A→B→C gives asserted links, no A→C shortcut | `cert_chain_extracts_links_without_transitive_invention` |
| E06 | reverse order keeps direction | `cert_reverse_order_keeps_direction` |

Plus: `contributes_to ≠ causes` (`cert_contributes_to_distinct_from_causes`).
Renderer gate: hypothetical relations carry `[hypothetical]` at half score —
never a certain arrow.

## 4. Timeline Events (6)

| ID  | Test | Location |
| --- | ---- | -------- |
| T01 | 2024/2020/2022 narrated → 2020→2022→2024 | `timeline_event_time_separate_from_video_time` |
| T02 | "tre anni dopo" + known anchor → 2023 | `cert_relative_resolves_with_anchor_not_without` |
| T03 | same, no anchor → unresolved | same test (None branch) |
| T04 | 2020–2023 is one interval, not an instant | `cert_interval_represented_as_span` |
| T05 | same year, two events → both kept | `cert_same_year_two_events_kept` |
| T06 | `event_date` ≠ `video_start_ms` | `cert_event_date_separate_from_video_window` |

Announcement/construction/completion case: order chronologically by
`normalized_year`, narrate in `narration_index` order — both preserved.

## 5. Entity Graph (6)

| ID  | Test | Location |
| --- | ---- | -------- |
| G01/G02 | `owns` vs `partners_with` typed correctly | `extract_relations` marker table + `cert_extractor_uncapped_visual_capped` fixture |
| G03 | same-sentence co-mention → nothing | `comentions_without_marker_link_nothing` |
| G04 | "non possiede più" → no `owns` | `cert_negated_ownership_links_nothing_positive` |
| G05 | certain aliases share one identity | `cert_alias_surfaces_share_identity` (+ `CanonicalEntityID` owner in `capabilities/entities`) |
| G06 | 100 relations complete in data, ≤6 on screen | `cert_extractor_uncapped_visual_capped` |

Fundamental: naming two people in one paragraph creates no edge.

## 6. Visual Continuity (6)

| ID  | Test | Location (`overlays`) |
| --- | ---- | -------- |
| V01 | Toyota scenes 2/5/9 → one identity | `TestSIV01ThreeScenesCoherentIdentity` |
| V02 | alternative assets admitted, identity kept | `TestSIContinuityKeepsIdentityAcrossDistantScenes` |
| V03 | two companies never confused | `TestSIV03TwoCompaniesNeverConfused` |
| V04 | moved scene recalculates in new order | `TestSIV04MovedSceneRecalculatesContinuity` |
| V05 | missing asset → valid fallback | `TestSIV05MissingAssetFallsBackValid` |
| V06 | ten scenes rotate among equivalents | `TestSIV06TenScenesRotateAmongEquivalents` |

Continuity ≠ anti-repetition: MediaMemory owns reuse penalties; this
manager owns stable identity with rotating assets.

## 7. Recap (6)

| ID  | Test | Location (`overlays`) |
| --- | ---- | -------- |
| R01 | long video → placement points | `TestSIR01R02PlacementPoints` |
| R02 | short single-topic → no recap | same test (nil branch) |
| R03 | recap uses only narrated sentences | `TestSIR03RecapNeverLeaksLaterChapters`, `TestSIRecapOnlyCoveredContent` |
| R04 | equivalent bullets deduplicated | `TestSIR04EquivalentBulletsDeduplicated` |
| R05 | negations/attributions verbatim | `TestSIR05NegationsPreservedVerbatim` |
| R06 | chapter edit invalidates dependents | `TestSIR06ChapterEditInvalidatesDependents` (+ graph-level `recap:job` global in `script::TestSIInvalidationScene8Change`) |

Crucial: no sentence from later chapters leaks into an earlier recap.

## 8. End-to-End (10)

| ID  | Test | Location |
| --- | ---- | -------- |
| X01 | one index, one candidate per signal | `scripts::TestSIX01SingleIndexNoDuplicateExtraction` |
| X02 | 10 candidates, coherent admission | `scripts::TestSIX02TenCandidatesCoherentAdmission` |
| X03 | 20 scenes, varied + pertinent | `script::TestSISequenceTwentyScenesVariedAndPertinent` |
| X04 | EN→IT→DE same ids, local timing | `script::TestSICrossLanguageIdentityWithLocalTiming` |
| X05 | scene-8 change invalidates dependents | `script::TestSIInvalidationScene8Change` |
| X06 | 100 runs → identical manifests | `cert_manifest_stable_over_100_runs` |
| X07 | worker unavailable → typed error, no panic | `rustexec::TestDecodeSIFailSoftContract` |
| X08 | disabled → legacy invariant | `script::TestSIDisabledKeepsBaselineAdmission`, `scripts::TestSIProjectionDisabledIsNoop` |
| X09 | invalid relation rejected pre-render | `script::TestSIUnknownRelationRejectedBeforeRender`, `rustexec::TestDecodeSIRejectsUnknownRelation` |
| X10 | final plan inside timing, no collisions | `scripts::TestSIX10FinalPlanInsideTimingWithoutCollisions` |

## Verification levels

- **A. Invariants (100% mandatory):** offset integrity, no invented
  relations/values/timestamps, valid scene references, identifier
  integrity — every gate above fails closed.
- **B. Metamorphic:** C06 paraphrase, V04 reorder, D03 repetition,
  X06 determinism, X04 cross-language, X08 disabled-mode.
- **C. Quality on unseen data:** precision/recall/F1 vs baseline on an
  independent dataset + human recap review + visual readability sample —
  not in this tree; semantic 100% is not claimed.

## Performance baseline (measured, this machine)

`cargo bench --bench scene_intelligence` (lexical-only; no E5 path exists
by design — the E5 column applies to phrase-impact):

| words | scenes | sents | p50 | p95 | p99 | sent/s |
| ----- | ------ | ----- | --- | --- | --- | ------ |
| 1,020 | 11 | 88 | 2.01ms | 2.18ms | 2.18ms | 43,676 |
| 3,056 | 33 | 264 | 6.01ms | 6.22ms | 6.22ms | 43,937 |
| 10,000 | 108 | 864 | 19.74ms | 23.23ms | 23.23ms | 43,769 |

Peak RSS 3.3 MB. Scaling is linear (~44k sent/s). Set limits against
this baseline, never from invented millisecond targets.

## Render-side check on a real job (2026-10-10)

Payload `ops/jobs/garlasco_2000w_10overlays_shortphrases_20261010.generate.json`
(2000-word script, 10 short-phrase overlays, `overlay_style.font_family`
`inter_bold`), submitted through the canonical operator path
(`scripts/submit-and-poll.sh`). Three defects were found by running it and are
fixed in this tree; one blocker is outside this tree.

| # | Symptom seen live | Cause | State here |
| - | ----------------- | ----- | ---------- |
| 1 | `params.font_family "inter_bold" is unsupported` at overlay compile | the published schema/test pair and the runtime resolver disagreed about the selectable families; the deployed worker predated the resolver entry | fixed: `TestRuntimeStyleInterBoldIsSelectableAndRenderable` pins listing + lowering + asset presence; the enum contract test now names the 5 selectable families (`bricolage_grotesque` stays internal) |
| 2 | `workspace: invalid job id ... contains unsupported character` | `queue/client.ValidateJobID` accepted ASCII only, so an id derived from `comunità` failed at workspace materialization after the plan was paid for | fixed: letters/digits/marks in any script are accepted; separators, controls, symbols, traversal and invalid UTF-8 stay rejected (`TestValidateJobIDAcceptsAuthoredTextAndRejectsUnsafeComponents`) |
| 3 | `invariants + archcheck` | the new surface pushed two files past 600 LOC and two packages past their registered file budgets | fixed: `planner_config.go` split, numeric-value admission moved beside its caller, timing and overlay-prepare pairs merged; `go run ./cmd/archcheck --strict` reports 0 violations |
| 4 | `render: ipc render: daemon status Error (1) ... surface is not exportable to CUDA` | Chronon3d native render-graph surfaces were created without `desc.residency.exportable`, so the Direct-YUV handoff to the encoder rejected every text/number composition | **fixed outside this tree**: the Chronon3d surface-authority work landed at 18:51 and the same jobs started reporting `completed` |
| 5 | `official preset font "assets/fonts/Inter.ttf" is missing from the editorial_v1 bundle` | the shipped worker font bundle carried Poppins-Bold, Inter-Bold, Playfair and Bricolage but NOT `Inter.ttf` / `DejaVuSans.ttf`, so `inter` and `dejavu_sans` — both published, selectable families — could not render at all | fixed: the two faces (and `OFL-Inter.txt`) are staged in the bundle, and `processor::TestMaterializeBuiltinFontsStagesEveryRuntimeFontAsset` now stages EVERY asset `overlay.RuntimeFontAssets()` can return, so the guard fails on the exact live error when a family is unshippable |

Job states reached: `SCRIPT` → `TRANSLATION` → `VOICEOVER` → `AUDIO_COMPILE`
completed (17:04–17:07 on r18), overlay render then failed on defect 4. A later
attempt also failed once in scene-text validation (`segment[5] did not produce
one valid paragraph`) and once on an Ollama circuit breaker — LLM-side flakes,
not scene-intelligence behaviour, and both cleared on retry.

### The run that completed

After defects 1, 4 and 5 were fixed, the same payload (r22,
`job_1791658498906120876_b17e80d3`) reached **SUCCEEDED / progress 100**:

```
SCRIPT 18:54:59→18:56:13  TRANSLATION 18:56:13  VOICEOVER 18:56:13→18:57:24
AUDIO_COMPILE 18:57:24→18:58:25  overlay_render 18:57:34→18:58:18
PERSISTENCE 18:58:38  worker.execution COMPLETED 18:58:51
10/10 overlay.render jobs completed on attempt 1
```

### Render-side verification of that run

Plan properties (from the queue record of each render job):

| Property | Observed |
| -------- | -------- |
| overlays per run | 10 render jobs; 9 overlay items (5 `number` date cards, 4 `text_phrase`), `max_phrase_overlays: 4` / `max_image_overlays: 0` / `max_map_overlays: 1` respected |
| fonts asked for | `inter_bold` on 9/9 items — the family the worker used to reject (defect 1) |
| motion variety | 9 distinct motions over 9 items: `date_page_turn`, `date_timeline_tick`, `typewriter_modern_02_kinetic_scramble`, `image_scale_reveal`, `shutter_blade_reveal`, `typewriter_modern_09_highlighter_expansion`, `date_fade_rise`, `date_month_wipe`, `date_chronology_focus`, `precision_word_stagger` — no repeat |
| collisions | none: no two item windows overlap inside a plan |
| timing | every window inside the scene duration |
| phrases | 2, 2, 12 words in the plans read; the planner cap is `MaxPhraseWords` (default 20) and this payload sets no lower bound, so a 12-word card is inside policy — "short" is a payload knob, not a planner guarantee |
| cross-scene identity | not exercised by THIS payload: `max_image_overlays: 0`, so no image identity is planned to reuse |

Artifacts (downloaded from the object store and probed): all 10 are
1920×1080 H.264 segments whose frame counts match their declared durations
(120 frames = 5.000 s, 192 = 8.000 s, 65 = 2.708 s, …), 190 KiB–1.9 MiB, zero
download or probe failures.

Pixel-level proof the font override is honoured (not silently substituted):
the SAME composition submitted twice with only `params.font_family` changed
(`inter` vs `inter_bold`) renders two different frames — different frame
digests, PSNR 23.82 dB, SSIM 0.971 (same layout, different glyph raster). And
the whole selectable set renders live, one job per family:
`poppins`, `inter`, `inter_bold`, `dejavu_sans`, `playfair_display_italic`,
`bricolage_grotesque` — all `completed` on attempt 1.

What is still a HUMAN gate: judging whether the rendered cards look good. The
sample frames and segments are on disk (`/tmp/render-artifact-check`,
`/tmp/font-override-probe`), and the run published its artifacts through the
normal path.

### Plan-level artifacts of the real run

The queue still holds the overlay.render jobs the run submitted, each with the
compiled plan. Reading them (r19: 5 jobs, 4 overlay items) is the render-side
check that does not need an encoder:

| Property | Observed |
| -------- | -------- |
| fonts asked for | `inter_bold` on 4/4 items — the family the deployed worker used to reject; defect 1 is gone end to end up to the encoder |
| mixing | 2 `text_phrase` + 2 `number` (timeline date cards), `max_phrase_overlays: 4` / `max_image_overlays: 0` / `max_map_overlays: 1` respected |
| motion variety | `date_calendar_flip`, `date_timeline_tick`, `typewriter_modern_02_kinetic_scramble`, `masked_upward_reveal` — one per item, no repeats |
| collisions | none: no two item windows overlap |
| timing | every window inside the scene duration; no item exceeds its scene |
| phrase length | 2, 2 and 12 words. 12 is the longest; the planner's cap is `MaxPhraseWords` (default 20) and this payload sets no lower bound, so the plan is inside policy — "short" here is a payload knob, not a planner guarantee |
| cross-scene identity | nothing to reuse: the plan's images are 0 (`max_image_overlays: 0`), so continuity is not exercised by THIS payload |

The counts are partial by construction: the run died on the first render, so only
3 of the script's scenes were ever planned.

## Release criteria status

1. Invariants + regression green — this suite; `make verify-rust-muscles`
   (184 tests) and the five Go packages green after the file merges.
2. Semantic quality vs baseline on unseen scripts — automated subset green
   2026-10-10 (`cargo test --test si_level_c_unseen`, exit 0, 3/3 on 12 fresh
   sentences with topics/nouns never used by the unit suite): causal task
   exact-relation accuracy 6/6, causes-detection F1 1.0 vs naive
   marker==cause baseline 0.5; comparison task F1 1.0 vs co-mention baseline
   0.75 (qualitative zero values, quantitative exactly `[12]`); unseen negated
   ownership yields no `owns`; all 12 form a `validate_manifest`-valid
   manifest. Human recap review stays pending by design.
3. Candidates compatible with registry + planner — X02/X10 + admission tests.
4. Timing/identity/provenance across jobs + languages — X04/X05 + prior 5-system suite.
5. Perf/memory/reliability neutral — baseline above, remeasured 2026-10-10
   (`cargo bench --bench scene_intelligence`, exit 0): 1k p50 1.77ms /
   3k 5.26ms / 10k 17.42ms p95, ~50k sent/s, RSS 3.5MB, same linear shape,
   no regression. Planner/admission render path green: X02+X10 package
   0.117s, V/R 11/11 package 0.015s, five Go packages exit 0. The real run's
   overlay render cost is no longer blocked: 10/10 jobs completed in 44 s
   wall (`overlay_render` 18:57:34→18:58:18) for 9 overlay items.
6. Visual check on real rendered sample — artifacts verified programmatically
   (resolutions, frame counts vs declared durations, per-frame luma, and a
   font-override pixel diff). The human judgement of the rendered cards is the
   only part still open, with the samples left on disk for it.

## Repository gates after this work

```
make archcheck-strict        # 0 violations (was 3 errors + 1 warning)
python3 scripts/regen_hotspots.py --check   # fresh
make verify-rust-muscles     # 184 passed
cd RenderingGen/renderinggen && go test ./internal/overlay/ ./internal/processor/ ./internal/workspace/...
cd RenderingGen/queue && go test ./...
cd refactored && go test ./internal/kernel/script/ ./internal/capabilities/overlays/ \
  ./internal/capabilities/scripts/ ./internal/capabilities/scripts/overlay/ \
  ./internal/platform/media/rustexec/
```
