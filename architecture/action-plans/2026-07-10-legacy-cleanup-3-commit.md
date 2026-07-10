# Legacy Cleanup — 3-Commit Direct-to-Main Orchestration

`ship_date: 2026-07-10`
`wave-tracker slot: architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-3-COMMIT-2026-07-10 (DEFERRED per PRE-EXISTING-YAML-PARSE carry-forward)`
`workflow: NO BRANCHES — direct-to-main, push frequently per AGENTS.md Git-Lesson-2`

---

## §0 — Pre-Flight Live-Truth Validation (godlike/07 NO-FAKE-AVAILABILITY)

> **CRITICAL RULE:** Before any surgery, verify the audit claim is REAL on `origin/main` HEAD. Fabricating work on a non-existent surface is the canonical "no fake availability" anti-pattern. The historical diagnostic loop (10+ redundant basher calls across turns) surfaced multiple `No such file or directory` ripgrep results, meaning some prior-session audit claims may be stale against the live tree.

### §0.1 — Git state verification

```bash
cd "$(git rev-parse --show-toplevel)"
git fetch origin
echo "HEAD: $(git log -1 --format='%H %s (%ci)')"
echo "Branch: $(git branch --show-current)"
echo "Divergence (HEAD vs origin/main): $(git rev-list --left-right --count HEAD...origin/main)"
echo "Dirty tree: $(git status --short | wc -l)"
echo "Stash count: $(git stash list | wc -l)"
echo "CGO: $(which gcc 2>&1 || echo 'no gcc — Windows agent carry-forward')"
go version
```

**Acceptance gate:** `git rev-list --left-right --count HEAD...origin/main` must return `0 0` (zero behind, zero ahead); CGO is irrelevant for our scope (3 commits modify no CGO code).

### §0.2 — Audit-Claim Truth Table (the load-bearing check)

> The historical 3-commit plan targets 5 audit claims. Each must be VERIFIED present on `origin/main` before commit surgery. Each live-truth check below is **per audit claim** and BLOCKING per `godlike/07 NO-FAKE-AVAILABILITY`.

| # | Audit claim | Per-claim live check | Truth verdict (binary) |
|---|------------|---------------------|------------------------|
| C1.a | `POST /api/images/animate` endpoint exists (Commit 1 target) | `rg -n '/api/images/animate\|animate_handler\|AnimateRequest' --type go internal/api 2>&1` | TBD by §0.3 diagnostic |
| C1.b | `PostProcessArtifact` type alias exists (Commit 1 target) | `rg -nc 'PostProcessArtifact' --type go internal/ 2>&1` | TBD |
| C1.c | `vector_search:` config block exists in `config.yaml` / `config.example.yaml` (Commit 1 target) | `rg -n 'vector_search:' config.yaml config.example.yaml 2>&1` | TBD |
| C1.d | `status: removed` records exist in `architecture/deprecations.yaml` (Commit 1 archival target) | `rg -n 'status:\s*removed' architecture/deprecations.yaml 2>&1` | TBD |
| C2.a | `NewUnavailableEntityExtractionAdapter` + `NewUnavailableMetadataGenerationAdapter` + `unavailableArtlistClipSearcher` adapter constructors exist (Commit 2 target) | `rg -nc 'NewUnavailable(EntityExtraction\|MetadataGeneration)Adapter\|unavailableArtlistClipSearcher' --type go internal/application/scripts/adapters/` | TBD |
| C3.a | `generateOneVideo` function exists in `internal/application/images/fullimages/service.go` (Commit 3 per deep-think Option B verdict) | `rg -n 'generateOneVideo\|SectionVideo\|VideoPath\|processGeneratedVideo\|uploadAndFinish\|publishToDrive' --type go internal/application/images/ 2>&1` | TBD |

### §0.3 — Outcome dispatch (decision tree per godlike/07 NO-FAKE-AVAILABILITY)

- **ALL 6 audit claims TBD = TRUE** → proceed with §1 → §2 → §3 sequentially.
- **One or more audit claims TBD = FALSE** → write a single 0-action audit-pin `POST` to `/api/jobs/`-parallel closure record (CHANGELOG.md + AGENTS.md + commit with `ship_via: AUDIT_PIN_PRE_SHIPPED_FALSE_PREMISE`), surface the false-premise verbatim in the closure entries, and provide revised forward-pointers for the actually-present surfaces. **DO NOT FABRICATE SECTIONS for non-existent files.** This is the 4-strike audit-pin precedent pattern (`4fc8106` + `4f1f286ad` + `6391621f0` + `6391621f0`).
- **Mixed verdict (e.g., C1.a TRUE but C1.b FALSE)** → write per-claim audit-pin closures for FALSE items, proceed with §N commitments for TRUE items with their scope narrowed accordingly.

