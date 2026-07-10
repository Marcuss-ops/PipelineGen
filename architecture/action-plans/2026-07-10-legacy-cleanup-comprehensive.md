# Legacy Cleanup Comprehensive Plan (2026-07-10)

> **Authoritative surface**: this file is the SOLE canonical owner of the comprehensive
> legacy-cleanup migration sequence derived from the user-pasted Italian audit of
> origin/main @ `0976108e` (the TTS-bug-fix commit). The 12-item audit + the 4
> "altri cleanup" items + the "campi API" section + the explicit "non vanno
> eliminare" guardrails are all enumerated below per **godlike/06 SSOT
> one-canonical-owner-per-fact** discipline.

> **Wave-rollup vs prior action plan**: the prior
> `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md`
> shipped 5 of the 12 items via 4 atomic commits on `origin/main`. This
> comprehensive plan supersedes that orchestration for the **remaining 7
> items + 4 "altri cleanup" items + "campi API" section + guardrails** —
> the prior 5 items are listed here ONLY as audit-pin annotations
> referencing the canonical SHAs on `origin/main` (no further code
> changes required for them).

---

## §0 — Status snapshot (2026-07-10, anchor: origin/main @ 2ebb24ea)

**Honest scope-lock (godlike/07 NO-FAKE-AVAILABILITY)**: the user-pasted
audit covers 12 distinct items + the "campi API" field-collapse section
+ the "altri cleanup" forward-pointer section + the explicit "non vanno
eliminare" guardrails. The 5 items already shipped via the prior
`LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10` wave are summarized in
`§10.audit-pin registry` below (audit-pin discipline per godlike/07
NO-FAKE-AVAILABILITY — the action plan records the canonical work, not
re-ships it).

**Already-shipped (5/12 — audit-pin via §10 below)**:

| # | Audit item | Canonical ship_sha | Source commit |
|---|------------|-------------------|---------------|
| 1 | `/api/images/animate` route retirement | `4ebf1bfdf` | legacy-cleanup-2026-07-10 Item 1 |
| 2 | `PostProcessArtifact = any` alias retirement + `dto/compat_types.go` deletion | `fefaa7d48` | legacy-cleanup-2026-07-10 Item 2 |
| 3 | `vector_search:` yaml block elimination | `cc8225e68` | legacy-cleanup-2026-07-10 Item 3 |
| 4 | `status: removed` records → `architecture/archive/deprecations-removed-2026-07-10.yaml` | `b7d73a18` + `7e25e58a9` (lockstep) | legacy-cleanup-2026-07-10 Item 4 |
| 5 | Fullimages catena orfana — **Option B chosen** (images-only, retire Ken Burns + 6 helpers) | `b95aceadb` | legacy-cleanup-2026-07-10 Item 5 |

**Remaining scope (this action plan addresses)**:

* Items 6, 7, 8 — **NEW** no-fake-availability + typed-struct cleanup
  (Item 7 + 8 are NEW findings vs prior audit).
* Items 9, 10 — **scheduled retirements** (post-metric-zero + post-deadline,
  canonical disposition is "leave tombstone, schedule on calendar",
  not DELETE today).
* Items 11, 12 — **P1 + backfill-gated** retirements.
* **Campi API senza effetto** (`OutputSpec.Generate*` + `ArtifactResult.Document`)
  — coordinated wire-shape collapse across request + normalisation +
  response in a single commit (per user spec literal "non vanno rimossi
  singolarmente").
* **Altri cleanup giá tracciati** — 4 forward-pointers (mediasearch
  X-Deprecation + Qdrant DTO duplicates + Drive Admin FileLifecycle
  fallback + `uploader_deprecated.go`).
* **Non vanno eliminate** — explicit guardrails (migration SQLite +
  `legacyState*` in `index_state.go` + `qdrant/legacyaudit` +
  `architecture/archive`).

---

## §1 — Honest scope-lock (godlike/07 NO-FAKE-AVAILABILITY)

The user-pasted audit is a STATIC analysis of repository state at
commit `0976108e`. The audit's quantitative claims (e.g. "0 riferimenti",
"single caller", "production-only") reflect a point-in-time state that
MUST be re-verified via `rg` sweep ON EACH SEPARATE RUN per the
canonical `architecture/action-plans/2026-07-08-script-pipeline-contract.md`
verification protocol (§5 Verification gates).

**The audit sometimes drifts vs ground truth** — for example the audit
claimed Item 6 has "nessun impatto sui test" but the prior Item 2 commit
shipped a migration where the test was updated. Future agents landing
per-PR closures in this plan MUST:

1. Run the canonical pre-flight rg sweep from §5 below BEFORE editing
   any file.
2. Confirm the user's stated "one caller" / "zero callers" claims in the
   audit by re-running the same rg query on `origin/main`'s current HEAD.
3. If the rg result CONFIRMS the audit, ship the per-PR closure
   (`fix(scripts): commit message via go test -short`).
4. If the rg result CONTRADICTS the audit (e.g. the function has 3
   callers, not 1), ship the closure as a 0-action **CLAIM-DRIFT
   AUDIT-PIN** (per the established pattern of PR-PROMO-STRICT-
   ACCOUNTING-MIGRATION-GATE + FASE-11-V3-Option-C audit-pin
   precedent).

