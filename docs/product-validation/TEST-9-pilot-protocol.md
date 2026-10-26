# Test 9 — Pilota cliente reale 7gg (protocol)

This protocol measures product usefulness with real clients over 7 days, not
engine correctness. It runs in PipelineGen; Chronon3d only renders the
evidence (`chronon3d_cli render`) and never learns about the pilot. Sharded
from Test 15 (10-min per-subject) on the **timeline axis only**: the
verbatim-question shape is identical.

## Cohort (role-balanced, ≥5 clients)

| Role | Seats | Why |
|---|---|---|
| founder | 2 | judges replacement of the previous pipeline end to end |
| PM | 2 | judges throughput and handoff friction |
| designer | 2 | judges visual quality without architecture context |
| operator | 2 | judges manual touches and failure recovery |

≥5 completed clients are required for CAT-9; recruit up to 8 to absorb drops.
No architecture explanations to clients: if the product needs an explanation
to be useful, that is a finding, not a support session.

## 7-day timeline SOP

| Day | Activity |
|---|---|
| 0 | Onboarding ≤30 min: access only, baseline recorded (`hours/video` on the previous pipeline, `videos/week`), no feature tour beyond what the client asks |
| 1–6 | Real production use; every produced video logged in [`TEST-9-transcript-7gg.md`](TEST-9-transcript-7gg.md) the same day (diary block per client per day) |
| 1–6 | Renders must be real pipeline runs; keep artifact + receipt paths as evidence |
| 7 | Client fills [`TEST-9-feedback-form.md`](TEST-9-feedback-form.md) unassisted; interview ≤20 min, verbatim answers preserved |

## Evidence

- Render evidence per video: artifact path + receipt from `chronon3d_cli render`.
- Daily diary: [`TEST-9-transcript-7gg.md`](TEST-9-transcript-7gg.md).
- Day-7 forms: [`TEST-9-feedback-form.md`](TEST-9-feedback-form.md), one per client.
- Aggregate the diaries into `transcripts/aggregate.md` (median per question,
  per-role split, verbatim quotes kept).

## CAT-9 (PASS criterion)

```text
median Q1 >= +1 across >= 5 clients   (better than previous pipeline)
median Q2 >= +1                       (would use it again)
median Q3 >= 30 min/video             (time saved per video, 7gg window)
```

Failing any line is a FAIL of CAT-9; the pilot report must say which line
failed and by how much.

## §honesty cert (PARTIAL)

- Protocol + form + transcript = **HARNESS-COMPLETE** (this directory).
- Pilot-runtime = **DEFERRED**: CAT-9 requires real clients over 7 real days.
  It cannot be machine-verified and must never be seeded with synthetic data.
- Recorded observations only. A missing answer is a missing answer; no
  defaults, no interpolation, no silent subject drops.

## Cross-link

- [`PRODUCT-VALIDATION-AGGREGATOR.md`](PRODUCT-VALIDATION-AGGREGATOR.md) row 9
- [`TEST-15-PRODUCT-VALIDATION.md`](TEST-15-PRODUCT-VALIDATION.md) — 10-min per-subject sibling (same verbatim-question shape)
