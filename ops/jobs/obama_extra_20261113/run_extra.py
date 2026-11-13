#!/usr/bin/env python3
"""Sequential runner for the two newly supplied Obama videos.

Reuses the proven Obama runner (HTTP retries, one-video-at-a-time jobs,
English backfill, per-asset verification) with its own manifest and state.
"""
from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REFACTORED = HERE.parents[2]
sys.path.insert(0, str(REFACTORED / "ops/jobs/obama_20261113"))

import run_remaining as rr  # noqa: E402

rr.OUT = HERE
rr.MANIFEST = HERE / "manifest.json"
rr.STATE = HERE / "state.json"
rr.REPORT = HERE / "BATCH-REPORT.json"

_original_wait_assets = rr.wait_assets


def wait_assets(asset_ids, timeout_s=600):
    return _original_wait_assets(asset_ids, timeout_s=timeout_s)


rr.wait_assets = wait_assets


if __name__ == "__main__":
    rr.main()
