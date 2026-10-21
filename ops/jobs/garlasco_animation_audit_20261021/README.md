# Garlasco 01 — animation audit (2026-10-21)

This directory holds the reproducible audit of every overlay animation family on
the Garlasco 01 script: maps, short/long phrases, multi-image and multi-entity
groups, group final animations, bullets and charts.

The audit is split in two because the final Master handoff is a strictly weaker
contract than the per-item overlay render:

| Payload | Purpose | Contract |
| --- | --- | --- |
| `payloads/overlay.json` | certify extraction, identity, timing and motion of every overlay | per-item Chronon renders, full-frame |
| `payloads/final.json` | certify the same plan through the Master 51 composite | `mode=replace`, one overlay per frame interval |

`overlay.json` must pass before `final.json` is run: a render is expensive and
the final handoff cannot rescue a broken plan.

## Reproduce

```bash
cd refactored                      # PipelineGen root

# 1. Build both payloads from the untouched r9 base.
python3 ops/jobs/garlasco_animation_audit_20261021/build_payloads.py

# 2a. Cheap plan-only audit: compile the plan without overlay renders.
#     Writes evidence/plan/{plan-only.json,full.json,plan.json}.
make audit-plan PAYLOAD=ops/jobs/garlasco_animation_audit_20261021/payloads/overlay.json \
  OUT=ops/jobs/garlasco_animation_audit_20261021/evidence/plan
python3 ops/jobs/garlasco_animation_audit_20261021/audit_plan.py \
  ops/jobs/garlasco_animation_audit_20261021/evidence/plan/full.json

# 2b. Run the real overlay audit (long-running; writes evidence/<profile>.*).
ops/jobs/garlasco_animation_audit_20261021/run_audit.sh overlay

# 3. Score the produced plan against G01..C02.
python3 ops/jobs/garlasco_animation_audit_20261021/audit_plan.py \
  ops/jobs/garlasco_animation_audit_20261021/evidence/overlay.full.json

# 4. Only after step 3 has no FAIL: the final handoff.
ops/jobs/garlasco_animation_audit_20261021/run_audit.sh final
python3 ops/jobs/garlasco_animation_audit_20261021/audit_plan.py \
  ops/jobs/garlasco_animation_audit_20261021/evidence/final.full.json
```

`audit_plan.py` writes `*.audit.json` and `*.audit.md` next to the job body. It
exits 1 when any check fails, so the audit is usable in CI.

`make audit-plan` is the fast path: it derives a payload with
`output.render.enabled=false` (and docs off), keeping narration/voiceover intact
because the plan needs certified speech timing. Plan compilation happens in the
audio stage, before the overlay render fan-out, so the semantic plan is
identical while the expensive per-item renders are skipped. Use it for every
iteration; reserve `run_audit.sh` for the render/visual certification.

`run_audit.sh` is safe to rerun for the same payload: it fingerprints the JSON,
uses a stable `Idempotency-Key`, and resumes polling the recorded job instead
of submitting a duplicate. The generation runner also resumes from its durable
checkpoint and skips stages already completed **within that run**. A separate
`overlay` run and a `final` run have different payloads/run IDs, so the final
handoff cannot inherit completed audio or analysis stages from the earlier
overlay job; `force_refresh=false` only reuses assets in providers that support
that cache. To avoid doing the work twice, submit `final` once and use
`overlay`/plan-only only when you need to inspect or adjust the plan first.
Failed or cancelled jobs are saved as evidence and return a nonzero exit code;
the launcher never reports them as successful or silently creates a replacement.

## What the payload changes relative to r9

- every overlay category is enabled, including `bullets` and `charts`;
- charts also get the explicit `enable_charts` opt-in;
- `overlay_group_limits.bullets = 4` opens the independent bullet lane, so the
  10 short + 10 long phrase contract is not reduced by chapter cards;
- the style block is replaced with the verbatim/grounding contract (Stasi vs
  Sempio, real places only, charts only for comparable numbers);
- `final_job` is the only difference between the two payloads.

`force_refresh` stays `false` (asset reuse where supported) while the plan
itself must be new: the audit scores the plan produced by this run, never a
cached one.

## Check map

| ID | Meaning | Owner |
| --- | --- | --- |
| G01/G02 | 2, and 3–5, distinct images in one composite | planner + RenderingGen composite lowering |
| E01/E02 | co-mentioned portraits with correct captions; no asset reuse | entity composition + budget dedupe |
| M01 | maps of narrated real places | map planner |
| S01/L01 | 2–5 and 6–16 word phrases with distinct motions | phrase lanes + motion selector |
| F01 | group closing/entrance choreography declared | group motion selector |
| B01/B02 | ≥2 grounded bullets, each with a visible motion | independent bullet lane |
| C01 | no fabricated comparison (dates are not a chart) | chart grounding |
| C02 | positive technical chart canary | `e2e_cert/T04_bullets_charts_20261020` |
| X01 | chart group is one artifact (replace-mode atomicity) | final handoff gate |
| X02 | other same-frame collisions | final handoff scheduler |
