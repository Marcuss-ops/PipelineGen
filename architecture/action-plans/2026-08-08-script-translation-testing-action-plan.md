# Script Translation Testing — Action Plan (2026-08-08)

> **Authority**: this is the canonical narrative for the script.generate translation
> test surface. The wave-tracker entry (when it lands) is the canonical SOLE owner
> of the per-PR status flips; the smoke scripts in `tests/operational/` are the
> canonical SOLE owners of the operator-facing verification surface.

## §0 — Status Snapshot (godlike/07 NO-FAKE-AVAILABILITY)

The translation feature surface (`internal/application/scripts/usecase/translation.go` per
the prior `PR-TRANSLATE-SCRIPT-SPEC` closure) is **architecturally present** on `origin/main` but
**operationally under-tested** for the 6 high-priority invariants listed below. The canonical
`TranslateScriptSpec` function exists + the typed-error contract exists, but no hermetic TDD
suite verifies the load-bearing property:

> "After translation, the SpecScene JSON block in the Google Doc must be
> structurally identical to the source SpecScene (same scene count, same scene
> order, same `id`/`index`/`kind`, same `clip_id`/`drive_link` bindings) —
> only the text-bearing fields may differ in surface language."

The 7 priority tests in this plan collectively LOCK that invariant.

## §1 — Honest Limitation Disclosure

- The translation surface is presently exercised by **0 hermetic TDD tests** in
  `internal/application/scripts/usecase/translation_test.go` for the post-generation
  contract (per user-pasted 2026-08-08 audit).
- `tests/operational/script_translation_e2e_smoke.sh` does **NOT** exist on
  `origin/main` HEAD (verified via `git ls-files`); the smoke is a forward-pointer
  from this plan.
- The 6 priority tests are **deduplicated** from the 7+ raw test scenarios in the
  user-pasted text (the anti-LLM-JSON-key-translation scenario is folded into
  Test 2; the long-script + special-chars scenarios are folded into Test 6; the
  failure-mode scenarios are folded into Test 3).

## §2 — Goal

Ship **6 priority hermetic TDD tests** + **1 operator-facing e2e smoke** that
collectively verify the canonical translation contract:

1. The text changes (it changes language, not structure)
2. The structure does NOT change (scene count, scene order, all identifier-bearing
   fields are byte-identical pre/post translation)
3. The Google Doc is created with the translated text + the canonical SpecScene
   JSON block (NOT a translated JSON block)
4. Translation failures fail-closed (no Google Doc on empty/identical/whitespace-only
   translations)

## §3 — Per-PR Migration Sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Each per-PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches,
no `--no-ff`, no `--force`, `Co-authored-by:` trailer per Git-Lesson-3, race-protect
per Git-Lesson-4).

### PR-1 — `PR-TRANSLATE-SCRIPT-SPEC-TEST-PRESERVES-SPEC-SCENE` (P0)

**Scope**: Test 1 — `TestToTranslatedScript_PreservesSpecSceneStructure`.

- **Surface (2 files)**: NEW `internal/application/scripts/usecase/translation_structure_test.go`
  (~150 LoC, `package usecase_test` external) + EXTEND
  `internal/application/scripts/usecase/translation.go` with 1 NEW helper
  `stripBindingIdentifiers(s *ModelScriptOutputV1) *ModelScriptOutputV1` (private,
  no signature drift on `TranslateScriptSpec`).
- **Pre-fix state**: scene count + scene order + `id` + `index` + `kind` fields
  could silently mutate post-translation (no regression guard).
- **Post-fix state**: hermetic test pins 7 invariants:
  1. `len(translated.SpecScene.Scenes) == len(source.SpecScene.Scenes)`
  2. `translated.Scenes[i].ID == source.Scenes[i].ID` for all i
  3. `translated.Scenes[i].Index == source.Scenes[i].Index` for all i
  4. `translated.Scenes[i].Kind == source.Scenes[i].Kind` for all i
  5. `translated.Scenes[i].Bindings.Clip.ClipID == source.Scenes[i].Bindings.Clip.ClipID` for all i
  6. `translated.Scenes[i].Bindings.Clip.DriveLink == source.Scenes[i].Bindings.Clip.DriveLink` for all i
  7. `translated.Scenes[i].Text != source.Scenes[i].Text` for at least 1 scene (proves translation actually happened)
- **godlike/06 SSOT**: `stripBindingIdentifiers` lives ONLY at `translation.go`; the
  test surface lives ONLY at `translation_structure_test.go`.
- **godlike/07 NO-FAKE-AVAILABILITY**: the test uses hermetic in-memory state
  (no LLM, no Drive, no real translator — uses a `TranslatorFunc` that
  byte-prefixes `"IT: "` to every text field).
