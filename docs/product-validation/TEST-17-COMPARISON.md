# Test 17 — Product comparison

PipelineGen compares Chronon3D, the previous pipeline and Remotion using
identical assets, resolution, duration, codec, hardware and output
requirements. This is product validation; Chronon3d only emits engine
telemetry and does not know the comparison or business verdict.

## Input

Provide a JSON document matching `fixtures/test-17-comparison.schema.json`.
It must contain 24 cells: 8 metrics for each of 3 products. Every cell has a
value and one evidence tier:

```text
EVIDENCED | SOURCED | ESTIMATED
```

The report must also identify two `[RADICAL W]` cells and one `[HONEST L]`
limitation.

## Contract

```bash
python3 tools/product_validation.py test-17 --input <comparison.json>
```

Pass requires 24/24 populated cells, valid evidence tiers, exactly two
`RADICAL W` markers and exactly one `HONEST L` marker. Missing or fabricated
data is a blocked input, not a pass.