### §0.4 — Per-claim defensive 2nd check (counter-fraud)

For ANY verbatim string-literal claim in the historical memory (e.g., "the comment claims convert to MP4", "the legacy `processGeneratedVideo` path"), re-derive at `origin/main` HEAD — never trust prior-session snapshots. Memory is a stale-evidence pattern; live ripgrep is canonical.

---

## §1 — Commit 1: P0 Safe Legacy Removals

**Prerequisite gate:** §0 audit-claim truth-table verification returns TRUE for C1.a + C1.b + C1.c (or the per-claim 0-action audit-pin substitution).

### §1.1 — Scope (declarative)

Drop `POST /api/images/animate` route handler + `AnimateRequest` request type + `PostProcessArtifact` type alias (or alias-target) + `SerializeEntityResultRoundTrip` (relocate to `internal/application/scripts/usecase/persistence.go` per the prior-session canonical-target convention). Additionally: remove the obsolete `vector_search:` block from `config.yaml` + `config.example.yaml` and archive the matching `status: removed` records from `architecture/deprecations.yaml` to `architecture/archive/`.

### §1.2 — SSOT owner (one canonical owner per fact)

- `internal/api/images/animate_handler.go` → SOLE canonical owner of animate endpoint contract (deleted post-commit; relocation target SOLE owner becomes the canonical `/api/images/...` surface in `internal/api/images/handler.go`).
- `SerializeEntityResultRoundTrip` → SOLE canonical owner = `internal/application/scripts/usecase/persistence.go` (canonical persistence orchestrator). The relocated function MUST restore the byte-equivalent lockstep contract per godlike/06 SSOT.
- `architecture/deprecations.yaml::removed` records → relocated to `architecture/archive/deprecations_removed_2026-07-10.yaml` after verbatim copy. Index entry preserved per archive-directory convention.

### §1.3 — Verification gates (run ALL in parallel where possible)

```bash
gofmt -l internal/api/images/ internal/application/scripts/ internal/application/scripts/usecase/ internal/app/ 2>&1
go vet ./internal/api/images/... ./internal/application/scripts/... ./internal/application/scripts/usecase/... 2>&1
go build ./internal/api/images/... ./internal/application/scripts/... ./internal/app/... 2>&1
go test -short -count=1 ./internal/api/images/... ./internal/application/scripts/... 2>&1
go test -short -count=1 -run 'TestGenerate\|TestVoiceover\|TestScriptPersist' ./internal/api/script/... 2>&1   # cross-package regression check
```

`bash scripts/ci-architectural-checks.sh` (canonical forward-prevention gate; must exit 0 — origin/per-check mandates).

### §1.4 — Race-protect clean push (AGENTS.md Git-Lesson-4)

```bash
git fetch origin && git log --oneline HEAD..@{u}   # MUST return empty pre-push
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body per codebase convention: godlike/07 NO-FAKE-AVAILABILITY + 4-surface SSOT note + Co-authored-by trailer>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git push origin main                            # direct ff-only, no --force per Git-Lesson-2
```

### §1.5 — 3-surface godlike/06 SSOT lockstep (per CANONICAL.md §1)

CHANGELOG.md `## Unreleased > ### Removed` mirror entry + AGENTS.md `## Recent cross-cutting closures` mirror entry + canonical ship_sha on `origin/main` after ff-push.

---

## §2 — Commit 2: No Fake Availability — Composition-Time Backend Gates

**Prerequisite gate:** §1 pushed to origin/main. §0 truth-table for C2.a Truth = TRUE (or per-claim audit-pin substitution if FALSE).

### §2.1 — Scope (declarative)

Drop 3 unavailable adapters from `internal/application/scripts/adapters/` (the `NewUnavailableEntityExtractionAdapter` + `NewUnavailableMetadataGenerationAdapter` + `unavailableArtlistClipSearcher`). Replace with composition-time backend gates (fail-fast at composition-root if the corresponding backend is wired: `nil` → typed sentinel + skip registration rather than silently registering a fallback). Extract typed ports from the consolidated `compat_adapters.go` file into the canonical typed-port file at `internal/application/scripts/ports/` and delete the compat-types aliases that pointed to the unavailable adapters.

### §2.2 — SSOT owner (one canonical owner per fact)