- **Verification**: `gofmt -l` clean; `go vet ./internal/application/scripts/usecase/...` exit 0;
  `go test -short -count=1 -run TestToTranslatedScript_PreservesSpecSceneStructure
  ./internal/application/scripts/usecase/` PASS.
- **Deadline**: 2026-08-15.

### PR-2 — `PR-TRANSLATE-SCRIPT-SPEC-TEST-DOES-NOT-TRANSLATE-JSON-KEYS` (P0)

**Scope**: Test 2 — `TestToTranslatedScript_DoesNotTranslateJSONKeys` + Test 3 (failure mode,
folded).

- **Surface (1 NEW file)**: `internal/application/scripts/usecase/translation_json_keys_test.go`
  (~220 LoC, `package usecase_test` external) — 2 hermetic TDD tests:
  - **Test 2a — JSON keys NOT translated**: feeds the canonical `translateScene` path
    with a translator that returns the SHAPE of an Italian-translated JSON block
    (`{"scena-1": {"tipo": "clip", "testo": "..."}}`); asserts `TranslateScriptSpec`
    either REJECTS the input (typed `ErrTranslationClipIDChanged` or
    `ErrTranslationDriveLinkChanged`) or REJECTS the OUTPUT (the structural-prevention
    strategy means the LLM NEVER sees JSON keys, so this test is a defense-in-depth
    regression guard).
  - **Test 2b — Failure modes (folded Test 7)**: 5 sub-cases — `translator==nil` →
    `ErrTranslationTranslatorMissing`; `targetLang==""` → `ErrTranslationTargetLangMissing`;
    `source==nil` → `ErrTranslationSourceInvalid`; translator returns `""` →
    `ErrTranslationEmpty`; translator returns whitespace-only →
    `ErrTranslationEmpty`; translator returns source verbatim → typed warning in
    `warnings` slice (not fail-fast per godlike/07 honesty).
- **godlike/06 SSOT**: the failure-mode typed sentinels live ONLY at
  `internal/application/scripts/usecase/translation.go`; the test surface lives
  ONLY at the new test file.
- **godlike/07 typed-error contract**: 5 typed sentinels all `errors.Is`-probeable;
  translator returning source verbatim is a soft warning (operator dashboard signal
  that the LLM is failing to translate, not a hard error).
- **Verification**: 6 sub-cases PASS; `errors.Is` probes return `true` for the
  expected sentinels.
- **Deadline**: 2026-08-15.

### PR-3 — `PR-TRANSLATE-SCRIPT-SPEC-TEST-PRESERVES-CLIP-BINDINGS` (P0)

**Scope**: Test 4 — `TestToTranslatedScript_PreservesClipBindings`.

- **Surface (1 NEW file)**: `internal/application/scripts/usecase/translation_clip_bindings_test.go`
  (~180 LoC) — 1 hermetic TDD test with 4 sub-cases:
  - 4a: 3 clips with 3 distinct `DriveLink`s → post-translation all 3 DriveLinks
    byte-equivalent to source
  - 4b: 3 clips with 3 distinct `ClipID`s (one with `drive_link` empty — the
    "no-Drive-URL yet" canonical state) → post-translation all 3 ClipIDs
    byte-equivalent, the empty `drive_link` stays empty
  - 4c: 1 clip with a YouTube-style URL (`https://www.youtube.com/watch?v=...`) →
    post-translation URL byte-equivalent (no LLM "translation" of the URL path)
  - 4d: 1 clip with a Drive-URL containing a long file ID (33+ chars) → post-translation
    file ID byte-equivalent
- **godlike/06 SSOT**: same `clip.Binding` field set as the production
  `ClipBinding` struct in `internal/domain/script/generation_result.go`; no
  re-declarations.
- **godlike/07 NO-FAKE-AVAILABILITY**: the test uses real-world URL patterns (Drive
  + YouTube) so a future refactor that strips/normalizes URLs would surface as
  test failure.
- **Deadline**: 2026-08-15.

### PR-4 — `PR-TRANSLATE-SCRIPT-SPEC-TEST-DOC-RENDERS-SPEC-SCENE-BLOCK` (P0)

**Scope**: Test 5 — `TestTranslatedScript_CreatesGoogleDocWithSpecSceneBlock`.

