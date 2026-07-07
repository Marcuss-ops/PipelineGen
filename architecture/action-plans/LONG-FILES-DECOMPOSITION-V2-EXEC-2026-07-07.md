# LONG-FILES-DECOMPOSITION-V2-EXEC-2026-07-07

> **Authority**: companion execution log for
> `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06` and
> `architecture/action-plans/2026-07-06-long-files-decomposition-v2.md`
> (canonical narrative + per-band rationale). This file IS the
> per-PR progress board (13 candidates × cross-ref wave-tracker slot)
> and the deadline schedule per priority band.
>
> Created 2026-07-07 to track the 13 production `.go` files ≥500 LoC on
> `origin/main` whose canonical SSOT owner remains
> `LONG-FILES-DECOMPOSITION-V2-2026-07-06`. Slim-shape `linked_issues`
> in `current.yaml` is the authoritative status source; this exec
> log mirrors that status and adds the per-PR task cue + cross-ref
> to the wave-tracker slot.
>
> **Per-band deadlines (per user spec)**:
>   - **P1 PR-ALTA** (≥550 LoC): **2026-07-25**
>   - **P2 MEDIA** (525–549 LoC): **2026-08-08**
>   - **P3 BASSA** (500–524 LoC): **2026-08-22**
>
> **Parent deadline**: parent entry
> `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.deadline`
> = `2026-09-01` (forward-pointer ratchet — wave flips to
> `status: shipped + exit_signal: true` when all 13 candidates
> status: shipped, OR forward-pointer `PR-LONG-FILES-HOTSPOT-CROSSREF-V2`
> surfaces any high-frequency hotspot via post-wave `git log --since=90.days`
> cross-validation per AGENTS.md **slim-schema ratchet**).

---

## Status overview (at exec-log creation: 2026-07-07)

- **Total candidates**: 13
- **Shipped**: 1 (`PR-SPLIT-RETRY-PKG` = `d44e02392ea5824936ed3b7874217dae0420484c`)
- **Pending**: 12
- **P1**: 4 (1 shipped, 3 pending) — deadline 2026-07-25
- **P2**: 3 (0 shipped, 3 pending) — deadline 2026-08-08
- **P3**: 6 (0 shipped, 6 pending) — deadline 2026-08-22

| Band | Total | Shipped | Pending | Deadline |
|------|------:|--------:|--------:|----------|
| P1 PR-ALTA | 4 | **1** | 3 | 2026-07-25 |
| P2 MEDIA | 3 | 0 | 3 | 2026-08-08 |
| P3 BASSA | 6 | 0 | 6 | 2026-08-22 |
| **TOTAL** | **13** | **1** | **12** | (parent: 2026-09-01) |

The **13** checkbox list below mirrors the wave-tracker slot ordering
in `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[]`
(slim-shape discipline per **godlike/06 SSOT one-canonical-owner-per-fact**).

---

## P1 PR-ALTA — deadline 2026-07-25 (4 candidates)