- 2-3 typed ports move from `compat_adapters.go` → `internal/application/scripts/ports/{entity_extractor,metadata_generator,artlist_clip_searcher}.go` per godlike/06 SSOT.
- `composition_root.New*AdapterByBackendPresence` (NEW canonical fail-fast composition gate) lives ONLY at `internal/app/build_bundles_scripts.go` (or equivalent canonical composition-root file, verified per §0.2).
- The dropped unavailable adapter types are NOT retained as dead code per the prior-session `AUDIT_PIN_DEAD_CODE_PURGE_2026-07_25` precedent (commit `5a32611d`).

### §2.3 — Verification gates (parallel where possible)

```bash
gofmt -l internal/application/scripts/ports/ internal/application/scripts/adapters/ internal/app/ 2>&1
go vet ./internal/application/scripts/ports/... ./internal/application/scripts/adapters/... ./internal/app/... 2>&1
go build ./internal/application/scripts/ports/... ./internal/application/scripts/adapters/... ./internal/app/... 2>&1
go test -short -count=1 ./internal/application/scripts/ports/... ./internal/application/scripts/adapters/... ./internal/app/... 2>&1
go test -short -count=1 -run 'TestPipelineFlow\|TestAdaptByBackendPresence\|TestComposition' ./internal/app/... 2>&1
```

### §2.4 — Race-protect clean push

Per §1.4 idempotent — same procedure.

### §2.5 — 3-surface godlike/06 SSOT lockstep

CHANGELOG.md `## Unreleased > ### Refactor` + AGENTS.md mirror + canonical ship_sha on `origin/main`.

---

## §3 — Commit 3: Fullimages Orphan Chain Cleanup (Per Deep-Think Option B Verdict)

**Prerequisite gate:** §2 pushed to origin/main. §0 truth-table for C3.a Truth = TRUE.

### §3.1 — Deep-Think Verdict (canonical decision)

The prior diagnostic round (`<sample trial session 2026-07-04>` via `thinker-with-files-gemini`) returned **Option B**: **`internal/application/images/fullimages` should produce IMAGES (not MP4)**. The rationale: the comment in `service.go:132` claims "convert to Ken Burns MP4" but the active `generateOneVideo` code path strictly calls `s.imgService.GenerateSmartImage` (line ~175) and assigns the raw `imagePath` to `VideoPath` (line ~224) — **zero usage of `s.ffmpegProc` or `s.uploadAndFinish` in this chain**. The legacy `processGeneratedVideo` (lines 230-252) is orphaned compat-path overhead. Therefore: rename the orphan chain to truthfully reflect current behavior (image-generation, not video-generation).

### §3.2 — Scope (declarative)