- **Surface (2 files)**: EXTEND
  `internal/application/scripts/adapters/processor_document.go` (no signature
  change; the canonical `BuildGenerationDocumentHTML` already accepts `SpecScene`
  + `Language`) + NEW
  `internal/application/scripts/adapters/processor_document_translation_test.go`
  (~200 LoC, `package adapters` internal to access unexported helpers) — 1
  hermetic TDD test asserting 7 invariants on the rendered HTML:
  1. `<h2>Script</h2>` present
  2. `<h2>Scenes</h2>` present
  3. `<h2>SpecScene JSON</h2>` present
  4. "Capitolo" (Italian chapter label) present when `Language=it`
  5. `clip.drive_link` URL present in the HTML
  6. "Capítulo" (Spanish chapter label) present when `Language=es`
  7. FORBIDDEN: NO `collegamento`, `tipo`, `testo`, `id_clip`, `link_drive` (any
    Italian-translated JSON key in the SpecScene block)
- **godlike/06 SSOT**: the test consumes the canonical `BuildGenerationDocumentHTML`
  signature verbatim (no test-only widening); the chapter-label localization
  table lives ONLY at `processor_document.go::chapterLabelForLanguage` (the
  canonical SOLE owner of the i18n strings).
- **godlike/07 NO-FAKE-AVAILABILITY**: the forbidden-keys list is a hard `assert.NotContains`
  (NOT a `strings.Contains` whitelist) — a future regression that adds a
  translated JSON key to the SpecScene block would surface as test failure.
- **Deadline**: 2026-08-22.

### PR-5 — `PR-TRANSLATE-SCRIPT-SPEC-TEST-LONG-SCRIPT` (P0)

**Scope**: Test 6 — `TestTranslatedScript_LongScript_NoSceneLossNoTruncation`.

- **Surface (1 NEW file)**: `internal/application/scripts/usecase/translation_long_test.go`
  (~250 LoC) — 1 hermetic TDD test with 4 sub-cases:
  - 6a: 10-scene source + translator that returns a full 10-scene translation →
    asserts `len(translated.Scenes) == 10` + all 10 scene text fields non-empty +
    10 translator calls recorded (1 per scene.Text + 1 per scene.Title = 20 total
    per the canonical contract)
  - 6b: 10-scene source + translator that returns 8-scene translation (truncated
    output) → asserts `TranslateScriptSpec` REJECTS via the canonical
    `ErrTranslationSceneCountMismatch` typed sentinel (the defensive guard
    added in PR-2 / Test 2b)
  - 6c: 10-scene source + 1 scene with empty `Text` in the translator output →
    asserts the all-or-nothing contract kicks in (the whole translation is
    rejected, NOT a partial-success)
  - 6d: 10-scene source + 1 scene with `Word count translated >= 70% of source`
    invariant (regression guard against the LLM truncating a scene to a single
    short sentence)
- **godlike/06 SSOT**: the word-count invariant lives ONLY at this test (no
  production code change); the all-or-nothing failure mode lives ONLY at
  `translation.go::TranslateScriptSpec` (already enforced by Test 2b's
  structural-prevention).
- **godlike/07 NO-FAKE-AVAILABILITY**: the 70% word-count threshold is a
  conservative soft invariant (the test logs a typed warning if a translation
  falls below 70% but does NOT hard-fail — per godlike/07 honesty, the LLM may
  legitimately compress in some languages).
- **Deadline**: 2026-08-22.

### PR-6 — `PR-SCRIPT-TRANSLATION-E2E-SMOKE` (P0, operator-facing)

**Scope**: Smoke 1 — `tests/operational/script_translation_e2e_smoke.sh`.

- **Surface (1 NEW file)**: `tests/operational/script_translation_e2e_smoke.sh`
  (~210 LoC, `bash -n` clean, `chmod 755` at commit time) — hermetic shell smoke
  modeled on the canonical `STK-E2E-*` precedent. The smoke:
  1. **Preflight**: server reachable on `$BASE/health` + `token.json` present
  2. **Step A — Generate EN script**: POST `/api/script/generate` with 3
     `clip_ids` + `language=en` + `generate_document=true` → wait for
     `SUCCEEDED` → extract `EN_doc_link` from the response + `EN_SpecScene` from
     the SQLite `scripts` row
  3. **Step B — Translate to IT**: call the canonical translation function
     (or invoke the `POST /api/script/translate` route when it lands) with
     `target_language=it` → wait for `SUCCEEDED` → extract `IT_doc_link` +
     `IT_SpecScene`
  4. **Step C — Assert structure preservation** (8 invariants):
     - `len(EN_SpecScene.scenes) == len(IT_SpecScene.scenes)`
     - `EN_SpecScene.scenes[i].id == IT_SpecScene.scenes[i].id` (for all i)
     - `EN_SpecScene.scenes[i].index == IT_SpecScene.scenes[i].index`
     - `EN_SpecScene.scenes[i].kind == IT_SpecScene.scenes[i].kind`
     - `EN_SpecScene.scenes[i].bindings.clip.clip_id == IT_SpecScene.scenes[i].bindings.clip.clip_id`
     - `EN_SpecScene.scenes[i].bindings.clip.drive_link == IT_SpecScene.scenes[i].bindings.clip.drive_link`
     - `EN_SpecScene.scenes[i].text != IT_SpecScene.scenes[i].text` (at least 1)
     - `IT_SpecScene.scenes[i].text` contains at least 1 Italian word
  5. **Step D — Assert Google Doc content** (4 invariants):
     - `EN_doc_link != ""` + `IT_doc_link != ""`
     - GET `IT_doc_link` HTML + assert `<h2>Capitolo</h2>` present
     - Assert no `collegamento` / `tipo` / `testo` strings (Italian JSON keys
       would indicate the LLM translated the JSON)
     - Assert the canonical `clip.drive_link` is present in the IT Doc HTML
