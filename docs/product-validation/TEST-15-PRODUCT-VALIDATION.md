# Test 15 — Product validation

This test measures product usability, not Chronon engine correctness. It runs in
PipelineGen against operator/customer feedback and never adds business logic to
Chronon3d.

## Input

Provide a JSON document matching `fixtures/test-15-feedback.schema.json`:

```json
{
  "subjects": [
    {"id":"subject-1", "q1":1, "q2":1, "q3_minutes":15}
  ]
}
```

Scores are recorded observations. Synthetic/default values are invalid.

## Contract

```text
at least 5 subjects
at least 3 of 5 q1 scores >= 1
median q1 >= 1
median q2 >= 1
median q3_minutes >= 10
no q3_minutes == 0
```

Run:

```bash
python3 tools/product_validation.py test-15 --input <feedback.json>
```

Exit `0` means the supplied evidence satisfies the contract. Exit `1` means
real evidence is present but fails. Exit `2` means the evidence is missing or
malformed.