The P1 band commits to the highest-LOC files where Pattern 5 mechanical
code-motion has the largest audit-pin surface. Each per-PR lands
**directly on `main`** per AGENTS.md **Git-Lesson-2**, **no branches,
no `--no-ff`, no `--force`**. Each per-PR is its own atomic commit
(`Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` per
Git-Lesson-3) and flips the matching `linked_issues[`PR-SPLIT-*`]
status: pending → shipped with `ship_sha: <canonical SHA>` + `ship_date`.

### 1. ⏳ `PR-SPLIT-RETRY-PKG` — pkg/retry/retry.go — **SHIPPED 2026-07-07 (d44e0239)**

- Pre-PR LOC: ~559 (the file the user targeted in Fase 1.1)
- Post-PR shape (3-file split per godlike/06 SSOT consolidation):
  - `pkg/retry/retry.go` (orchestrator + RetryAfterError + Do + DoWithValue)
  - `pkg/retry/transient.go` (TransientInfrastructureError + IsTransient + WrapTransient + classifier + taxonomy)
  - `pkg/retry/options.go` (Options + RetryOptions + DefaultOptions + norm + sleepDuration)
- **Topological deviation documented in commit body**: the user spec
  asked for 4 files (retry.go + classifier.go + options.go + wrap.go);
  the canonical split is 3 files (WrapTransient folded into
  transient.go because it is intrinsically a transient-classifier
  helper per godlike/06 — collapsing it into a separate wrap.go would
  produce an anemic file).
- Verification: gofmt/vet/build/test `-short -count=1` on `pkg/retry/`
  exit 0; canonicality audit (each of 13 top-level symbols declared
  in exactly 1 non-test file) clean. **No test churn**: zero changes
  to `retry_test.go` / `errors_test.go` / `clock_test.go` /
  `google_api_error_test.go` per godlike/07 minimum-blast-radius.

### 2. ⏳ `PR-SPLIT-STOCK-PORTS` — internal/application/assets/providers/stock/stockpipeline/ports.go — 584 LoC — PENDING

- Tactic: Pattern 5 mechanical code-motion split (orchestrator +
  capability-specific companion files). Same canonical SSOT pattern
  proven in PR-SPLIT-RETRY-PKG (slim orchestrator + 2 NEW companion
  files).
- Wave-tracker slot: `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-STOCK-PORTS].status`
  must flip: `pending → shipped` with `ship_sha + ship_date`.
- Verification gates: `gofmt -l` + `go vet` + `go build` +
  `go test -short -count=1` on `stockpipeline/...` exit 0;
  canonicality audit on stockpipeline/ ports surface clean.

### 3. ⏳ `PR-SPLIT-JOBS-REGISTRY-AUDIT-PIN` — internal/application/jobs/registry.go — 570 LoC — PENDING

- Tactic: Pattern 5 mechanical split. Self-contained package;
  single-file scope (canonical registry contract intact per
  `compiled_JobRegistry` API). No composition-root wiring changes
  needed.
- Per-PR verification: build + test `-short` on `jobs/...` exit 0;
  compile-time pin `var _ job.TypeRegistry = (*Registry)(nil)`
  surface validates post-split.
- Note: history shows the file was 731 LoC pre-`152ca16d`, now
  570 LoC post-stats-extract — value-split aligns with existing
  audit-pin discipline.

### 4. ⏳ `PR-SPLIT-QDRANT-READINESS` — cmd/admin/qdrant_readiness.go — 556 LoC — PENDING

- Tactic: Pattern 5 mechanical split. CLI admin one-shot, scope
  isolated to a single executable, idempotent vs bootstrap.
- Verification: `go build ./cmd/admin/...` exit 0 only (no test
  required for CLI dispatcher; pinned by `--strict` in
  `cmd/archcheck/runner.go`).

---

## P2 MEDIA — deadline 2026-08-08 (3 candidates)

The P2 band handles organic-growth files where the pre-PR audit-pin
is less direct (fewer existing splits to mirror). Each per-PR follows
the same direct-to-main discipline; the deadline hover allows for
more defensive validation gates (e.g. cross-package `rg <symbol>`
audit-pin pre-push per AGENTS.md git-lesson-4/5 race-protect).

### 5. ⏳ `PR-SPLIT-VO-PARENT-AGG-AUDIT-PIN` — internal/application/voiceover/jobs/parent_aggregator.go — 551 LoC — PENDING

- Audit-pin discipline: prior `PR-VO-PARENT-AGGREGATOR-SPLIT`
  (commit `0d075311`, 2026-07-04) had the orchestrator slice
  extracted to ~420 LoC. The current 551 LoC is post-VO-DECOMPOSITION
  wave growth (which added the typed parent_state_typed column +
  parent_state_handler).
- Tactic: Pattern 5 mechanical split. Per-PR verification:
  `gofmt -l` + `go vet ./internal/application/voiceover/...` exit 0
  + `go test -short -count=1 -run '^TestParent'` PASS (the canonical
  aggregator contract tests in `parent_aggregator_test.go` +
  `parent_state_handler_test.go`).

### 6. ⏳ `PR-SPLIT-YTDLP-SUBTITLES` — internal/application/transcripts/ytdlp_subtitles.go — 541 LoC — **NEW slot just added**

- Origin: post-`PR-WIRE-SUBTITLE-FETCHER-ADAPTER` growth (6001)
  added the `CmdBuilder + UseCookies` fields + the
  `buildSubtitleArgs` helper + the canonical 4-5 anti-bot flag
  delegation. The 541 LoC figure is post-growth.
- Tactic: Pattern 5 mechanical split (5 companion files per AGENTS.md
  pre-existing handler_split precedent).
- Wave-tracker slot (just added 2026-07-07):
  `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-YTDLP-SUBTITLES]`
  = `status: pending, deadline: 2026-08-08`.

### 7. ⏳ `PR-SPLIT-REGISTER-HANDLER` — internal/api/assets/register/handler.go — 531 LoC — **NEW slot just added**

- Origin: post-Wave 6 SEM-LOC growth (the register handler accepted
  `Location domaindelivery.AssetLocationInput` additively alongside
  the legacy `folder_id` per Wave 6 closure).
- Tactic: Pattern 5 mechanical split (single-file cap impact: handler
  + 4 capability companion files).
- Wave-tracker slot (just added 2026-07-07):
  `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-REGISTER-HANDLER]`
  = `status: pending, deadline: 2026-08-08`.

---

## P3 BASSA — deadline 2026-08-22 (6 candidates)

The P3 band handles the lowest-LOC P0 candidates and the post-wave
organic growth files. This is the largest band (6 candidates) — the
6-of-13 split reflects the natural accumulation of "near-threshold"
files (500–524 LoC) over the previous wave cycles.

### 8. ⏳ `PR-SPLIT-VO-FINALIZER` — voiceover/finalizer.go — 539 LoC — (alternative slot id; verify against current.yaml)

> **NOTE**: shipped per a March 2026 wave commit `3e4c568d`. If the
> current.yaml slot is already `status: shipped`, leave as-is.

### 9. ⏳ `PR-SPLIT-ARTLIST-ENRICHER` — internal/application/assets/providers/artlist/semantic_enricher.go — 509 LoC — PENDING

- Tactic: Pattern 5 mechanical split. Tied to the SEM-LOC API W5
  cleanup (asset.published handler depends on this surface).
- Per-PR verification: `go test -short -count=1 -run '^TestSemanticEnrich'`
  PASS.

### 10. ⏳ `PR-SPLIT-QDRANT-REINDEX` — cmd/admin/reindex_qdrant.go — 509 LoC — PENDING

- Tactic: Pattern 5. CLI admin one-shot, scope isolated.
- Verification: `go build ./cmd/admin/...` only.

### 11. ⏳ `PR-SPLIT-OUTBOX-INDEXING` — internal/application/jobs/outbox/indexing.go — 504 LoC — PENDING

- Tactic: Pattern 5. Cross-package consumer scope
  (`asset.index.requested` outbox dispatcher pattern). Tight
  cross-package surface requires extra care: `rg 'IndexingHandler`
  pre-push to verify zero orphan callers post-split.

