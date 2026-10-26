#!/usr/bin/env python3
"""product_validation.py — Test 15 / Test 17 product-validation validator.

Migrated into PipelineGen scripts/ (the core tools/ directory was purged by
commit 7e6965aab; PRODUCT-VALIDATION-AGGREGATOR rows 15/17 point here).

Usage:
    python3 scripts/product_validation.py test-15 --input <feedback.json>
    python3 scripts/product_validation.py test-17 --input <comparison.json>

Normative inputs:
    docs/product-validation/fixtures/test-15-feedback.schema.json
    docs/product-validation/fixtures/test-17-comparison.schema.json

Exit codes (TEST-15/TEST-17 contract):
    0 = PASS       the supplied evidence satisfies the contract
    1 = FAIL       real, well-formed evidence is present but fails the contract
    2 = BLOCKED    evidence is missing or malformed (bad JSON, schema
                   violation, unpopulated cell) — a blocked input, never a pass

Rule of thumb: structural problems exit 2, contract verdicts exit 1/0.
Fabricated/defaulted data cannot be detected by machine; §honesty forbids it
upstream. No third-party dependencies: stdlib only.
"""

import argparse
import json
import statistics
import sys
from pathlib import Path

FIXTURES = Path(__file__).resolve().parent.parent / "docs" / "product-validation" / "fixtures"

RADICAL_W = "RADICAL W"
HONEST_L = "HONEST L"


# ── Minimal JSON-Schema subset validator (fixtures are the normative source) ──

def _type_ok(value, expected):
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    return True


def validate_schema(value, schema, path="$"):
    """Validate against the JSON-Schema subset used by the two fixture files:
    type, required, properties, additionalProperties, items, minItems,
    maxItems, minLength, minimum, enum. Returns a list of error strings."""
    errors = []
    expected = schema.get("type")
    if expected and not _type_ok(value, expected):
        errors.append(f"{path}: expected type {expected}, got {type(value).__name__}")
        return errors

    if "enum" in schema and value not in schema["enum"]:
        errors.append(f"{path}: {value!r} not in enum {schema['enum']}")

    if isinstance(value, dict):
        for key in schema.get("required", []):
            if key not in value:
                errors.append(f"{path}: missing required property {key!r}")
        props = schema.get("properties", {})
        for key, sub in value.items():
            if key in props:
                errors.extend(validate_schema(sub, props[key], f"{path}.{key}"))
            elif schema.get("additionalProperties") is False:
                errors.append(f"{path}: additional property {key!r} not allowed")

    if isinstance(value, list):
        if "minItems" in schema and len(value) < schema["minItems"]:
            errors.append(f"{path}: {len(value)} items < minItems {schema['minItems']}")
        if "maxItems" in schema and len(value) > schema["maxItems"]:
            errors.append(f"{path}: {len(value)} items > maxItems {schema['maxItems']}")
        if "items" in schema:
            for i, item in enumerate(value):
                errors.extend(validate_schema(item, schema["items"], f"{path}[{i}]"))

    if isinstance(value, str) and "minLength" in schema and len(value) < schema["minLength"]:
        errors.append(f"{path}: string shorter than minLength {schema['minLength']}")
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if "minimum" in schema and value < schema["minimum"]:
            errors.append(f"{path}: {value} < minimum {schema['minimum']}")

    return errors


def load_document(path):
    """Load the evidence document. Returns (doc, None) or (None, reason)."""
    try:
        raw = Path(path).read_text(encoding="utf-8")
    except OSError as exc:
        return None, f"input not readable: {exc}"
    try:
        return json.loads(raw), None
    except json.JSONDecodeError as exc:
        return None, f"input is not valid JSON: {exc}"


# ── Test 15 — product validation (feedback) ──
#
# Contract (TEST-15-PRODUCT-VALIDATION.md):
#   at least 5 subjects
#   at least 3 of 5 q1 scores >= 1
#   median q1 >= 1
#   median q2 >= 1
#   median q3_minutes >= 10
#   no q3_minutes == 0

