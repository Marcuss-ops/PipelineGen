# Test 9 — Pilota 7gg (feedback form)

One form per client, filled on **day 7** after a full week of real use.
Recorded observations only: synthetic or defaulted answers invalidate the
pilot (§honesty). Filled unassisted; verbatim comments are copied as spoken.

## Meta

```text
client id:        ____
role:             founder | PM | designer | operator
days active:      ____
videos produced:  ____
previous tool:    ____
baseline hours/video (day 0):  ____
```

## Questions

| # | Question | Scale | Value |
|---|---|---|---|
| Q1 | Compared with your previous pipeline, this week was… | −2 molto peggio … −1 peggio … 0 uguale … +1 meglio … +2 molto meglio | ____ |
| Q2 | Would you use it again next week? | −2 assolutamente no … −1 no … 0 indeciso … +1 sì … +2 assolutamente sì | ____ |
| Q3 | Time saved per video during these 7 days | minutes/video (your own estimate vs day-0 baseline) | ____ |
| — | Verbatim quote: the one sentence you would tell a colleague | free text (preserve wording) | ____ |
| — | NPS | 0–10: probability you recommend it | ____ |

## Aggregation

- Q1/Q2/Q3 feed CAT-9: `median Q1 >= +1`, `median Q2 >= +1`,
  `median Q3 >= 30 min/video`, across ≥5 clients (see
  [`TEST-9-pilot-protocol.md`](TEST-9-pilot-protocol.md)).
- Report medians per role as well: a positive median that hides one role at
  `0` or below is a finding, not a rounding error.
- Quotes and NPS are context, not gates.

## §honesty cert (PARTIAL)

- A missing answer stays empty and is excluded from the median with a note;
  it is never defaulted.
- The day-0 baseline (hours/video on the previous pipeline) anchors Q3; without
  it, Q3 is `NOT BASELINED` and cannot count toward CAT-9.

## Cross-link

- [`TEST-9-pilot-protocol.md`](TEST-9-pilot-protocol.md) — SOP and CAT-9
- [`TEST-9-transcript-7gg.md`](TEST-9-transcript-7gg.md) — daily diary
- [`PRODUCT-VALIDATION-AGGREGATOR.md`](PRODUCT-VALIDATION-AGGREGATOR.md) row 9
