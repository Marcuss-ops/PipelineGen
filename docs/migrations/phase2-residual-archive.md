# Phase 2 PR-5 + FRAGMENTO chain — archive of resolved blocks

> **Purpose.** Forensic archive for ALREADY-DONE ALREADY-DONE blocks whose Wave is
> now resolved-and-closed. These were originally preserved verbatim in
> [`docs/migrations/phase2-residual.md`](./phase2-residual.md) (per audit-pin
> rationale "preserve verbatim below this top section for grep-by-author
> + commit-tracing purposes"). User request (July 2026) was to drop the resolved
> rows from the live doc; the code-reviewer found that pure-delete violates the
> preservation audit-pin. SHIFT-to-archive preserves both.
>
> **Provenance.** Each entry preserves the original tornata verification text
> + commit SHA + filename shape at time of close. Future agents requiring
> per-tornata forensic detail can grep this file; the live doc keeps a
> landscape-only view.

---


---

## Wave 1 (middleware migration) — VERIFIED ALREADY-DONE, July 2026

**Action taken this tornata:** zero code changes. Atomic docs-only close commit.

**Verifications on HEAD `18b30c45` (independent bash recon):**
- `internal/assets/` directory — DOES NOT EXIST (`find . -path '*/internal/assets*'` returns zero). Zero importers of `github.com/Marcuss-ops/PipelineGen/internal/assets` in the entire repo.
- `internal/app/bootstrap.go` — 0 refs to `internal/assets`. Domain-asset imports handled elsewhere.
- `internal/app/registry.go` — 0 refs to `internal/assets`. Imports `internal/application/assets/providers` directly for source wiring.
- `internal/app/dependencies.go` — DOES NOT EXIST (file deleted earlier during the AGENTS.md "God Object splits" cleanup pass).

The user-reported 14-ref-count (bootstrap=1, dependencies=12, registry=1) is a phantom from a stale mental model. The actual filesystem state matches a clean migration of these three files (or their replacements/splits) that was completed by prior code-improvement passes.

**What was implied vs what shipped:** No new code changes needed. The user rule "no cose strane + commit + push directly main, frequently, no branches" is honored by an atomic docs-only close.

**Phase-2 PR-5 wave ordering note:** Per docs/migrations/phase2-residual.md count, Wave 1 was the smallest of the 5 waves (3 files, 14 refs). Mid-tornata, the larger waves (Wave 4 sources: 20 files, 102 refs; Wave 5 FRAGMENTO-c: 4 files) become the natural next steps via fresh tornatas. Each closes via the same pattern (recon + atomic commit + push) without code changes if structurally already done.

**Why this commit is explicit:** The user standing rule ("5+ suggested followups" + "no cose strane") implies that an explicit close-commit is preferable to a silent no-op that leaves the Wave 1 line item dangling in the next assistant turn's queue. Future tornatas can grep `ALREADY-DONE` to fast-skip the closed waves.

---


---

## Wave 4 (sources migration) — VERIFIED ALREADY-DONE, July 2026

**Action taken this tornata:** zero code changes. Atomic docs-only close commit.

**Verifications on HEAD `9a949ec8`:**
- `internal/assets/` directory — DOES NOT EXIST (`find . -path '*/internal/assets*'` zero hits repo-wide).
- 0 importers of `github.com/Marcuss-ops/PipelineGen/internal/assets` repo-wide.
- `internal/sources/artlist/*` — 0 refs to `internal/assets`. The 9 files (per user-reported count) reference `internal/domain/asset` or `internal/application/assets/...` already.
- `internal/sources/youtube/*` — 0 refs to `internal/assets`. The 11 files (per user-reported count) also reference internal/domain/asset or internal/application/assets/... already.

The user-reported 102-ref-count (artlist: 9 files / 57 refs; youtube: 11 files / 45 refs) is a phantom from a stale mental model. The actual filesystem state matches a clean migration of these 20 source-package files that was completed by prior code-improvement passes.

**No 2-atomic-commit split needed:** the entire Wave 4 is structurally complete from prior tornatas. A single docs-only close commit captures it.

**Phase-2 PR-5 wave ordering (post Wave 4 close):** all 5 documented waves are now either fully done or pending separate, non-conflicting paths:
  - Wave 1 (middleware): DONE (9a949ec8).
  - Waves 2 + 3 (generic handlers, cmd/): originally documented as 0 files in phase2-residual.md; effectively no-op from the start.
  - Wave 4 (sources / artlist + youtube): DONE this tornata.
  - Wave 5 (multi-PR FRAGMENTO-c overlap): partially DONE (PR-c-2 forward-looking guard test lands in 18b30c45; PR-c-1 + PR-c-3 closed as docs-only ALREADY-DONE in prior tornatas).

Per the user rule: commit + push directly to main, no branches.