The 0-action CLAIM-DRIFT AUDIT-PIN pattern is load-bearing — it
prevents drifting spec-vs-reality work from manufacturing
fictitious-callers to fit a calendar deadline (canonical godlike/07
NO-FAKE-AVAILABILITY).

---

## §2 — Goal

Bring `origin/main` to a state where:

* Every retired legacy surface has either (a) physically gone via the
  canonical retirement commit + audit-pin closure record, OR (b) a
  scheduled retirement on file with the canonical deadline
  (2026-09-26 / 2026-12-31 / Q3 2026 / Q4 2026) + the canonical
  Prometheus counter for the death-watch window.
* Every 410-Gone tombstone returns a typed `LegacyDeprecationPayload`
  per AGENTS.md Pattern 9.
* Every "missing backend silent-success" class is structurally
  eliminated (per Items 7 + 8) so a future operator does NOT confuse
  "no results" with "backend absent" + returning nil.
* Wire-shape collapses (`campi API senza effetto`) are coordinated
  across all 4 sites of the canonical contract (request envelope +
  payload normaliser + response aggregator + test fixture) in ONE
  atomic commit, per user spec literal "non vanno rimossi
  singolarmente".

---

## §3 — Migration sequence (5 phases × per-PR auto-sufficient)

Each per-PR closure lands DIRECTLY on `main` per AGENTS.md Git-Lesson-2
(NO branches, NO `--no-ff`, NO `--force`); each ships its own 3-surface
lockstep (CHANGELOG.md + AGENTS.md + `architecture/current.yaml` wave-tracker
slot where parseable). Wave-tracker slot flips are DEFERRED per the
pre-existing `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer
(deadline 2026-08-15) — until that parse-fix lands, the parent
action-plan + CHANGELOG + AGENTS entries are the canonical SOLE
closure record per the established `PR-POSTPROCESSOR-UNIFICATION-PHASE-4`
+ `SCRIPT-CONTRACT-2026-07-08` + `LEGACY-CLEANUP-5-ITEM-ORCHESTRATION`
precedent.

### Phase A — P0 no-fake-availability (deadline 2026-07-22)

#### PR-LEGACY-NA-1: retire `unavailableArtlistClipSearcher` (Item 7)

**Pre-flight (mandatory)**: `rg '\.SearchClips\(' --type go` MUST return
zero hits on the unavailable adapter path (§5 refines this query).

**Surface** (3 files):

1. `internal/application/scripts/adapters/compat_adapters.go` — REMOVE
   the 8-line `unavailableArtlistClipSearcher` struct + the 6-line
   `NewUnavailableArtlistClipSearcher` ctor + the Download interface
   signature { 3 methods }.
2. `internal/app/wire_script_postprocess.go` — REMOVE the
   `phraseSearcherAdapter := NewUnavailableArtlistClipSearcher()`
   fallback literal (canonical `*artlist.SceneSynthesizer.PhraseSearcherAdapter`
   in the success branch stays).
3. `internal/app/wire_script_postprocess.go` — UPDATE the
   `phraseTranslatorAdapter` chain to use the typed `*artlist_phrase.PhraseTranslator`
   (`phrase.NewService(...)` per `PR-POSTPROCESSOR-UNIFICATION-PHASE-4`
   ship_sha `4c4550259`).

**Per-PR godlike/06 SSOT invariants** (preserved across the
        retirement):

* The canonical SOLE owner of `ArtlistClipMatch` lives ONLY at
  `internal/application/scripts/ports/asset_search_port.go` (the typed-port
  Pattern 0 contract; alias was the legacy stub).
* `phrase.NewService(...)` returns nil + `ErrTranslatorNil` per the FASE-4
  fail-closed pattern — composition root MUST respect this canonical
  error contract.

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**: when the
artlist backend is absent, the postprocessor-registration MUST skip
the `ClipSearchProcessor` entirely (canonical `pol := ProcessorBestEffort`
gating in `registerScriptPostProcessors` already short-circuits if the
backend returns nil — verify the wire-up respects this). A future
operator MUST NEVER see a non-empty `ClipSearchProcessor.Run` output
generated by a missing-backend stub.

**Verification gates (mandatory)**:

* `rg 'unavailableArtlistClipSearcher|NewUnavailableArtlistClipSearcher|SearchClips'`
  returns 0 hits post-edit.
* `go test -short ./internal/application/scripts/...` PASS.
* `bash scripts/ci-architectural-checks.sh` exit 0.

**Forward-pointer** (godlike/07 OUT OF SCOPE here):

* `PR-ARTLIST-COMPOSITION-FAIL-CLOSED` (deadline 2026-08-15) — fully
  wire the `artlist_phrase.NewService` composition root surface
  + add a compositional-side fail-closed gate test pinning the
  "nil-backend → processor skipped" invariant.

#### PR-LEGACY-NA-2: retire `NewUnavailableEntityExtractionAdapter` + `NewUnavailableMetadataGenerationAdapter` (Item 8)

**Pre-flight (mandatory)**: `rg 'EntityExtractor\|MetadataGenerator'`
MUST scope to the typed-port contract (NOT the unavailable adapter
        code path).

**Per-PR godlike/06 SSOT invariants**: the canonical typed-port
contracts `EntityExtractor` + `MetadataGenerator` (2-port Pattern 0)
live ONLY at `internal/application/scripts/ports/asset_search_port.go`;
the unavailable adapter stubs are code-motion retire (`-12 LoC`
approx).

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**:

* `BestEffort` policy → composition-time SKIP the processor (NOT
  register an unavailable adapter).
* `Required` policy → composition-time FAIL-CLOSED the boot
  (typed `ErrProcessorRequired` sentinel; per PR script-legacy-contract
  ship_sha `461b71a4` + `PR-PROMOTE-REQUIRED-FIX` ship_sha `e68bb859f`).

**Verification gates**: identical to PR-LEGACY-NA-1.

#### PR-LEGACY-ENG-1: collapse `Section.Engine` field (Item 6 — P1)

**Pre-flight (mandatory)**: `rg 'Section.Engine\|google-vids'` MUST
scope to the fullimages + images handlers ONLY.

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**:

* Phase 1 (this PR): handler returns `http.StatusBadRequest` (`400`)
  when `engine` field is present in request body.
* Phase 2 (forward-pointer PR-REQUESTSCHEMA-ENGINEREMOVAL —
        deadline TBD): remove the field from `Section` DTO entirely +
        delete `ErrEngineRetired` + the gate loop in handler.

### Phase B — Scheduled retirements (per calendar deadline, NO code change today)

#### PR-LEGACY-RETIRE-IMAGES-GENERATE-tombstone (Item 9, deadline 2026-12-31)

This PR is NOT TO BE SHIPPED TODAY. The canonical disposition is
"leave the 410-Gone tombstone alive until the Prometheus counter
`legacyImagesGenerateTotal` reports 0 sustained over 7 days". At the
deadline, a separate closure (PR-LEGACY-RETIRE-IMAGES-GENERATE-FINAL
— TBD) will physically git-rm:

* `internal/api/images/legacy_generate_handler.go`
* The `POST /api/images/generate` route registration in
  `internal/api/images/routes.go` (or whatever its current home is)
* `legacyImagesGenerateTotal` Prometheus counter
* The 410 deprecation tests
* The legacy deprecation constants
* The deprecation header + 410 response shape
* The references in the AGENTS.md "Common Operations" section

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants** (load-bearing,
this is why this stays a TOMBSTONE not a DELETE):

* The metric-counter wait is the canonical death-watch; without it,
  a future operator might lose a caller silently.
* The retiral PR is gated on a NEW canonical successor function
  (the v2 api surface at `POST /api/images/generated/generate`)
  crossing the user's specific adoption threshold (currently
  undefined; operator-set, not code-set).

#### PR-LEGACY-RETIRE-VOICEOVER-GENERATE-WITH-GROUP-tombstone (Item 10, deadline 2026-09-26)

Same canonical pattern: the existing 410-Gone tombstone stays
unchanged today; at 2026-09-26 a follow-up PR will:

* Physically git-rm the handler.
* Update AGENTS.md references
  ("`POST /api/voiceover/generate-with-group`" → "`POST /api/voiceover/generate` with
  `destination.kind=group`").
* Remove the canonical Prometheus counter.

**Forward-pointer** (calendar-gated):
`PR-LEGACY-RETIRE-VOICEOVER-GENERATE-WITH-GROUP-FINAL` (deadline
2026-09-26) — auto-triggered by the calendar, not by code-asset
completion.

### Phase C — API field collapse ("campi API", deadline 2026-08-08)

#### PR-LEGACY-FIELDS-COLLAPSE-1 (campi API section)

**Pre-flight (mandatory)**: `rg 'GenerateVoiceover\|GenerateSceneImages\|GenerateDocument\|ArtifactResult.Document\|Artifacts\.Document'`
MUST scope to the canonical 4 sites the user spec calls out.

**Per-PR godlike/06 SSOT invariants** (the user spec is explicit):
"richiesta, normalizzazione e risposta devono cambiare nello stesso
commit" — the canonical 4-file change is atomic:

1. Request envelope (`OutputSpec` — likely `internal/application/scripts/dto/types.go`)
2. Payload normaliser (typically `internal/application/scripts/usecase/generation_normalizer.go:308 isUnsetToggle` — verify via rg)
3. Response aggregator (typically `internal/application/scripts/adapters/postprocessor_document.go` — verify via rg that the `PipelineResult` writer chain references these fields)
4. Test fixture (typically `internal/application/scripts/.../*_test.go`)

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**:

* 4 sites MUST change atomically (otherwise a wire-shape drift
  surfaces — handler returns 200 with field absent but normaliser
  panics or vice versa).
* The collapse keeps field-zero semantics (omitempty) — empty
  payload equivalent to "absent" — so old clients passing
  `generate_voiceover=true` continue to receive voiceover
  (the new policy default) without breakage.

**Verification gates**:

* `rg 'GenerateVoiceover\|GenerateSceneImages\|GenerateDocument\|ArtifactResult.Document\|Artifacts\.Document'`
  returns 0 hits in `internal/api/` + `internal/application/scripts/dto/`
  + `internal/application/scripts/usecase/generation_normalizer.go`
  + `internal/application/scripts/adapters/postprocessor_document.go`.
* `go test -short ./internal/application/scripts/...` PASS.

### Phase D — P1 + backfill-gated retirements (deadline 2026-08-15)

#### PR-LEGACY-TRANSLATION-PORTS-RETIRE (Item 11, completion Q4 2026)

The 3 legacy translation ports `LegacyTextTranslationService` +
`LegacyTranslatorService` + `LegacyMetadataTranslator` + the
`LegacyTranslator` alias in `internal/application/translation/legacy.go`
+ the type alias in `internal/application/scripts/usecase/services.go` +
the legacy methods on the concrete `OllamaTranslator` + the dedicated
test files MUST all be migrated to the canonical `translation.TranslationPort`
+ companion `MetadataTranslator` BEFORE this retiral.

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**:

* Step 1 (this PR): the canonical migration of every caller
  (`scripts/usecase/*` + tests) from the legacy port
  signatures to `TranslationPort` is COMPLETE.
* Step 2 (this PR): the canonical 7-DAY SOAK window completes:
  zero production log records of `LegacyTextTranslationService`
  usage in the canonical trace. (Operator-set, not code-set.)
* Step 3 (this PR): the godlike/07 typed-error migration is
  COMPLETE: every caller reads `TranslationError` via `errors.As`
  per the AGENTS.md Pattern 0 discipline.

**Verification gates**:

* `rg 'LegacyTextTranslationService\|LegacyTranslatorService\|LegacyMetadataTranslator\|LegacyTranslator'`
  returns zero hits in production code.
* `rg 'translation\.TranslationPort\|TranslationError'`
  reaches >= 1 hit per consumer site.

**Forward-pointer** (calendar-gated):
`PR-LEGACY-TRANSLATION-FINAL-RETIRE` (deadline Q4 2026) — at calendar
deadline: physically git-rm `internal/application/translation/legacy.go`
+ aliases + concrete methods + tests.

#### PR-LEGACY-JSON-ARRAY-BACKFILL (Item 12, backfill-gated completion)

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants** (the user spec is
        explicit):

* Step 1: monitor `jsonextract_legacy_array_fallback_total{source="cache"}`
  Prometheus counter.
* Step 2: identify and convert the pre-V1 cache rows via a one-shot
  backfill CLI (NOT git-rm yet).
* Step 3: observe the Prometheus counter at zero over a 7-day window.
* Step 4: split `legacy_converter.go` into `plain_text.go` +
  `legacy_array_converter.go` (so the modern `ParsePlainTextFresh`
  path stays co-located with its tests).
* Step 5: physically delete `legacy_array_converter.go` + the
  `ModeCompatibility` flag + `legacyScene` DTO + `convertLegacyArray`
  function + the legacy dedicated tests.

### Phase E — "altri cleanup" forward-pointers (4 items, deadline 2026-08-22)

Each item gets a separate PR (these are NOT collapsed; they have
different canonical owners per godlike/06 SSOT).

#### PR-MEDIASEARCH-LEGACY-TRANSLATOR-RETIRE (Q3 2026 deadline)

* Retires the legacy response translator + the `X-Deprecation`
  header in `internal/api/mediasearch/`.
* Canonical owner: `internal/api/mediasearch/` (per godlike/06).

#### PR-QDRANT-DTO-DEDUP (Q4 2026 deadline)

* Removes the duplicate DTO mirror between
  `internal/application/qdrant/...` + `internal/infrastructure/qdrant/...`.
* Requires introducing a shared types package upstream.
* Canonical owner: shared `_packages/qdrant` (NEW) — proposed.

#### PR-DRIVE-ADMIN-FALLBACK-CUTOVER

* Cuts the `Admin.UploadFile`-via-`FileLifecycle` fallback in the
  4 adapter sites (typically `internal/application/assets/...`).
* Requires `FileLifecycle` to be canonical-available in all
  dependency bags.
* Canonical owner: `delivery.FileLifecycle` (already shipped).

#### PR-UPLOADER-DEPRECATED-RETIRE

* Physically git-rm `internal/infrastructure/drive/uploader_deprecated.go`
  after ALL its `UploadFile*` + `doUploadFile` callers have migrated
  to `delivery.Publisher.Publish`.
* Canonical owner: `internal/infrastructure/drive/` (the canonical
  publisher package).

---

## §4 — Per-PR execution checklist (template)

For every PR in §3:

1. **Pre-flight `rg` sweep** (canonical queries in §5 below).
   If the rg result CONFIRMS the audit → ship. If it CONTRADICTS →
   ship CLAIM-DRIFT AUDIT-PIN (per §1).
2. **Verify gate** (gofmt + go vet + go test -short) on the targeted
   subtree ONLY (NOT `go build ./...`, per godlike/07 minimum-blast-radius
   — the pre-existing 6-item voiceover + app build-issue carry-forward
   would surface false-positive regressions).
3. **Commit directly on `main`** per AGENTS.md Git-Lesson-2
   (NO branches, NO `--no-ff`, NO `--force`, NO PR).
4. **Co-authored-by trailer** per AGENTS.md Git-Lesson-3.
5. **3-surface lockstep** (CHANGELOG.md entry + AGENTS.md `Recent
   cross-cutting closures` mirror + canonical ship_sha on `origin/main`).
6. **Race-protect** per AGENTS.md Git-Lesson-4 (`git fetch origin && git
   log --oneline HEAD..@{u}` MUST be empty for safe ff-push).
7. **Wave-tracker slot flip in `architecture/waves/wave_p1_high.yaml`**
   is DEFERRED per the pre-existing
   `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
   carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer
   (deadline 2026-08-15) until the parse fix lands — the CHANGELOG +
   AGENTS + canonical ship_sha are the canonical SOLE closure record
   per the established precedent.

---

## §5 — Verification gates (canonical commands)

### Per-item rg queries (run BEFORE editing any file)

```bash
# --- Item 6: Section.Engine field precondition
rg 'Section\.Engine|ErrEngineRetired' --type go -g '!*_test.go' -g '!CHANGELOG.md' -g '!AGENTS.md' -g '!architecture/'

# --- Item 7: unavailableArtlistClipSearcher precondition
rg 'unavailableArtlistClipSearcher|NewUnavailableArtlistClipSearcher'

# --- Item 8: Entity + Metadata unavailable adapter preconditions
rg 'NewUnavailableEntityExtractionAdapter|NewUnavailableMetadataGenerationAdapter'

# --- Item 11: Legacy translation ports precondition
rg 'LegacyTextTranslationService|LegacyTranslatorService|LegacyMetadataTranslator|LegacyTranslator'

# --- Item 12: legacy JSON array precondition
rg 'convertLegacyArray|ModeCompatibility|legacyScene'

# --- Campi API: 4-site wire-shape collapse precondition
rg 'GenerateVoiceover|GenerateSceneImages|GenerateDocument|ArtifactResult\.Document|Artifacts\.Document' --type go

# --- Phase E altri cleanup preconditions
rg 'X-Deprecation|legacy.*translator' internal/api --type go
rg 'admin_|Admin' internal/infrastructure/drive/uploader_deprecated.go
rg 'FileLifecycle.Admin|Admin\.UploadFile' internal/application --type go
```

### Go gates (run on the targeted subtree, NOT `./...`)

```bash
gofmt -w internal/application/scripts internal/application/translation internal/api/mediasearch internal/infrastructure/drive

go test -short ./internal/application/scripts/...
go test -short ./internal/application/translation/...
go test -short ./internal/api/mediasearch/...
go test -short ./internal/infrastructure/drive/...

go build ./cmd/server/...    # cross-package compile verifies
go build ./cmd/worker/...    #   cross-package compile verifies
```

### Architecture gate (per AGENTS.md Pattern 5 + verify scripts)

```bash
bash scripts/ci-architectural-checks.sh    # informational exit-0; pre-existing
                                           # backwards-compat tests allow
                                           # the script to exit 0 even on
                                           # residue per the in-script
                                           # waiver logic.
```

### Post-edit rg queries (run AFTER every PR)

The canonical post-edit rg MUST return exactly the audit-pin's expected
hits: a minimal set of references (often 1 — the audit-pin goddoc itself)
that document the canonical retirement.

If a post-edit `rg` returns UNEXPECTED new hits (especially in
production code outside the audit-trail godoc) → the per-PR closure has
INTRODUCED a new caller → revert + ship-again.

---

## §6 — Honest scope-lock (godlike/07 minimum-blast-radius)

* This action plan documents the migration sequence for 7 remaining
  items + 4 "altri cleanup" items + the "campi API" + guardrails.
  Each PR is auto-sufficient with its own §4 checklist + §5 verification.
* The wave-tracker slot flip in `architecture/waves/wave_p1_high.yaml`
  remains DEFERRED per the pre-existing
  `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
  carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer
  (deadline 2026-08-15) — the canonical SOLE closure record is the
  CHANGELOG + AGENTS + canonical ship_sha pattern per the established
  `PR-POSTPROCESSOR-UNIFICATION-PHASE-4` precedent.
* Pre-existing 6-item voiceover + app build-issue carry-forward per
  `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
  must be preserved unchanged across this wave — NOT a regression of this
  action plan.
* Pre-existing dirty working-tree residue from prior sessions
  (per AGENTS.md pre-existing-build-issues convention) is OUT OF SCOPE
  for the per-PR closures in §3 — each per-PR closure scopes its
  modifications to its targeted subtree + the canonical 3-file lockstep
  surfaces only.

---

## §7 — Cross-references (godlike/06 SSOT umbrella)

* `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md`
  — the prior orchestration that shipped 5/12 items (canonical source for
  the audit-pin registry in §10 below).
* `architecture/action-plans/2026-07-10-dead-code-p1-p2-cleanup.md`
  — sister plan for the related `scriptdto.PipelineResult` deprecation
  + `legacyScene` migration.
* `architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md`
  — the 3-refactor (composition monolith + global-state + blocking-IO)
  forward-pointer surface.
* `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
  — the parse-error carry-forward that DEFERs wave-tracker slot flips
  for THIS plan + the prior orchestration.

  * Forward-pointer: `PR-CURRENT-YAML-PARSE-FIX-PART-N` (deadline
    2026-08-15) — when this lands, the wave-tracker slot flips
    happen as a single bookkeeping commit.

* AGENTS.md `## Recent cross-cutting closures` entries — every per-PR
  closure in §3 ships its own AGENTS.md mirror entry per CANONICAL.md
  §1 3-surface godlike/06 SSOT lockstep discipline.

* CHANGELOG.md `## Unreleased` — every per-PR closure ships its own
  CHANGELOG.md entry mirroring the AGENTS.md surface (per CANONICAL.md
  §1).

---

## §8 — Wave-flip criterion

`architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-COMPREHENSIVE-2026-07-10`
flips `status: pending` → `status: shipped + exit_signal: true` ONLY
when:

* All 7 remaining items (6 + 7 + 8 + 9-final + 10-final + 11 + 12) have
  reached `status: shipped` in their respective forward-pointers (the
  Items 9 + 10 tombstones are "shipped" when their final retiral at
  2026-12-31 + 2026-09-26 respectively completes the canonical delete).
* All 4 "altri cleanup" PRs reach `status: shipped`.
* The 7-day sustained-zero metric counter for Items 9 + 10 has
  cleared.
* The `PR-HOTSPOT-CROSSREF` post-wave cross-validation (deadline
  2026-09-15) returns zero NEW hotspots in
  `git log --since=90.days --pretty=format: --name-only | sort |
  uniq -c | sort -rn | head -30` — otherwise the wave-tracker stays at
  `status: in_progress` and the forward-pointer ratchet extends.

Until ALL conditions hold, the wave-tracker slot flip is DEFERRED per
the YAML parse carry-forward + the parent action-plan + CHANGELOG +
AGENTS entries are the canonical SOLE closure record.

---

## §9 — Lifecycle audit-trail

* **Origin**: Italian audit pasted to orchestrator at 2026-07-10 against
  commit `0976108e` (the TTS-bug-fix commit).
* **Anchor**: action-plan file canonical owner is
  `architecture/action-plans/2026-07-10-legacy-cleanup-comprehensive.md`.
  This file is the SOLE canonical owner of the migration sequence;
  AGENTS.md + CHANGELOG.md are mirror surfaces.
* **Sister**: `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md`
  (already shipped 5 of 12).
* **Wave-tracker**: `architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-COMPREHENSIVE-2026-07-10`
  (DEFERRED per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward).
* **Audit-pin entries on `origin/main`**: 5 already-shipped entries
  per §10 below.
* **3-surface lockstep template**: AGENTS.md `## Recent cross-cutting
  closures` entry per closure + CHANGELOG.md `## Unreleased` sub-entry
  per closure + canonical `git push origin main` SHA per closure.

---

## §10 — Audit-pin registry (the 5 already-shipped items + 4 outstanding cleanup retirements)

This section is the canonical SOLE owner of the audit-pin registry
for the LEGACY-CLEANUP wave. Each retired item has its own canonical
ship_sha + ship_date; future agents reading this section inherit
the canonical record (no need to re-verify via rg on the retired
surface).

*(Audit-pin markers are intentionally NOT in code — they live in
this action-plan file only, so a future operator reading the source
tree does not see them; they ONLY surface in the AGENTS.md chron
feed + the CHANGELOG.md closure entries + the canonical git history.)*

### 10.1 — SHIPPED: `/api/images/animate` retirement

* Audit item: Item 1
* Canonical ship_sha: `4ebf1bfdf3ce82836262433e536136d81744b79b`
* Canonical ship date: 2026-07-10
* Source commit message:
  `refactor(images): PR-LEGACY-CLEANUP-2026-07-10 Item 1 — retire POST /api/images/animate handler (NVIDIA capability removed)`

### 10.2 — SHIPPED: `PostProcessArtifact = any` + `dto/compat_types.go` retirement

* Audit item: Item 2
* Canonical ship_sha: `fefaa7d48cd35eb944f28e3211111a9a390b8018`
* Canonical ship date: 2026-07-10
* Source commit message:
  `refactor(scripts): PR-LEGACY-CLEANUP-2026-07-10 Item 2 — retire PostProcessArtifact alias + dto/compat_types.go`

### 10.3 — SHIPPED: `vector_search:` yaml block elimination

* Audit item: Item 3
* Canonical ship_sha: `cc8225e68eed5c601bf4286bd85dc64e28bb4106`
* Canonical ship date: 2026-07-10
* Source commit message:
  `refactor(config): PR-LEGACY-CLEANUP-2026-07-10 Item 3 — retire vector_search: yaml block (canonical qdrant: retained)`

### 10.4 — SHIPPED: `status: removed` records → archive

* Audit item: Item 4
* Canonical ship_sha: `b7d73a18335234cf34d27cdaf9cac25c0d3a96bc` (source) +
  `7e25e58a946c09afc9db6796c2210494184c1aee` (lockstep)
* Canonical ship date: 2026-07-10
* Source commit message:
  `chore(arch): PR-DEPRECATIONS-ARCHIVE-2026-07-10 — migrate status: removed records to architecture/archive/deprecations-removed-2026-07-10.yaml`

### 10.5 — SHIPPED: Fullimages Option B retirement

* Audit item: Item 5
* Canonical ship_sha: `b95aceadbc24693c65f320eaf89234739439e3d7`
* Canonical ship date: 2026-07-10
* Source commit message:
  `refactor(fullimages): PR-IMAGES-FULLIMAGES-IMAGE-ONLY CUTOVER phase (2026-07-10) - retire unused video pipeline`
* Note: the user spec's "Option B" verdict (images-only, retire Ken Burns +
  6 helper functions + `SectionVideo` rename + `VideoPath` rename +
  route `/image/generate`) is LOCKED IN. Option A (re-link Ken Burns for
  MP4 output) was rejected.

### 10.6 — RETIRED (test-only, no production code change): nothing further this audit

The "tests relating to the route" referenced in Item 1's user-spec was
already deleted via the prior `4ebf1bfdf` commit; the canonical audit
for retirement-test-cleanup is at origin/main HEAD `2ebb24ea`
verified via `git ls-tree --name-only origin/main HEAD internal/api/images/`
returning no test file.

### 10.7 — RETIRED ON CALENDAR (today: leave tombstone, NOT-YET-PHYSICALLY-GONE)

| Audit item | Tombstone on origin/main | Scheduled physical retirement deadline |
|------------|--------------------------|--------------------------------------|
| Item 9 `/api/images/generate` | `internal/api/images/legacy_generate_handler.go` (returns 410 Gone + `X-Deprecation` header per AGENTS.md Pattern 9) | **2026-12-31** (post-7-day-zero-metric gate) |
| Item 10 `/api/voiceover/generate-with-group` | (handler + 410 + deprecation counter live on origin/main) | **2026-09-26** (post-7-day-zero-metric gate) |

These items get a SEPARATE final-retire PR at the deadline; this plan
documents the deadline + the wait-condition but does NOT physically
delete today (per godlike/07 NO-FAKE-AVAILABILITY: a final retire
without the metric-zero wait would silently delete a still-in-use
route).

---

## §11 — "Non vanno eliminate" guardrails (canonical SSOT)

The user-pasted audit enumerates 4 EXPLICIT categories of code that
MUST NEVER be retired by any PR in this plan (or any future wave).
They are canonical SSOT per godlike/06 one-canonical-owner-per-fact:

### 11.1 — `migrations/sqlite/*` (all SQLite migration files)

* **Canonical SOLE owner**: `migrations/sqlite/*.sql` (each file)
* **FORBIDDEN**: any per-PR in this plan MUST NOT git-rm a migration
  file. Migration files are the canonical ledger required for:
  * Creating new databases.
  * Rebuilding historical versions.
  * Verifying upgrade paths.
  * Restoring backups.
* **Forward-pointer exception**: a forward-pointer to a NEW migration
  file (e.g. `_add_column_X.sql`) is ALWAYS OK.

### 11.2 — `legacyState*` constants in `index_state.go`

* **Canonical SOLE owner**: `internal/domain/asset/index_state.go`
* **FORBIDDEN**: any per-PR in this plan MUST NOT delete the
  `legacyState*` typed-envelope constants. These exist INTENTIONALLY
  to reject pre-migration states (canonical rejection contract).
  Zero production callers is by design — they exist as the
  "we explicitly refuse to handle this state" signal per
  godlike/07 NO-FAKE-AVAILABILITY.

### 11.3 — `qdrant/legacyaudit` operator tool

* **Canonical SOLE owner**: `internal/application/qdrant/legacyaudit/`
* **FORBIDDEN**: any per-PR in this plan MUST NOT git-rm the
  legacyaudit binaries (the 4-file split topology per
  `PR-SPLIT-LEGACYAUDIT-V2` ship_sha `085210c02`). This is an
  operator-facing tool for finding + reconciling historical Qdrant
  payloads + collections. The file name "legacy" is NOT an indicator
  of dead code — it indicates that the tool KNOWS ABOUT legacy
  payloads. Removal is ONLY permitted after a healthy 30-day window
  of zero `audit_report{resolution="unsupported"}` Prometheus
  counters in production monitoring.

### 11.4 — `architecture/archive/*`

* **Canonical SOLE owner**: `architecture/archive/` (4 files currently
  per `git ls-tree --name-only origin/main HEAD architecture/archive/`)
* **FORBIDDEN**: any CI script MUST NOT include `architecture/archive/`
  in its source-scan configuration. The directory IS historical
  intent, not active code. Removal is ONLY permitted if the operator
  decides the SUPERANNUATION DATE has passed (currently
  2030-01-01 per the FASE-2.5-REPOSITORY-CLEANUP wave anchor).

---

## §12 — "Campi API senza effetto" wire-shape collapse (Phase C)

The user-pasted audit identifies the 4 wire-shape fields that have
zero behaviour-driving effect today:

| Field | Canonical owner | Behavior status |
|-------|-----------------|----------------|
| `OutputSpec.GenerateVoiceover` | `internal/application/scripts/dto/types.go::OutputSpec` | RETAINED for client compat; no longer read by canonical pipeline |
| `OutputSpec.GenerateSceneImages` | (same) | RETAINED for client compat; no longer read by canonical pipeline |
| `OutputSpec.GenerateDocument` | (same) | RETAINED for client compat; no longer read by canonical pipeline |
| `ArtifactResult.Document` (`Artifacts.Document`) | `internal/application/scripts/dto/...` | RETAINED; always nil in pipeline |

**Per-PR godlike/07 NO-FAKE-AVAILABILITY invariants**:

* 4 fields MUST NOT be removed individually (the user spec is
  explicit on this — "richiesta, normalizzazione e risposta
  devono cambiare nello stesso commit").
* The atomically-collapsed PR removes the 4 fields AND updates
  the test fixtures + the docs in a SINGLE atomic commit.

**Pre-flight rg** (§5 above) MUST scope to all 4 sites + the test
fixtures.

**Forward-pointer** (godlike/07 OUT OF SCOPE here):

* `PR-LEGACY-FIELDS-COLLAPSE-2` (deadline 2026-09-01, P2) — once the
  user client adoption threshold crosses (operator-set), also retire
  the wire-shape JSON tags (`omitempty` removal).

---

## §13 — "Altri cleanup" forward-pointer registry (Phase E)

| Item | Canonical owner | Deadline | Forward-pointer PR id |
|------|-----------------|----------|---------------------|
| mediasearch X-Deprecation + legacy response translator | `internal/api/mediasearch/` | Q3 2026 | `PR-MEDIASEARCH-LEGACY-TRANSLATOR-RETIRE` |
| Qdrant DTO duplicates (infrastructure ↔ application) | `_packages/qdrant/` (NEW shared types) | Q4 2026 | `PR-QDRANT-DTO-DEDUP` |
| Drive Admin fallback cutover (4 adapters) | `delivery.FileLifecycle` | 2026-08-22 | `PR-DRIVE-ADMIN-FALLBACK-CUTOVER` |
| `uploader_deprecated.go` retirement | `internal/infrastructure/drive/` | 2026-08-22 | `PR-UPLOADER-DEPRECATED-RETIRE` |

---

## §14 — Suggested followups (the clickable actions the user explicitly asked for)

Per the user's spec: "Proponi sempre Come minimo almeno 5 Suggested
followups o più se puoi". This action plan WILL be pushed to origin/main
in this session — that is commit 1 (the action plan + 3-surface lockstep
mirrors). The followups below are the subsequent per-PR closures the
plan orchestrates; each is a clickable action the user can pick up
to drive the wave forward:

1. **PR-LEGACY-NA-1 — retire `unavailableArtlistClipSearcher`** (Phase A, deadline 2026-07-22). Single PR. Per §3 + §4 + §5. Canonical lockstep on `origin/main`.
2. **PR-LEGACY-NA-2 — retire `NewUnavailableEntityExtractionAdapter` + `NewUnavailableMetadataGenerationAdapter`** (Phase A, deadline 2026-07-22). Single PR. Canonical fail-closed contract preserved per godlike/06 SSOT.
3. **PR-LEGACY-ENG-1 — collapse `Section.Engine`** (Phase A, deadline 2026-07-22). Phase 1 + Phase 2 forward-pointer.
4. **PR-LEGACY-FIELDS-COLLAPSE-1 — atomic wire-shape collapse of 4 "campi API" fields** (Phase C, deadline 2026-08-08). Single atomic commit enforcing "non vanno rimossi singolarmente".
5. **PR-LEGACY-TRANSLATION-PORTS-RETIRE — Item 11 + Item 12** (Phase D, deadline 2026-08-15). Two atomic commits on separate concerns.
6. **PR-MEDIASEARCH-LEGACY-TRANSLATOR-RETIRE** (Phase E, deadline 2026-08-22). Forward-pointer from §13.
7. **PR-DRIVE-ADMIN-FALLBACK-CUTOVER + PR-UPLOADER-DEPRECATED-RETIRE** (Phase E, deadline 2026-08-22). Two PRs cleanup the Drive adapter chain.
8. **PR-LEGACY-RETIRE-IMAGES-GENERATE-FINAL** (Item 9 final, calendar-gated 2026-12-31). Wait on the 7-day-zero-metric gate.
9. **PR-LEGACY-RETIRE-VOICEOVER-GENERATE-WITH-GROUP-FINAL** (Item 10 final, calendar-gated 2026-09-26). Wait on the 7-day-zero-metric gate.
10. **PR-LEGACY-HOTSPOT-CROSSREF** (deadline 2026-09-15). Post-wave git-log frequency cross-validation per the established `PR-CLEANUP-HOTSPOT-CROSSREF` + `PR-P12-HOTSPOT-CROSSREF` precedent. Verifies no NEW dead-code hotspots surfaced across the wave.

---

## §15 — Signature

* **Authoritative surface**: this file (`architecture/action-plans/2026-07-10-legacy-cleanup-comprehensive.md`).
* **Lockstep surfaces (canonical godlike/06 SSOT)**: CHANGELOG.md `## Unreleased > ### Documentation`
  mirror entry + AGENTS.md `## Recent cross-cutting closures` mirror entry + canonical ship_sha on `origin/main`.
* **Pre-existing 6-item voiceover + app build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`**: UNCHANGED — NOT a regression of this action plan.
* **Pre-existing dirty working-tree residue from prior sessions**: preserved untouched per godlike/07 minimum-blast-radius (only this action-plan file + CHANGELOG + AGENTS reach this commit; the §10 already-shipped canonical SHAs are the canonical reference, no further code changes are in scope for this commit).
* **Wave-tracker slot flip in `architecture/waves/wave_p1_high.yaml`**: DEFERRED per the pre-existing `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer chain (deadline 2026-08-15). The parent action-plan + CHANGELOG + AGENTS entries are the canonical SOLE closure record until the parse carry-forward resolves (mirrors `PR-POSTPROCESSOR-UNIFICATION-PHASE-4` + `SCRIPT-CONTRACT-2026-07-08` + `LEGACY-CLEANUP-5-ITEM-ORCHESTRATION` precedents at ship_shas `4c4550259` + `46c8911652e0` + `b95aceadbc` respectively).
* **Direct-to-main per AGENTS.md Git-Lesson-2** (NO branches, NO `--no-ff`, NO `--force`); **Co-authored-by trailer** per AGENTS.md Git-Lesson-3; **race-protect clean** per AGENTS.md Git-Lesson-4 (HEAD == origin/main @ `2ebb24ea` — verified before this commit lands; ff-only push expected).