**Step A — rename (3 lines):**
- struct `SectionVideo` → `SectionImage` (file: `internal/application/images/fullimages/service.go`)
- struct field `VideoPath` → `ImagePath`
- function `generateOneVideo` → `generateOneImage`
- HTTP endpoint conceptual rename: `/api/images/full/generate-video` → `/api/images/full/generate-image` (mirror in route registration + inspector godocs only; do NOT modify route literal in this commit — that's a separate downstream consumer migration)

**Step B — delete the orphan chain (3 functions):**
- `processGeneratedVideo` (lines ~230-252) — explicit legacy compat hardcoded as overlay compat-path
- `uploadAndFinish` (the legacy MP4 upload ceremony)
- `publishToDrive` (the legacy Drive publish ceremony)

**Step C — verify godlike/07 typed-error contract preserved:** the rename must preserve ALL sentinel error strings + return-type signatures so callers in `internal/api/fullimages/handler.go::Handler.GenerateOneImage` (post-rename `Was GenerateOneVideo`) keep byte-equivalent behavior.

### §3.3 — SSOT owner (one canonical owner per fact)

- `internal/application/images/fullimages/service.go` is the SOLE canonical owner of `SectionImage` struct (post-rename).
- `internal/api/fullimages/handler.go` is the SOLE canonical owner of `GenerateOneImage` POST endpoint implementation (consumer of the renamed struct).
- The 3 deleted orphan functions become dead-code-removal entries (mirrors AUDIT_PIN_DEAD_CODE_PURGE_2026-07_25 precedent `5a32611d`).

### §3.4 — Verification gates (parallel where possible)

```bash
gofmt -l internal/application/images/fullimages/ internal/api/fullimages/ 2>&1
go vet ./internal/application/images/fullimages/... ./internal/api/fullimages/... 2>&1
go build ./internal/application/images/fullimages/... ./internal/api/fullimages/... ./internal/app/... 2>&1
go test -short -count=1 ./internal/application/images/fullimages/... ./internal/api/fullimages/... 2>&1
go test -short -count=1 -run 'TestFullImagesGenerateOne\|TestSectionImage' ./internal/application/images/fullimages/... 2>&1
```

`go run ./cmd/admin --check-tests` (programmatic test verification; BATS smoke gate per the canonical discipline).

### §3.5 — Race-protect clean push

Per §1.4 idempotent — same procedure.

### §3.6 — 3-surface godlike/06 SSOT lockstep

CHANGELOG.md `## Unreleased > ### Refactor` + AGENTS.md `## Recent cross-cutting closures` mirror + canonical ship_sha on `origin/main`.

---

## §4 — Wave-Flip Criterion (mother of all gates)

The wave entry `architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-3-COMMIT-2026-07-10` (with 3 slim-shape `linked_issues[]` slots) flips to `status: shipped + exit_signal: true` ONLY WHEN:

1. ✅ Commit 1.1 SSOT: animate route + AnimateRequest type + PostProcessArtifact alias + SerializeEntityResultRoundTrip relocation + vector_search config block + status:removed archive — all 6 surgical operations ACTUAL on live tree (per §0 truth-table).
2. ✅ Commit 2.1 SSOT: 3 unavailable adapters dropped, typed-port extract into `internal/application/scripts/ports/`, composition-time fail-fast gate at `internal/app/build_bundles_scripts.go` (or canonical equivalent per §0.2 verification).
3. ✅ Commit 3.1 SSOT: SectionVideo→SectionImage rename + generateOneVideo→generateOneImage + orphan chain (`processGeneratedVideo` + `uploadAndFinish` + `publishToDrive`) deleted — verified by `rg -nc 'processGeneratedVideo\|uploadAndFinish\|publishToDrive' internal/application/images/` returning 0 (post-commit-must-not-rereference).
4. ✅ All `gofmt + go vet + go build + go test -short` verification gates exit 0 on the touched subtrees.
5. ✅ `bash scripts/ci-architectural-checks.sh` exits 0 (forward-prevention gates consistent with the live tree).
6. ✅ 3-surface godlike/06 SSOT lockstep entries shipped for each commit (CHANGELOG + AGENTS + canonical ship_sha on `origin/main`).
7. ✅ Post-wave hotspot crossref (forward-pointer §5) appends ZERO new `linked_issues[]` per slim-schema ratchet (mirrors `PR-CLEANUP-HOTSPOT-CROSSREF-2026-07-09` precedent `ab7042f0`).

The 4th surface (`architecture/waves/wave_p1_high.yaml` wave-tracker slot flip) is **DEFERRED** per the pre-existing `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15). Parent CHANGELOG + AGENTS entries are the canonical SOLE closure record until the parse carry-forward resolves.

---

## §5 — Forward-Pointers (post-wave + per-claim substitutes)

### §5.0 — Per-claim substitutes (only if §0.3 returned FALSE for any audit claim)

- **§5.0.1 — ANIMATE-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15) — write 0-action audit-pin mirroring the `4fc8106` YAGNI precedent if §0 returns C1.a = FALSE.
- **§5.0.2 — POSTPROCESSARTIFACT-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15) — same pattern if C1.b = FALSE.
- **§5.0.3 — VECTOR-SEARCH-CONFIG-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15) — if C1.c = FALSE.
- **§5.0.4 — FULLIMAGES-DEEPTHINK-REVALIDATE** (deadline 2026-07-22) — if any of the C3.x family is FALSE, re-spawn `thinker-with-files-gemini` with current `service.go` content for re-validation.
- **§5.0.5 — UNAVAILABLE-ADAPTERS-FAMILY-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15) — if C2.a = FALSE.

### §5.1 — Per-commit wave-link forward-pointers (post-wave completion)

- **§5.1.1 — PR-LEGACY-CLEANUP-HOTSPOT-CROSSREF** (deadline 2026-08-15) — post-wave git-log frequency cross-validation per slim-schema ratchet (mirrors `PR-CLEANUP-HOTSPOT-CROSSREF-2026-07-09` precedent).
- **§5.1.2 — PR-ANIMATE-CONSUMER-CLEANUP** (deadline 2026-08-22) — follow-up wave migrating any remaining caller of `AnimateRequest` after the commit-1 deletion.
- **§5.1.3 — PR-FULLIMAGES-DOWNSTREAM-MIGRATION** (deadline 2026-08-22) — operator-facing follow-up wave migrating operator callers from the conceptual `/generate-video` endpoint to `/generate-image` per the Option B truth-alignment.
- **§5.1.4 — PR-COMPOSITION-FAILFAST-AUDIT** (deadline 2026-08-22) — forward-prevention guard in `cmd/archcheck/scan/` mirroring the new composition-time backend-presence fail-fast gate (TypeScript-style guard on the AppBuilder wiring order).
- **§5.1.5 — PR-DEAD-COMPAT-ALIASES-RETIRE** (deadline 2026-08-31) — close the 12-week canonical compatibility window per the COMPOSITION-ROOT-RETIREMENT convention post-wave.

---

## §6 — Honest Scope-Lock (godlike/07 minimum-blast-radius)

- **In-scope:** 3 surgical commits, each auto-sufficient per AGENTS.md Pattern 5; 3-surface godlike/06 SSOT lockstep per CANONICAL.md §1; verbatim pre-flight + race-protect + bonus audit-pin for any FALSE §0 claim.
- **Out-of-scope:** rewrite of unrelated canonical surfaces (e.g., the `generation_plan_builder.go` slated for canonical-processor-name unification lives on a different wave-tracker slot per `architecture/waves/wave_p1_high.yaml#POSTPROCESSOR-UNIFICATION-2026-07-08`). Do NOT touch unrelated code in service of this wave's 3 commits.
- **Pre-existing 6-item voiceover + app build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED** — NOT a regression of the 3 commits.
- **`architecture/waves/wave_p1_high.yaml` wave-tracker slot flip DEFERRED** per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15).
- **Race-condition discipline (AGENTS.md Git-Lesson-4 + Git-Lesson-5):** every push checks `git fetch && git log --oneline HEAD..@{u}` MUST be empty pre-push. If parallel agent lands byte-equivalent work during the commit-to-push window, accept the canonical SHAs on `origin/main` without force-push (the canonical-coordination signal, not a contest).