- **godlike/06 SSOT (one canonical owner per fact)**: the smoke script lives
  ONLY at `tests/operational/script_translation_e2e_smoke.sh`; the canonical
  `VELOX_ADMIN_TOKEN` source is `.env`; the canonical translation route
  (when it lands) is `POST /api/script/translate` per the
  `PR-TRANSLATE-SCRIPT-SPEC-ROUTE` forward-pointer (or the smoke calls
  `TranslateScriptSpec` directly via the composition-root if the route has not
  landed yet).
- **godlike/07 NO-FAKE-AVAILABILITY**: every assertion probe has a canonical
  FAIL→PR-TRANSLATE-SCRIPT-SPEC-* forward-pointer mapping per the
  `architecture/notes/script-legacy-to-v2-mapping.md` precedent.
- **godlike/07 minimum-blast-radius**: re-bashable per-run via
  `REQ_ID="script_translate_$(date +%s)"` (idempotency contract preserved).
- **Deadline**: 2026-08-22.

## §4 — Per-PR Execution Checklist

For each per-PR above:

1. **Gather context** via `code-searcher` + `read_files` for the existing
   `internal/application/scripts/usecase/translation.go` surface + the canonical
   `ModelScriptOutputV1` + `SpecScene` types in `internal/domain/script/`.
2. **Implement** the TDD test in the canonical location (see PR-1..PR-5 above)
   following the existing `processor_*_test.go` style (3-col-group assertions
   + sub-test for each invariant).
3. **Verify gates** in this order:
   - `gofmt -l <test-file>` clean
   - `go vet ./internal/application/scripts/usecase/...` exit 0
   - `go build ./cmd/server/ ./cmd/worker/` exit 0
   - `go test -short -count=1 -run <TestName> ./internal/application/scripts/usecase/` PASS
   - Full `go test -short ./internal/application/scripts/usecase/` PASS (no pre-existing test broken)
4. **3-surface godlike/06 SSOT lockstep** (per CANONICAL.md §1):
   - CHANGELOG.md `## Unreleased > ### Added` entry (closure meta-entry)
   - AGENTS.md `## Recent cross-cutting closures` mirror entry (this entry pattern)
   - `architecture/waves/wave_p1_high.yaml` wave-tracker slot flip (DEFERRED per
     pre-existing `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward; forward-pointer
     `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15)
5. **Commit + push directly to `main`** per AGENTS.md Git-Lesson-2 (no branches,
   no `--no-ff`, no `--force`); `Co-authored-by:` trailer per Git-Lesson-3;
   race-protect via `git fetch && git log --oneline HEAD..@{u}` per Git-Lesson-4.

## §5 — Verification Gates (godlike/07 minimum-blast-radius)

Per-PR gates (per §4 step 3 above).

Wave-level gate (per the canonical wave-flip criterion):

- All 6 priority TDD tests PASS
- `tests/operational/script_translation_e2e_smoke.sh` exits 0 against a live
  PipelineGen server
- `gofmt -l` clean on all 7 files (5 test files + 1 new helper + 1 smoke)
- `go vet ./...` exit 0 on the targeted subtrees
- `go test -short -count=1 ./...` exits 0 (no pre-existing test broken)

## §6 — Honest Scope-Lock (godlike/07 minimum-blast-radius)

**Out of scope for this plan**:

- **Production code change to `TranslateScriptSpec` itself** — the function is
  already shipped per the prior `PR-TRANSLATE-SCRIPT-SPEC` closure; this plan
  is **test-only** (Hermetic TDD surface + operator-facing smoke).
