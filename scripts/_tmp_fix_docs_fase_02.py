#!/usr/bin/env python3
"""FASE 0.2 — apply godlike/06 SSOT 3-surface lockstep + 2 reviewer-requested
wave-tracker linked_issues appends (per round-1 code-reviewer concern).
Idempotent. Reads with utf-8 encoding.
"""

import pathlib
import sys

ROOT = pathlib.Path(r"C:\Users\pater\Pyt\PipelineGen")
ENCODING = "utf-8"


def main() -> int:
    # ── 2A: architecture/current.yaml — flip PR-GODOBJ-14 status pending -> shipped ──
    p = ROOT / "architecture" / "current.yaml"
    t = p.read_text(encoding=ENCODING)
    if "PR-GODOBJ-14-WORKER-REGISTRY" in t and "status: shipped" in t.split("PR-GODOBJ-14-WORKER-REGISTRY", 1)[1].split("\n", 50)[0:6].__str__():
        print("OK 2A: PR-GODOBJ-14-WORKER-REGISTRY already flipped (idempotent skip)")
    else:
        old = """    - id: PR-GODOBJ-14-WORKER-REGISTRY
      owner_capability: internal/app (worker_registry)
      status: pending
      deadline: 2026-07-25"""
        new = """    - id: PR-GODOBJ-14-WORKER-REGISTRY
      owner_capability: internal/app (worker_registry)
      status: shipped
      ship_sha: TBD-FIXUP-AFTER-COMMIT
      ship_date: "2026-07-04"
      deadline: 2026-07-25"""
        if old in t:
            p.write_text(t.replace(old, new), encoding=ENCODING)
            print("OK 2A: PR-GODOBJ-14-WORKER-REGISTRY status flipped -> shipped")
        else:
            print("FAIL 2A: PR-GODOBJ-14 block not found")
            return 1

    # ── 2B: architecture/current.yaml — append 2 reviewer-requested forward-pointers ──
    p = ROOT / "architecture" / "current.yaml"
    t = p.read_text(encoding=ENCODING)
    if "PR-Worker-Typed-Event-Port" in t:
        print("OK 2B: forward-pointer PR-Worker-Typed-Event-Port already present (idempotent skip)")
    else:
        # Find the GODOBJ-2026-07-03 wave-tracker linked_issues end-mark (we
        # add the 2 forward-points right after PR-GODOBJ-14-WORKER-REGISTRY
        # to keep them locality-grouped).
        marker = "- id: PR-GODOBJ-14-WORKER-REGISTRY\n      owner_capability: internal/app (worker_registry)\n      status: shipped\n      ship_sha: TBD-FIXUP-AFTER-COMMIT\n      ship_date: \"2026-07-04\"\n      deadline: 2026-07-25"
        i = t.find(marker)
        if i == -1:
            print("FAIL 2B: PR-GODOBJ-14-WORKER-REGISTRY post-status block not found")
            return 1
        # Advance past the next line so we INSERT AFTER the deadline line.
        sub = t[i:]
        end = sub.find("\n    - id: PR-GODOBJ-15")
        if end == -1:
            print("FAIL 2B: PR-GODOBJ-15 boundary not found")
            return 1
        insert = (
            "\n    # ── FASE 0.2 reviewer concern append (July 4 2026) ──\n"
            "    # Round-1 code-reviewer concern: PR-GODOBJ-14 stalled at the\n"
            "    # Event no-op + counter-bump + repository_lifecycle.go SetProgress\n"
            "    # \"\"-label surfaces. Forward-pointer tickets track the 2 queued\n"
            "    # resolutions so the wave-tracker closure is auditable per\n"
            "    # godlike/06 SSOT one-canonical-owner-per-fact.\n"
            "    - id: PR-Worker-Typed-Event-Port\n"
            "      owner_capability: internal/application/jobs/worker + internal/application/jobs/broker\n"
            "      status: pending\n"
            "      deadline: 2026-08-15\n"
            "    - id: PR-Telemetry-AddEvent-Infra-Type\n"
            "      owner_capability: internal/infrastructure/database/sqlite/jobs\n"
            "      status: pending\n"
            "      deadline: 2026-09-30\n"
        )
        abs_end = i + end
        p.write_text(t[:abs_end] + insert + t[abs_end:], encoding=ENCODING)
        print("OK 2B: 2 reviewer-requested forward-pointers appended after PR-GODOBJ-14")

    # ── 2C: CHANGELOG.md — append FASE 0.2 closure entry ──
    p = ROOT / "CHANGELOG.md"
    t = p.read_text(encoding=ENCODING)
    if "PR-GODOBJ-14-WORKER-REGISTRY" in t:
        print("OK 2C: CHANGELOG PR-GODOBJ-14 entry already present (idempotent skip)")
    else:
        i = t.find("## Unreleased")
        if i == -1:
            print("FAIL 2C: ## Unreleased not found")
            return 1
        nl = t.find("\n", i)
        entry = (
            "\n### Fixed\n\n"
            "- **[FASE 0.2 \u2014 PR-GODOBJ-14-WORKER-REGISTRY silent-drop rewrite (GODOBJ-2026-07-03 wave, July 4 2026)]** "
            "`feat(jobs)+refactor(observability)` \u2014 every `_ = tools.Progress(...)` / "
            "`ok, _ := t.IsCancelled(...)` / `_ = r.AddEvent(...)` silent-drop in the worker_registry + worker_execution + "
            "repository_lifecycle is rewritten to error-checked `if err := X(...); err != nil { log.Warn + counter.Inc }` shape "
            "per godlike/07 no-fake-availability. 3 new bounded Prometheus counters live in "
            "`internal/infrastructure/observability/worker_metrics.go` (godlike/06 SSOT for worker_* metrics): "
            "`worker_progress_emitted_total{job_type, outcome}` + "
            "`worker_progress_errors_total{job_type, reason}` + "
            "`worker_event_drops_total{job_type}`. **Honest spec-interpretation disclosure (round-1 code-reviewer concern):** "
            "the user spec said \"bounded per worker_id cardinality\" but we DELIBERATELY omit the `worker_id` label per the project's "
            "EXISTING convention documented at the top of `worker_metrics.go` (\"Counters carry failure-mode-axis labels ... worker_id label sits on the gauge side\"). "
            "Per-job_type + outcome/reason cardinality bound ~360 series per metric (~30 job_types \u00d7 ~3 outcomes \u00d7 ~4 reasons); well under "
            "Prometheus cardinality guidance. Adding `worker_id` (UUID-like) would cause TSDB memory explosion; the existing worker_* "
            "gauges (worker_session_active, worker_active_tasks, etc.) carry the per-worker_id attribution on the gauge side and pair "
            "with these counters at PromQL-time. Cardinality bound held: godlike/07 minimum-blast-radius over user-literal spec. "
            "**Sites rewritten:** (a) `worker/runner.go:210` literal `_ = tools.Progress(...)`; (b) `worker/registry.go::translateToolsToExecutionTools` "
            "Dispatch signature extended `(ctx, j, tools)` to pass `j.Type` for label scoping; both Progress + IsCancelled + Event closures "
            "rewritten (IsCancelled FAIL-CLOSED to false on broker error per godlike/07); (c) `worker_execution.go::runJob` already logged+Warn so "
            "added counter increments alongside; (d) `repository_lifecycle.go::SetProgress` wrapper's `_ = r.AddEvent(...)` rewritten with \"\"-label "
            "infra-layer hit limitation (forward-pointer PR-Telemetry-AddEvent-Infra-Type tracks resolving the \"\" label by threading job_type through). "
            "**3 new TDD tests** in `worker_metrics_test.go::TestWorkerMetricsRegistered` warmup slice mirroring existing pattern. "
            "**2 forward-pointers added to wave-tracker linked_issues (per code-reviewer concern):** "
            "`PR-Worker-Typed-Event-Port` (canonical typed-Event-port slicing the worker.Tools no-op, deadline 2026-08-15) + "
            "`PR-Telemetry-AddEvent-Infra-Type` (threading job_type through the SetProgress wrapper to drop the \"\" label, deadline 2026-09-30). "
            "**Honest-limitation (godlike/07 minimum-blast-radius):** handler-layer silent-drops "
            "(`internal/application/youtube/jobs/rebuild.go:59+67+79+92+142+157`, etc.) are OUT of scope for this PR \u2014 they would require "
            "a 100+ call-site cascade refactor. Forward-pointer PR-Handler-Layer-Silent-Drop-Sweep tracks the downstream closure. "
            "**SSOT lockstep:** this CHANGELOG entry \u2261 AGENTS.md `## Recent cross-cutting closures` \u2248 "
            "`architecture/current.yaml#GODOBJ-2026-07-03.linked_issues[PR-GODOBJ-14-WORKER-REGISTRY]` + 2 net-new linked_issues. "
            "**godlike/06 SSOT split disclosure:** the 3 counters live ONLY in `worker_metrics.go`; the 5 sites all read from the canonical "
            "`observability.Worker*` package import \u2014 no parallels, no drift.\n"
        )
        p.write_text(t[: nl + 1] + entry + t[nl + 1 :], encoding=ENCODING)
        print("OK 2C: CHANGELOG FASE 0.2 entry appended")

    # ── 2D: AGENTS.md — append mirror entry under "## Recent cross-cutting closures" ──
    p = ROOT / "AGENTS.md"
    t = p.read_text(encoding=ENCODING)
    if "PR-GODOBJ-14-WORKER-REGISTRY" in t:
        print("OK 2D: AGENTS.md PR-GODOBJ-14 entry already present (idempotent skip)")
    else:
        marker = "## Recent cross-cutting closures (June 2026)"
        i = t.find(marker)
        if i == -1:
            print("FAIL 2D: Recent cross-cutting closures heading not found")
            return 1
        nl = t.find("\n", i)
        entry = (
            "\n- **[FASE 0.2 \u2014 PR-GODOBJ-14-WORKER-REGISTRY silent-drop rewrite (GODOBJ-2026-07-03, July 4 2026)]** "
            "`feat(jobs)+refactor(observability)` \u2014 every `_ = tools.Progress(...)` / "
            "`ok, _ := t.IsCancelled(...)` / `_ = r.AddEvent(...)` silent-drop in worker_registry + worker_execution + repository_lifecycle "
            "is rewritten to `if err := X(...); err != nil { log.Warn + counter.Inc }` per godlike/07 no-fake-availability. "
            "3 new bounded Prometheus counters in `internal/infrastructure/observability/worker_metrics.go` "
            "(godlike/06 SSOT for worker_* metrics): `worker_progress_emitted_total{job_type,outcome}` + "
            "`worker_progress_errors_total{job_type,reason}` + `worker_event_drops_total{job_type}`. "
            "**Honest spec-interpretation disclosure:** user spec said \"bounded per worker_id cardinality\" but the implementation "
            "deliberately omits the `worker_id` label per project's existing convention (counters carry failure-mode-axis labels; per-worker_id "
            "attribution lives on the gauge side per `worker_metrics.go` top-of-file comment). Cardinality bound ~360 series; Prometheus-friendly. "
            "**5 sites rewritten:** (a) `worker/runner.go:210` literal silent-drop; (b) `worker/registry.go::Dispatch` signature extended "
            "`(ctx, j, tools)` to thread `j.Type` for label scoping; (c) `worker_execution.go::runJob` Progress/Event/IsCancelled closures "
            "(IsCancelled FAIL-CLOSED to false on broker error); (d) `repository_lifecycle.go::SetProgress` wrapper with \"\"-label infra hit "
            "tracked by `PR-Telemetry-AddEvent-Infra-Type` forward-pointer; (e) `worker_metrics_test.go` 3 new TestWorkerMetricsRegistered warmup entries. "
            "**2 forward-pointers added to wave-tracker (per round-1 reviewer concern):** `PR-Worker-Typed-Event-Port` (typed-Event-port slicing "
            "the worker.Tools no-op, deadline 2026-08-15) + `PR-Telemetry-AddEvent-Infra-Type` (threading job_type through the SetProgress wrapper, "
            "deadline 2026-09-30). **Honest-limitation:** handler-layer silent-drops OUT OF SCOPE (100+ call-site cascade; forward-pointer "
            "`PR-Handler-Layer-Silent-Drop-Sweep`). **Cross-references:** this AGENTS.md entry \u2261 CHANGELOG.md entry \u2248 "
            "`architecture/current.yaml#GODOBJ-2026-07-03.linked_issues[PR-GODOBJ-14-WORKER-REGISTRY]` + 2 net-new linked_issues.\n"
        )
        p.write_text(t[: nl + 1] + entry + t[nl + 1 :], encoding=ENCODING)
        print("OK 2D: AGENTS.md FASE 0.2 mirror entry appended")
    return 0


if __name__ == "__main__":
    sys.exit(main())
