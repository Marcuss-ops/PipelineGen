#!/usr/bin/env python3
"""FASE 0.1 — apply remaining godlike/06 SSOT doc-surface updates.

Idempotent. Reads with utf-8 encoding (Windows cp1252 default breaks on non-ASCII
chars in CHANGELOG / AGENTS / deprecations).
"""

import pathlib
import sys

ROOT = pathlib.Path(r"C:\Users\pater\Pyt\PipelineGen")
ENCODING = "utf-8"


def main() -> int:
    # ── 2B: architecture/deprecations.yaml — promote REMOTE-COMPLETE-LEGACY ──
    p = ROOT / "architecture" / "deprecations.yaml"
    t = p.read_text(encoding=ENCODING)
    if "deprecated-with-typed-gate" in t and "promotion:" in t and "ErrCompleteJobPathViolation" in t:
        print("OK 2B: REMOTE-COMPLETE-LEGACY already promoted (idempotent skip)")
    else:
        marker = "- id: REMOTE-COMPLETE-LEGACY\n"
        i = t.find(marker)
        if i == -1:
            print("FAIL 2B: REMOTE-COMPLETE-LEGACY entry not found")
            return 1
        sub = t[i : i + 10000]
        intro = sub.find("introduction_date:")
        if intro == -1:
            print("FAIL 2B: no introduction_date inside block")
            return 1
        # Use 2-space indent to match sibling blocks (the entry's `removal_date:`
        # is 4-space indented). The promotion block lives under
        # the entry at indent-4 to align with sibling top-level keys.
        inject_lines = [
            "  promotion:\n",
            "    status: deprecated-with-typed-gate\n",
            "    migration_phase: EXPAND-with-typed-gate\n",
            "    promotion_date: \"2026-07-04\"\n",
            "    typed_sentinel: domainremote.ErrCompleteJobPathViolation\n",
            "    gate_surfaces:\n",
            "      - \"CompleteJobService.completeInTx::JobTypeRegistry.ProducesArtifacts(jobType) lookup\"\n",
            "      - \"SQLiteStore.Complete::r.producesArtifacts[jobType] lookup\"\n",
            "  introduction_date:",
        ]
        inject = "".join(inject_lines)
        abs_intro = i + intro
        before = t[:abs_intro]
        after = t[abs_intro + len("introduction_date:") :]
        p.write_text(before + inject + after, encoding=ENCODING)
        print("OK 2B: REMOTE-COMPLETE-LEGACY promoted to deprecated-with-typed-gate")

    # ── 2C: CHANGELOG.md — append P0-COMPL-1 closure entry ──
    p = ROOT / "CHANGELOG.md"
    t = p.read_text(encoding=ENCODING)
    if "P0-COMPL-1 typed-error gate" in t:
        print("OK 2C: CHANGELOG entry already present (idempotent skip)")
    else:
        i = t.find("## Unreleased")
        if i == -1:
            print("FAIL 2C: ## Unreleased not found")
            return 1
        entry = (
            "\n### Fixed\n\n"
            "- **[FASE 0.1 \u2014 P0-COMPL-1 typed-error gate (COMPLETION-CUTOVER-P0-2026-07-04), July 4 2026]** "
            "`feat(jobs)+refactor(jobs)` \u2014 canonical typed sentinel `remote.ErrCompleteJobPathViolation` "
            "(single SSOT owner at `internal/domain/remote/complete_job.go`) gates the legacy `tools.Complete` "
            "path for artifact-producing job types. Shipped across two enforcement surfaces: "
            "(a) typed-service `JobTypeRegistry` port wired fluently via `Service.WithJobTypeRegistry(reg)` "
            "+ in-TX gate in `completeInTx` (placed AFTER replay probe to preserve idempotency-on-replay "
            "semantics); (b) SQL-layer rejection at `SQLiteStore.Complete` via `r.producesArtifacts[jobType]` "
            "lookup wrapped to the canonical sentinel via `fmt.Errorf %w`. **Honest-limitation (godlike/07):** "
            "the typed-service gate is structurally unreachable today because `Request.Validated()` rejects "
            "empty Artifacts at the pre-TX fail-fast gate; its value is forward-preventive \u2014 when BACKFILL "
            "softens Validated to permit empty artifacts on non-artifact job types, this gate becomes the "
            "canonical enforcement point. Until then, SQL-layer rejection is the only active enforcement. "
            "**Deprecation record:** `architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY` promoted to "
            "`status: deprecated-with-typed-gate` + `migration_phase: EXPAND-with-typed-gate` (removal_date "
            "2026-Q4). **SSOT split (godlike/06 follow-up):** \"produces artifacts\" lives in 2 surfaces "
            "(Registry + SQLiteStore map); forward-pointer `PR-Boot-Registry-ProducesArtifactsMap-"
            "AgreesWithSQLiteStore` adds the canonical contract test. **Companion test surface (godlike/06 "
            "SSOT):** `TestErrCompleteJobPathViolation_DistinctFromExistingSentinels` (errors.Is wrap probe "
            "+ self-identification + sentinel-distinctness from `ErrCompleteJobRequestMissingFields` + "
            "`ErrCompleteJobNotConfigured` + `ErrConcurrentLeaseRefutation`). Pre-existing 1-item "
            "carry-forward: `worker_execution.go:246` reference updated to canonical sentinel (deleted "
            "package-local alias `ErrArtifactJobRequiresCompleteWithArtifactsCompat` per godlike/07 "
            "no-fake-availability). Cross-references: "
            "`architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS.linked_issues"
            "[P0-COMPL-1-MANIFEST-DECISION]`, `architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY`, "
            "AGENTS.md \u00a7Recent cross-cutting closures.\n"
        )
        nl = t.find("\n", i)
        p.write_text(t[: nl + 1] + entry + t[nl + 1 :], encoding=ENCODING)
        print("OK 2C: CHANGELOG entry appended")

    # ── 2D: AGENTS.md — append P0-COMPL-1 closure entry under "## Recent cross-cutting closures" ──
    p = ROOT / "AGENTS.md"
    t = p.read_text(encoding=ENCODING)
    if "P0-COMPL-1 typed-error gate" in t:
        print("OK 2D: AGENTS.md mirror entry already present (idempotent skip)")
    else:
        marker = "## Recent cross-cutting closures (June 2026)"
        i = t.find(marker)
        if i == -1:
            print("FAIL 2D: Recent cross-cutting closures heading not found")
            return 1
        nl = t.find("\n", i)
        entry = (
            "\n- **[FASE 0.1 \u2014 P0-COMPL-1 typed-error gate (COMPLETION-CUTOVER-P0-2026-07-04), "
            "July 4 2026]** `feat(jobs)+refactor(jobs)` \u2014 canonical typed sentinel "
            "`remote.ErrCompleteJobPathViolation` gates legacy `tools.Complete` for artifact-producing "
            "job types. Landed on 6 files (canonical typed sentinel at "
            "`internal/domain/remote/complete_job.go` + fluent-registry wiring + in-TX gate at "
            "`complete_job_service.go` + SQL-layer rejection at `repository_lifecycle.go` + 2 doc-comment "
            "updates at `registry.go` + `registry_compose_ssot_test.go`) + 1 critical build-regression "
            "fix (`worker_execution.go:246` referencer renamed from removed package-local alias to "
            "canonical sentinel). godlike/06 SSOT lockstep: this AGENTS.md entry \u2261 CHANGELOG.md "
            "`## Unreleased > ### Fixed` entry \u2248 `architecture/current.yaml#CUTOVER-COMPLETE-WITH-"
            "ARTIFACTS.linked_issues[P0-COMPL-1-MANIFEST-DECISION]` \u2248 "
            "`architecture/deprecations.yaml#REMOTE-COMPLETE-LEGACY.promotion`. Honest-limitation "
            "(godlike/07): typed-service gate structurally unreachable today (Validated rejects empty "
            "Artifacts pre-TX); SQL-layer is the only active enforcement until BACKFILL softens "
            "Validated. Forward-pointer (godlike/06 SSOT split): \"produces artifacts\" lives in 2 "
            "surfaces (Registry + SQLiteStore map); `PR-Boot-Registry-ProducesArtifactsMap-"
            "AgreesWithSQLiteStore` adds the agreement test.\n"
        )
        p.write_text(t[: nl + 1] + entry + t[nl + 1 :], encoding=ENCODING)
        print("OK 2D: AGENTS.md mirror entry appended")
    return 0


if __name__ == "__main__":
    sys.exit(main())