- **Voiceover from translated scenes** — folded into a future
  `PR-TRANSLATE-SCRIPT-SPEC-VOICEOVER-CHAIN` wave; not in this plan.
- **Live LLM integration** — the 6 TDD tests use deterministic hermetic translators
  (string-prefix or echo); the smoke uses a real LLM ONLY via the production
  composition root (the smoke is operator-facing, not CI).
- **Multi-language coverage** — the 6 TDD tests cover EN→IT (canonical) + IT
  language; the smoke adds ES as a 2nd target language (per the
  `chapterLabelForLanguage` i18n coverage). Additional languages
  (pt-BR + fr + de) are forward-pointers.

**Carry-forward unchanged, NOT regressions**:

- The 5-item voiceover + app build-issue list per
  `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
- The pre-existing YAML parse error in `architecture/waves/wave_p1_high.yaml`
  (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15)
- The untracked `internal/application/scripts/pipeline_postprocess_e2e_test.go`
  file (left as-is; not authored by this plan)

## §7 — Cross-References (godlike/06 SSOT umbrella)

- `architecture/waves/wave_p1_high.yaml#PR-TRANSLATE-SCRIPT-SPEC` (parent wave
  entry, DEFERRED per pre-existing YAML parse carry-forward) — canonical SOLE
  owner of the 6 per-PR status flips
- `internal/application/scripts/usecase/translation.go` (canonical SOLE owner of
  `TranslateScriptSpec` + 7 typed sentinels) — surface under test
- `internal/domain/script/generation_result.go` (canonical SOLE owner of
  `ModelScriptOutputV1` + `SpecScene` + `ClipBinding` types) — types under test
- `internal/application/scripts/adapters/processor_document.go` (canonical SOLE
  owner of `BuildGenerationDocumentHTML` + `chapterLabelForLanguage` i18n table)
  — surface for PR-4
- `architecture/notes/script-legacy-to-v2-mapping.md` (canonical SOLE owner of
  the v1→v2 field mapping; PR-4 references the chapter-label canonical
  reference) — audit-pin for the i18n table coverage
- `tests/operational/stock_e2e_full_battery.sh` (canonical SOLE owner of the
  aggregator-shell-smoke pattern) — template for PR-6's smoke
- `architecture/action-plans/2026-08-08-postprocessor-tdd-guard.md` (sister plan
  with same TDD-guard pattern) — format precedent for this plan
- AGENTS.md Git-Lesson-2/3/4 (direct-to-main + Co-authored-by + race-protect)

## §8 — Wave-Flip Criterion (godlike/06 SSOT)

This wave flips to `status: shipped + exit_signal: true` ONLY when:

1. All 6 per-PR closures (PR-1 through PR-6) reach `status: shipped` on
   `origin/main` (verifiable via `git branch -r --contains <ship_sha>` for each)
2. The full e2e smoke (`tests/operational/script_translation_e2e_smoke.sh`)
   exits 0 against a live PipelineGen server + the canonical VELOX_ADMIN_TOKEN
3. The 6 hermetic TDD tests (PR-1..PR-5) PASS in `go test -short -count=1` on
   the targeted subtrees
4. Zero new pre-existing build issues are surfaced (the 5-item voiceover +
   app build-issue carry-forward list remains UNCHANGED)
5. Zero pre-existing TDD test failures (the carry-forward test failures per
   `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
   reproduce identically on pre-PR + post-PR trees)

The wave-tracker entry flip is **DEFERRED** per the pre-existing
`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline
2026-08-15). The 3-surface lockstep is preserved in the CHANGELOG + AGENTS
+ this action plan until the YAML parse is repaired.

## §9 — Lifecycle Audit-Trail (godlike/06 SSOT one-canonical-owner-per-fact)

This action plan is the canonical SOLE narrative owner of the script-translation
testing migration. Per-PR ship SHAs are the canonical SOLE owners of the
per-PR code surfaces. The CHANGELOG.md + AGENTS.md mirror entries are the
canonical SOLE documentation surfaces. The wave-tracker entry (when it lands)
is the canonical SOLE status surface.

Audit-pin discipline (godlike/07): if a per-PR's ship_sha is NOT on
`origin/main` at lockstep-write time, the per-PR entry is annotated
`ship_via: AUDIT_PIN_FOR_PRE_SHIPPED_FIX` per the established codebase
precedent (see `PR-CLEANUP-HOTSPOT-CROSSREF` + `PR-P12-HOTSPOT-CROSSREF` +
`PR-AUTH-CREDENTIAL-HELPER-SETUP`).

## §10 — Co-authored-by

`Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`

AGENTS.md Git-Lesson-3.