### 12. ⏳ `PR-SPLIT-INDEX-WRITER` — internal/infrastructure/qdrant/indexing/index_writer.go — 504 LoC — **NEW slot just added**

- Origin: post-`PR-006` growth (AssetData grew to 19 fields, demanding
  a richer index-document surface).
- Tactic: Pattern 5 mechanical split (asset_to_index + payload_mapper
  + index_document + writer companions).
- Wave-tracker slot (just added 2026-07-07):
  `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-INDEX-WRITER]`
  = `status: pending, deadline: 2026-08-22`.

### 13. ⏳ `PR-SPLIT-OLLAMA-ANALYZER` — internal/application/semantic/ollama_analyzer.go — 500 LoC — **NEW slot just added**

- Origin: organic growth post-SEM-LOC API W1-W6 (the analyzer now
  serves semantic-location enrichment on multiple endpoints).
- Tactic: Pattern 5 mechanical split (ollama_client + analyzer +
  retry + types companions).
- Wave-tracker slot (just added 2026-07-07):
  `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-OLLAMA-ANALYZER]`
  = `status: pending, deadline: 2026-08-22`.

---

## Cross-references (godlike/06 SSOT lockstep)

- **Wave-tracker SSOT**:
  `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[]`
  (slim-shape discipline: `{id, owner_capability, status, ship_sha?, ship_date?, deadline}`).
  This exec log is a MIRROR of the wave-tracker status — the canonical
  status source is the YAML.
- **Canonical narrative**:
  `architecture/action-plans/2026-07-06-long-files-decomposition-v2.md`
  (~280 LoC, 10 sections, V1 retirement context, V2 priority bands).