---

## §7 — Cross-References (umbrella)

- **Sibling action plans (voice/structure convention):**
  - `architecture/action-plans/2026-07-08-script-pipeline-contract.md` (SCRIPT-PARENT-DECOUPLING)
  - `architecture/action-plans/2026-07-09-script-pipeline-decoupling.md` (DECOUPLING follow-up)
  - `architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md` (dead-code 8-PR wave)
- **Predecessors (cross-references for SSOT inheritance):**
  - AGENTS.md `## Recent cross-cutting closures` entries for `AUDIT_PIN_DEAD_CODE_PURGE_2026-07_25` (commit `5a32611d`) — the canonical orphan-chain-retirement precedent
  - AGENTS.md entry for `INTERFACE-ANY-CONVERSION-2026-07-09` (commit `f876c13aa`) — the canonical mechanical code-motion precedent
  - AGENTS.md entry for `P12-DRIVE-COMPLETION-2026-08-01` wave (`percheck_root_override_ban` archcheck scanner) — the canonical forward-prevention pattern
- **Successors (per §5 forward-pointers):** the 5 per-claim audit-pin substitutes + 5 commit-completion forward-pointers (HOTSPOT-CROSSREF + ANIMATE-CONSUMER-CLEANUP + FULLIMAGES-DOWNSTREAM-MIGRATION + COMPOSITION-FAILFAST-AUDIT + DEAD-COMPAT-ALIASES-RETIRE).

---

## §8 — Lifecycle Audit-Trail

```yaml
id: PR-LEGACY-CLEANUP-3-COMMIT-2026-07-10
status: pending
ships: 3 atomic commits on origin/main
wave_flip_criterion: §4 all 7 conditions met
yields_zero_new_hotspots: ENFORCED via §5.1.1 PR-LEGACY-CLEANUP-HOTSPOT-CROSSREF
godlike_07_NO_FAKE_AVAILABILITY: §0 is the load-bearing live-truth gate
godlike_06_SSOT: per-commit §-owner canonical declarations (3 surfaces per commit)
locked_3_surface: CHANGELOG.md + AGENTS.md + canonical ship_sha on origin/main per commit
carry_forward: architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04 (DEFERRED until parse carry-forward resolves)
race_protect: git fetch && git log --oneline HEAD..@{u} MUST return empty pre-push (no --force ever)
direct_to_main: 3 atomic commits on main per AGENTS.md Git-Lesson-2 (no branches, no --no-ff, no --force)
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>  # per AGENTS.md Git-Lesson-3 trailer convention
```

---

**End of action plan.** Use the 8+ suggest_followups below to drive execution per the §7 lifecycle audit-trail.
