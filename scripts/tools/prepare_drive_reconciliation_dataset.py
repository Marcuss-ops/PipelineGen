#!/usr/bin/env python3
"""Prepare the deterministic Drive reconciliation dataset locally.

This command deliberately has no Google API, HTTP, SQLite, Qdrant, or
PipelineGen generation dependency. It materializes the checked-in controlled
fixture so the later live verification can use one stable five-asset/five-scene
scenario without accidentally creating cloud data.

Usage:
    python3 scripts/tools/prepare_drive_reconciliation_dataset.py \
        --dry-run --output-dir /tmp/pipelinegen-drive-reconciliation

The explicit --dry-run flag is mandatory. Any future live provisioning must be
implemented as a separate, reviewed command rather than added here.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


EXPECTED_STATES = {
    "VERIFIED",
    "UPDATED",
    "TRASHED",
    "INACCESSIBLE",
    "MISSING",
}
EXPECTED_BOXERS = {
    "Mike Tyson",
    "Muhammad Ali",
    "Manny Pacquiao",
    "Floyd Mayweather",
    "Sugar Ray Robinson",
}


def default_fixture_path() -> Path:
    return (
        Path(__file__).resolve().parents[2]
        / "internal"
        / "infrastructure"
        / "drive"
        / "testdata"
        / "controlled_reconciliation_assets.json"
    )


def load_and_validate(fixture_path: Path) -> dict[str, Any]:
    try:
        dataset = json.loads(fixture_path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise ValueError(f"read fixture {fixture_path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise ValueError(f"decode fixture {fixture_path}: {exc}") from exc

    if dataset.get("scenario") != "drive_database_reconciliation":
        raise ValueError("fixture scenario must be drive_database_reconciliation")

    assets = dataset.get("assets")
    script = dataset.get("script")
    scenes = script.get("scenes") if isinstance(script, dict) else None
    if not isinstance(assets, list) or len(assets) != 5:
        raise ValueError("fixture must contain exactly five assets")
    if not isinstance(scenes, list) or len(scenes) != 5:
        raise ValueError("fixture script must contain exactly five scenes")

    asset_ids: set[str] = set()
    drive_file_ids: set[str] = set()
    scene_ids: set[str] = set()
    states: list[str] = []

    for asset in assets:
        if not isinstance(asset, dict):
            raise ValueError("each asset must be an object")
        asset_id = str(asset.get("asset_id", "")).strip()
        drive_file_id = str(asset.get("drive_file_id", "")).strip()
        boxer = str(asset.get("boxer", "")).strip()
        state = str(asset.get("expected_state", "")).strip()
        initial_lifecycle_state = str(asset.get("initial_lifecycle_state", "")).strip()
        drive = asset.get("drive")

        if not asset_id or not drive_file_id:
            raise ValueError(f"asset {boxer or '<unknown>'} needs asset_id and drive_file_id")
        if asset_id in asset_ids:
            raise ValueError(f"duplicate asset_id: {asset_id}")
        if drive_file_id in drive_file_ids:
            raise ValueError(f"duplicate drive_file_id: {drive_file_id}")
        if boxer not in EXPECTED_BOXERS:
            raise ValueError(f"unexpected boxer: {boxer}")
        if state not in EXPECTED_STATES:
            raise ValueError(f"unexpected expected_state for {asset_id}: {state}")
        if initial_lifecycle_state != "ACTIVE":
            raise ValueError(
                f"initial_lifecycle_state for {asset_id} must be ACTIVE, "
                f"got {initial_lifecycle_state or '<empty>'}"
            )
        if not isinstance(drive, dict):
            raise ValueError(f"asset {asset_id} needs controlled Drive metadata")

        if state == "UPDATED":
            old_link = str(asset.get("drive_link", "")).strip()
            canonical_link = str(drive.get("web_view_link", "")).strip()
            if not old_link or not canonical_link or old_link == canonical_link:
                raise ValueError("UPDATED asset must have a distinct old and canonical link")
        elif state == "VERIFIED":
            if str(asset.get("drive_link", "")).strip() != str(drive.get("web_view_link", "")).strip():
                raise ValueError("VERIFIED asset link must equal controlled web_view_link")
        else:
            if not str(asset.get("drive_link", "")).strip():
                raise ValueError(f"{state} asset must retain an initial link for reconciliation")

        if state in {"VERIFIED", "UPDATED", "TRASHED"}:
            if str(drive.get("id", "")).strip() != drive_file_id:
                raise ValueError(f"{asset_id} controlled Drive id does not match drive_file_id")
        if state == "TRASHED" and drive.get("trashed") is not True:
            raise ValueError("TRASHED asset must have trashed=true")
        if state == "INACCESSIBLE" and drive.get("error_status") != 403:
            raise ValueError("INACCESSIBLE asset must have controlled error_status=403")
        if state == "MISSING" and drive.get("error_status") != 404:
            raise ValueError("MISSING asset must have controlled error_status=404")

        asset_ids.add(asset_id)
        drive_file_ids.add(drive_file_id)
        states.append(state)

    if set(asset.get("boxer") for asset in assets) != EXPECTED_BOXERS:
        raise ValueError("fixture must contain exactly the five expected boxers")
    if {state: states.count(state) for state in EXPECTED_STATES} != {state: 1 for state in EXPECTED_STATES}:
        raise ValueError("fixture must contain one asset for every expected reconciliation state")

    for index, scene in enumerate(scenes):
        if not isinstance(scene, dict):
            raise ValueError("each scene must be an object")
        scene_id = str(scene.get("id", "")).strip()
        binding = scene.get("binding")
        if not scene_id or scene_id in scene_ids:
            raise ValueError(f"duplicate or empty scene id at index {index}")
        if scene.get("index") != index or not isinstance(binding, dict):
            raise ValueError(f"scene {scene_id} must have its ordered binding")
        asset_id = str(binding.get("asset_id", "")).strip()
        if asset_id not in asset_ids:
            raise ValueError(f"scene {scene_id} references unknown asset_id {asset_id}")
        asset = next(item for item in assets if item["asset_id"] == asset_id)
        if scene.get("scene_id", scene_id) != scene_id:
            raise ValueError(f"scene {scene_id} has inconsistent scene identity")
        if scene.get("boxer") != asset["boxer"]:
            raise ValueError(f"scene {scene_id} boxer does not match its asset")
        if binding.get("drive_file_id") != asset["drive_file_id"] or binding.get("drive_link") != asset["drive_link"]:
            raise ValueError(f"scene {scene_id} binding does not match its asset")
        scene_ids.add(scene_id)

    if len(scene_ids) != 5 or {asset["asset_id"] for asset in assets} != {
        scene["binding"]["asset_id"] for scene in scenes
    }:
        raise ValueError("each controlled asset must be referenced by exactly one scene")

    return dataset


def build_specscene(dataset: dict[str, Any]) -> dict[str, Any]:
    scenes = []
    for scene in dataset["script"]["scenes"]:
        binding = scene["binding"]
        scenes.append(
            {
                "id": scene["id"],
                "index": scene["index"],
                "text": f"{scene['boxer']} controlled reconciliation scene",
                "bindings": {
                    "clip": {
                        "clip_id": binding["asset_id"],
                        "drive_file_id": binding["drive_file_id"],
                        "drive_link": binding["drive_link"],
                    }
                },
            }
        )
    return {"version": 1, "scenes": scenes}


def materialize(dataset: dict[str, Any], output_dir: Path, fixture_path: Path) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    assets_dir = output_dir / "assets"
    assets_dir.mkdir(exist_ok=True)

    try:
        source_fixture = str(fixture_path.resolve().relative_to(Path(__file__).resolve().parents[2]))
    except ValueError:
        source_fixture = str(fixture_path)

    manifest = {
        "scenario": dataset["scenario"],
        "mode": "local_fixture_dry_run",
        "live_generation_executed": False,
        "network_accessed": False,
        "source_fixture": source_fixture,
        "assets": dataset["assets"],
        "script": dataset["script"],
        "expected_counts": {state.lower(): 1 for state in sorted(EXPECTED_STATES)},
    }
    (output_dir / "manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )
    (output_dir / "specscene.json").write_text(
        json.dumps(build_specscene(dataset), indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )
    for asset in dataset["assets"]:
        (assets_dir / f"{asset['asset_id']}.json").write_text(
            json.dumps(asset, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
        )


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true", help="required safety switch; no live mode exists")
    parser.add_argument("--fixture", type=Path, default=default_fixture_path())
    parser.add_argument("--output-dir", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    if not args.dry_run:
        print("refusing to prepare Drive data without --dry-run", file=sys.stderr)
        return 2
    try:
        dataset = load_and_validate(args.fixture)
        materialize(dataset, args.output_dir, args.fixture)
    except ValueError as exc:
        print(f"dataset preparation failed: {exc}", file=sys.stderr)
        return 1
    print(f"prepared local dry-run dataset in {args.output_dir}")
    print("live_generation_executed=false network_accessed=false assets=5 scenes=5")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
