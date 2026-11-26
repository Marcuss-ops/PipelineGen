#!/usr/bin/env python3
"""Read one value out of architecture/releases.json.

The pin file is the single owner of the external revision facts consumed by the
Makefile, the container image and the CI workflows. Everything that needs a pin
goes through this reader instead of copying the literal into a second place.

Usage:
    python3 scripts/ci/release_pin.py brainrust.git_sha [path/to/releases.json]
"""

import json
import pathlib
import sys


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: release_pin.py <dotted.key> [releases.json]", file=sys.stderr)
        return 2
    default = pathlib.Path(__file__).resolve().parents[2] / "architecture" / "releases.json"
    pin_path = pathlib.Path(argv[2]) if len(argv) > 2 else default
    try:
        value = json.loads(pin_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"release pin {pin_path}: {exc}", file=sys.stderr)
        return 1
    for part in argv[1].split("."):
        if not isinstance(value, dict) or part not in value:
            print(f"release pin {pin_path}: missing key {argv[1]}", file=sys.stderr)
            return 1
        value = value[part]
    print(value)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