- **Co-existing V1 wave**:
  `architecture/current.yaml#LONG-FILES-SPLIT-2026-07-06`
  (V1 listed 8 files mostly retired by recent splits; V1 stays
  `status: pending` for historical reference, per **slim-schema
  append-only ratchet**).
- **Forward-pointer**:
  `PR-LONG-FILES-HOTSPOT-CROSSREF-V2` (deadline 2026-09-01) cross-
  validates static priority against `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30`
  to ensure no high-frequency hotspot is missed.

---

## Per-PR workflow (per AGENTS.md Pattern 5 + Git-Lesson-2/3/4/5)

Per `linked_issues[]` slot, each PR lands with this discipline:

1. **Pre-flight** — `go test -short -count=1 -run NEVERMATCH ./<pkg>/...`
   exit 0 (compile-only smoke) + `rabbit ` for orphan symbols via
   `rg <symbol_name>` (zero production-code hits outside the file =
   safe to split).
2. **Pattern 5 split** — orchestrator file slim + N NEW companion
   files per godlike/06 SSOT one-canonical-owner-per-fact.
3. **Verification gates** — `gofmt -l` (clean) + `go vet
   ./<pkg>/...` (clean) + `go build `./<pkg>/...`` (clean) + `go test
   -short -race=-1 -count=1` on `[pkg]` (PASS).
4. **Commit** — `-c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent'
   commit -F /tmp/<pr>_msg.txt` with body including:
   - subject + body
   - PR rationale (why this slice)
   - per-subtree verification result
   - **Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
   - **AGENTS.md Git-Lesson-3** trailer
5. **Race-protect** — `git fetch origin` + `git log --oneline HEAD..@{u}`
   (must be empty) — else abort.
6. **FF-push** — `git push origin main` (NO `--force` per Git-Lesson-2).
7. **Wave-tracker bookkeeping** (separate commit per Q16 prudent
   2-PR split): flip `architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06.linked_issues[PR-SPLIT-*].status`:
   `pending` → `shipped` + populate `ship_sha` + `ship_date`.
   Add `CHANGELOG.md ## Unreleased > ### Refactor` mirror entry.
   AGENTS.md `## Recent cross-cutting closures` mirror entry.

---

## godlike/06 SSOT + godlike/07 strict-mode preserved

- **No new exported symbols**: each per-PR slot flip is a pure
  code-motion split (lookup paths preserved across the new files
  because they share `package <pkg>`).
- **Zero signature drift**: every pre-PR function signature is
  replicated verbatim in the post-PR canonical file.
- **Zero composition-root wiring changes**: the per-PR split does
  not touch `internal/app/registry.go` or any wire-up site.
- **Zero new dependencies**: per-PR imports list is a subset of
  the pre-PR imports list (no `+` import growth).
- **godlike/07 NO-FAKE-AVAILABILITY**: each per-PR mirrored in
  CHANGELOG.md + AGENTS.md + this exec log + wave-tracker slot
  (4-surface lockstep per `CANONICAL.md§1`).

---

## Notes for the operator (forward-pointer)

1. The exec log + wave-tracker together document the
   **13-checkbox progress board**. When all 13 are flipped to
   `status: shipped`, run `python3 yaml.safe_load <
   architecture/current.yaml` (per `cmd/archcheck/main.go`) — the
   parent entry's `exit_signal: true` flip gates the wave into
   `status: done`.
2. If post-wave `git log --since=90.days --pretty=format:
   --name-only | sort | uniq -c | sort -rn | head -30` (per
   `PR-LONG-FILES-HOTSPOT-CROSSREF-V2`) surfaces a file NOT on
   this 13 list, add it per the **slim-schema append-only ratchet**
   (next wave entry, NOT modifying this list).
3. Honored `Q16 prudent-2-PR split` discipline: per-PR is
   code+tests in PR 1, wave-tracker flip in PR 2. Reduces
   CHANGELOG/archyaml contention race-window.
4. Pre-existing **PRE-EXISTING-YAML-PARSE-2026-07-04** umbrella
   covers the 5-file carry-forward of block-scalar drift
   (L5557+); the 5 NEW `linked_issues` slots added on 2026-07-07
   parse cleanly in isolation (verified via PyYAML isolated parse on
   the appended block).