def run_test_15(doc, schema):
    problems = validate_schema(doc, schema)
    if problems:
        return 2, [f"BLOCKED (malformed): {p}" for p in problems]

    subjects = doc["subjects"]
    q1 = [s["q1"] for s in subjects]
    q2 = [s["q2"] for s in subjects]
    q3 = [s["q3_minutes"] for s in subjects]

    failures = []
    if len(subjects) < 5:
        failures.append(f"at least 5 subjects: got {len(subjects)}")
    positive_q1 = sum(1 for v in q1 if v >= 1)
    if positive_q1 < 3:
        failures.append(f"at least 3 q1 >= 1: got {positive_q1}")
    if statistics.median(q1) < 1:
        failures.append(f"median q1 >= 1: got {statistics.median(q1)}")
    if statistics.median(q2) < 1:
        failures.append(f"median q2 >= 1: got {statistics.median(q2)}")
    if statistics.median(q3) < 10:
        failures.append(f"median q3_minutes >= 10: got {statistics.median(q3)}")
    zero_q3 = [s["id"] for s in subjects if s["q3_minutes"] == 0]
    if zero_q3:
        failures.append(f"no q3_minutes == 0: violated by {zero_q3}")

    if failures:
        return 1, [f"FAIL: {f}" for f in failures]
    return 0, [
        f"PASS test-15: {len(subjects)} subjects, "
        f"median q1={statistics.median(q1)}, q2={statistics.median(q2)}, "
        f"q3={statistics.median(q3)} min"
    ]


# ── Test 17 — product comparison ──
#
# Contract (TEST-17-COMPARISON.md):
#   24/24 populated cells (8 metrics for each of 3 products),
#   valid evidence tiers (schema enum), exactly two RADICAL W markers,
#   exactly one HONEST L marker. Missing or fabricated data is a blocked
#   input, not a pass.

def run_test_17(doc, schema):
    problems = validate_schema(doc, schema)
    if problems:
        return 2, [f"BLOCKED (malformed): {p}" for p in problems]

    cells = doc["cells"]

    empty = [
        f"{c.get('product')}/{c.get('metric')}"
        for c in cells
        if c.get("value") is None or c.get("value") == ""
    ]
    if empty:
        return 2, [f"BLOCKED (missing data): unpopulated cells {empty}"]

    failures = []
    per_product = {}
    for c in cells:
        per_product.setdefault(c["product"], []).append(c["metric"])
    for product in ("chronon3d", "previous_pipeline", "remotion"):
        metrics = per_product.get(product, [])
        if len(metrics) != 8:
            failures.append(f"product {product}: expected 8 cells, got {len(metrics)}")
    metric_sets = {p: set(m) for p, m in per_product.items()}
    if len({frozenset(s) for s in metric_sets.values()}) != 1:
        failures.append("the 8 metrics are not identical across the 3 products")

    radical = [w for w in doc["radical_warnings"] if RADICAL_W in w]
    honest = [h for h in doc["honest_limitations"] if HONEST_L in h]
    if len(radical) != 2:
        failures.append(f"exactly two [{RADICAL_W}] markers: got {len(radical)}")
    if len(honest) != 1:
        failures.append(f"exactly one [{HONEST_L}] marker: got {len(honest)}")

    if failures:
        return 1, [f"FAIL: {f}" for f in failures]
    return 0, [
        f"PASS test-17: 24/24 cells, tiers "
        f"{sorted({c['evidence'] for c in cells})}, "
        f"2x [{RADICAL_W}], 1x [{HONEST_L}]"
    ]


RUNNERS = {
    "test-15": ("test-15-feedback.schema.json", run_test_15),
    "test-17": ("test-17-comparison.schema.json", run_test_17),
}


def main(argv=None):
    parser = argparse.ArgumentParser(description="Test 15 / Test 17 product-validation validator")
    parser.add_argument("test", choices=sorted(RUNNERS))
    parser.add_argument("--input", required=True, help="evidence JSON document")
    args = parser.parse_args(argv)

    fixture_name, runner = RUNNERS[args.test]
    fixture_path = FIXTURES / fixture_name
    try:
        schema = json.loads(fixture_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"BLOCKED (internal): fixture {fixture_path} unreadable: {exc}", file=sys.stderr)
        return 2

    doc, reason = load_document(args.input)
    if doc is None:
        print(f"BLOCKED (malformed): {reason}", file=sys.stderr)
        return 2

    code, messages = runner(doc, schema)
    for line in messages:
        print(line, file=sys.stderr if code != 0 else sys.stdout)
    return code


if __name__ == "__main__":
    sys.exit(main())
