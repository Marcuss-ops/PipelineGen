#!/usr/bin/env python3
"""Run the boxer media matrix without invoking any video renderer.

The matrix exercises the script-generation contract only: SpecScene text,
translation, voiceover metadata, stock bindings and visual assignments.
Remotion is deliberately not called by this runner.
"""

from __future__ import annotations

import argparse
import copy
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[3]
PAYLOAD_PATH = ROOT / "tests/operational/boxers-generate/five_boxers_stock_post_segments.json"
DB_PATH = Path(os.environ.get("VELOX_DB", ROOT / "data/media/media.db.sqlite"))
BOXERS = [
    ("boxer-mike-tyson", "mike tyson"),
    ("boxer-muhammad-ali", "muhammad ali"),
    ("boxer-evander-holyfield", "evander holyfield"),
    ("boxer-floyd-mayweather", "floyd mayweather"),
    ("boxer-sugar-ray-robinson", "sugar ray robinson"),
]
BOXER_IDS = {item[0] for item in BOXERS}
BOXER_SEGMENTS = [item[0] for item in BOXERS]
INTRO_ID = "intro-hook"


def fail(message: str) -> None:
    raise RuntimeError(message)


def load_base() -> dict[str, Any]:
    with PAYLOAD_PATH.open(encoding="utf-8") as handle:
        return json.load(handle)


def output(body: dict[str, Any]) -> dict[str, Any]:
    data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
    items = data.get("items", []) or []
    if items:
        return items[0].get("result", {}).get("output") or items[0].get("output") or {}
    return data.get("output", {})


def request(method: str, path: str, payload: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        fail("VELOX_ADMIN_TOKEN deve essere caricato tramite scripts/with-velox-auth")
    base = os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000").rstrip("/")
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    if method == "POST":
        headers["Idempotency-Key"] = f"boxer-matrix-{uuid.uuid4()}"
    body = json.dumps(payload, ensure_ascii=False).encode() if payload is not None else None
    try:
        with urllib.request.urlopen(urllib.request.Request(base + path, data=body, method=method, headers=headers), timeout=30) as response:
            return response.status, json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        try:
            detail = json.loads(exc.read().decode())
        except json.JSONDecodeError:
            detail = {}
        return exc.code, detail


def wait(job_id: str) -> dict[str, Any]:
    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    while time.time() < deadline:
        status, body = request("GET", f"/api/jobs/{job_id}/full")
        if status >= 400:
            fail(f"GET job {job_id}: HTTP {status}: {body}")
        state = str(body.get("status") or body.get("job", {}).get("status") or "").upper()
        if state in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED"}:
            return body
        if state in {"FAILED", "CANCELLED"}:
            fail(f"job {job_id}: {state}")
        time.sleep(2)
    fail(f"timeout job {job_id}")


def submit(item: dict[str, Any]) -> dict[str, Any]:
    status, body = request("POST", "/api/script/generate", {"version": 2, "preset": "custom", "items": [item]})
    if status not in (200, 202):
        fail(f"generation rejected: HTTP {status}: {body}")
    job_id = body.get("job_id") or body.get("id") or body.get("job", {}).get("id")
    if not job_id:
        fail(f"missing job id: {body}")
    return wait(job_id)


def catalog() -> dict[str, list[str]]:
    """Find three active indexed YouTube clips per subject from SQLite."""
    result: dict[str, list[str]] = {}
    with sqlite3.connect(DB_PATH) as db:
        for segment, subject in BOXERS:
            rows = db.execute(
                """SELECT id FROM media_assets
                   WHERE source='youtube' AND drive_link<>''
                     AND lifecycle_state='ACTIVE' AND index_state='INDEXED'
                     AND lower(name||' '||search_text||' '||metadata_json) LIKE ?
                   ORDER BY id LIMIT 20""",
                (f"%{subject}%",),
            ).fetchall()
            values = [str(row[0]) for row in rows]
            if len(values) < 3:
                fail(f"catalogo insufficiente per {segment}: {len(values)}/3 clip")
            result[segment] = values[:3]
    return result


def generic_intro_assets() -> list[str]:
    with sqlite3.connect(DB_PATH) as db:
        rows = db.execute(
            """SELECT id FROM media_assets
               WHERE source='youtube' AND drive_link<>''
                 AND lifecycle_state='ACTIVE' AND index_state='INDEXED'
               ORDER BY id LIMIT 20""",
        ).fetchall()
    values = [str(row[0]) for row in rows]
    if len(values) < 3:
        fail("catalogo intro insufficiente: meno di 3 clip")
    return values[:3]


def intro_stock() -> dict[str, Any]:
    with sqlite3.connect(DB_PATH) as db:
        row = db.execute(
            """SELECT id, drive_link FROM media_assets
               WHERE source='stock' AND drive_link<>''
                 AND lifecycle_state='PUBLISHED' AND index_state='INDEXED'
               ORDER BY id LIMIT 1""",
        ).fetchone()
    if not row:
        fail("catalogo stock intro assente")
    return {"asset_id": row[0], "drive_link": row[1], "name": "Boxing introduction B-roll", "source": "stock", "fallback": False, "start_ms": 0, "end_ms": 5000}


def visual_plan(segment: str, count: int, assets: list[str], mode: str) -> dict[str, Any]:
    api_mode = "auto" if mode == "automatic" else mode
    clips = [
        {"asset_id": asset, "start_ms": 0, "duration_ms": 5000, "locked": mode != "automatic", "position": index}
        for index, asset in enumerate(assets[:count])
    ]
    if mode == "automatic":
        clips = []
    elif mode == "hybrid":
        clips = clips[:1]
    plan: dict[str, Any] = {"mode": api_mode, "max_clips": count, "target_duration_ms": max(0, count * 5000), "clips": clips}
    if mode in {"automatic", "hybrid"}:
        plan["candidate_asset_ids"] = assets[:3]
    return plan


def make_item(name: str, counts: dict[str, int], intro: int, post_intro: int, stock_set: set[str], *, mode: str = "manual", intro_stock_enabled: bool = False, translate_voiceover: bool = False, base: dict[str, Any], clips: dict[str, list[str]], intro_assets: list[str]) -> dict[str, Any]:
    item = copy.deepcopy(base["items"][0])
    item["id"] = f"boxers-matrix-{name}-{uuid.uuid4().hex[:8]}"
    item["force_refresh"] = True
    item["docs"] = {"enabled": False}
    item["media_plan"] = {
        "mode": "auto" if mode == "automatic" else mode,
        "post_segments": [],
        "force_refresh_bindings": True,
        "include_trace": True,
    }
    if intro or mode == "manual":
        item["media_plan"]["intro"] = visual_plan(INTRO_ID, intro, intro_assets, mode)
    if post_intro:
        item["media_plan"]["post_segments"].append({"segment_id": INTRO_ID, **visual_plan(INTRO_ID, post_intro, intro_assets, mode)})
    for segment, _subject in BOXERS:
        count = counts.get(segment, 0)
        if count or mode != "automatic":
            item["media_plan"]["post_segments"].append({"segment_id": segment, **visual_plan(segment, count, clips[segment], mode)})
    item["output"]["stock_bindings"] = [
        binding for binding in base["items"][0]["output"]["stock_bindings"] if binding["segment_id"] in stock_set
    ]
    if intro_stock_enabled:
        item["output"]["stock_bindings"].append({"index": 0, "segment_id": INTRO_ID, **intro_stock()})
    item["output"]["stock_enabled"] = "enabled" if item["output"]["stock_bindings"] else "disabled"
    if translate_voiceover:
        folder = os.environ.get("BOXERS_VOICEOVER_FOLDER_ID", "")
        if not folder:
            fail("BOXERS_VOICEOVER_FOLDER_ID obbligatorio per gli scenari VO")
        item["language"] = "en"
        item["output"].update({"languages": ["it"], "translate_to": "it", "voiceover_folder_id": folder})
    else:
        item["language"] = "en"
        item["output"].pop("languages", None)
        item["output"].pop("translate_to", None)
        item["output"].pop("voiceover_folder_id", None)
    return item


def scenarios() -> list[dict[str, Any]]:
    all_boxers = {segment: 0 for segment, _ in BOXERS}
    one = {segment: 1 for segment, _ in BOXERS}
    two = {segment: 2 for segment, _ in BOXERS}
    three = {segment: 3 for segment, _ in BOXERS}
    stock5 = set(BOXER_IDS)
    return [
        *[{"id": ident, "intro": i, "post_intro": p, "counts": all_boxers, "stock": set()} for ident, i, p in (("I00", 0, 0), ("I10", 1, 0), ("I01", 0, 1), ("I11", 1, 1), ("I22", 2, 2), ("I33", 3, 3))],
        {"id": "B0", "intro": 0, "post_intro": 0, "counts": all_boxers, "stock": stock5},
        {"id": "B1", "intro": 0, "post_intro": 0, "counts": one, "stock": stock5},
        {"id": "B2", "intro": 0, "post_intro": 0, "counts": two, "stock": stock5},
        {"id": "B3", "intro": 0, "post_intro": 0, "counts": three, "stock": stock5},
        {"id": "M1", "intro": 0, "post_intro": 0, "counts": dict(zip(BOXER_SEGMENTS, (0, 1, 2, 3, 1))), "stock": stock5},
        {"id": "M2", "intro": 0, "post_intro": 0, "counts": dict(zip(BOXER_SEGMENTS, (3, 2, 1, 0, 2))), "stock": stock5},
        {"id": "M3", "intro": 0, "post_intro": 0, "counts": dict(zip(BOXER_SEGMENTS, (0, 0, 3, 0, 0))), "stock": stock5},
        {"id": "M4", "intro": 0, "post_intro": 0, "counts": dict(zip(BOXER_SEGMENTS, (1, 1, 0, 1, 1))), "stock": stock5},
        {"id": "M5", "intro": 1, "post_intro": 1, "counts": dict(zip(BOXER_SEGMENTS, (1, 1, 1, 1, 2))), "stock": stock5},
        {"id": "S0", "intro": 0, "post_intro": 0, "counts": one, "stock": set()},
        {"id": "S5", "intro": 0, "post_intro": 0, "counts": all_boxers, "stock": stock5},
        {"id": "S6", "intro": 0, "post_intro": 0, "counts": all_boxers, "stock": stock5, "intro_stock": True},
        {"id": "S7", "intro": 2, "post_intro": 2, "counts": two, "stock": stock5, "intro_stock": True},
        {"id": "SP1", "intro": 0, "post_intro": 0, "counts": one, "stock": {"boxer-mike-tyson", "boxer-muhammad-ali", "boxer-floyd-mayweather"}},
        {"id": "SP2", "intro": 0, "post_intro": 0, "counts": one, "stock": {"boxer-muhammad-ali"}},
        {"id": "C1", "intro": 0, "post_intro": 0, "counts": all_boxers, "stock": stock5, "voiceover": True},
        {"id": "C2", "intro": 1, "post_intro": 1, "counts": one, "stock": set(), "voiceover": True},
        {"id": "C3", "intro": 0, "post_intro": 0, "counts": one, "stock": stock5},
        {"id": "C4", "intro": 1, "post_intro": 1, "counts": all_boxers, "stock": stock5},
        {"id": "C5", "intro": 1, "post_intro": 1, "counts": one, "stock": stock5, "voiceover": True},
        {"id": "C6", "intro": 2, "post_intro": 2, "counts": two, "stock": stock5},
        {"id": "C7", "intro": 3, "post_intro": 3, "counts": three, "stock": stock5, "voiceover": True},
        {"id": "C8", "intro": 2, "post_intro": 1, "counts": dict(zip(BOXER_SEGMENTS, (0, 1, 2, 3, 2))), "stock": stock5, "voiceover": True},
        {"id": "manual", "intro": 0, "post_intro": 0, "counts": one, "stock": stock5, "mode": "manual"},
        {"id": "automatic", "intro": 0, "post_intro": 0, "counts": one, "stock": stock5, "mode": "automatic"},
        {"id": "hybrid", "intro": 0, "post_intro": 0, "counts": three, "stock": stock5, "mode": "hybrid"},
    ]


def negative_cases(base: dict[str, Any], clips: dict[str, list[str]]) -> list[tuple[str, dict[str, Any], bool]]:
    """Return (name, payload, gate_only) negative cases.

    N1-N5 and N8 must be rejected by request validation. N6/N7 are valid
    structures whose canonical ownership gate must reject the resulting
    cross-subject binding. N9/N11 must be rejected because the intro-hook
    stock binding is no longer at index 0.
    """
    def item(name: str) -> dict[str, Any]:
        return make_item(name, {segment: 1 for segment, _ in BOXERS}, 0, 0, set(BOXER_IDS), base=base, clips=clips, intro_assets=generic_intro_assets())

    cases: list[tuple[str, dict[str, Any], bool]] = []
    n1 = item("N1")
    n1["media_plan"]["post_segments"][0]["clips"].append(copy.deepcopy(n1["media_plan"]["post_segments"][0]["clips"][0]))
    n1["media_plan"]["post_segments"][0]["max_clips"] = 2
    cases.append(("N1", n1, False))
    n2 = item("N2")
    n2["media_plan"]["post_segments"][0]["clips"].append({"asset_id": clips[BOXER_SEGMENTS[0]][1], "position": 0, "duration_ms": 5000, "locked": True})
    n2["media_plan"]["post_segments"][0]["max_clips"] = 2
    cases.append(("N2", n2, False))
    n3 = item("N3")
    n3["media_plan"]["post_segments"][0]["clips"][0]["position"] = 2
    cases.append(("N3", n3, False))
    n4 = item("N4")
    n4["media_plan"]["post_segments"].append(copy.deepcopy(n4["media_plan"]["post_segments"][0]))
    cases.append(("N4", n4, False))
    n5 = item("N5")
    n5["media_plan"]["post_segments"][0]["clips"][0]["asset_id"] = "asset-does-not-exist"
    cases.append(("N5", n5, False))
    n6 = item("N6")
    n6["media_plan"]["post_segments"][0]["clips"][0]["asset_id"] = clips[BOXER_SEGMENTS[1]][0]
    cases.append(("N6", n6, True))
    n7 = item("N7")
    n7["output"]["stock_bindings"][0]["folder_id"], n7["output"]["stock_bindings"][1]["folder_id"] = n7["output"]["stock_bindings"][1]["folder_id"], n7["output"]["stock_bindings"][0]["folder_id"]
    cases.append(("N7", n7, True))
    n8 = item("N8")
    n8["output"]["stock_bindings"][0]["folder_id"] = ""
    n8["output"]["stock_bindings"][0]["fallback"] = False
    cases.append(("N8", n8, False))
    n9 = item("N9")
    n9["output"]["stock_bindings"].append({"index": 0, "segment_id": INTRO_ID, **intro_stock()})
    n9["output"]["stock_bindings"][0]["index"] = 1
    cases.append(("N9", n9, False))
    n11 = item("N11")
    n11["output"]["stock_bindings"].append({"index": 5, "segment_id": INTRO_ID, **intro_stock()})
    cases.append(("N11", n11, False))
    return cases


def assert_contract(body: dict[str, Any], scenario: dict[str, Any], base: dict[str, Any], *, translated: bool) -> None:
    out = output(body)
    spec = out.get("specscene", {})
    scenes = spec.get("scenes", [])
    if len(scenes) != 6:
        fail(f"{scenario['id']}: scenes={len(scenes)}, expected 6")
    by_segment = {scene.get("segment_id"): scene for scene in scenes}
    if set(by_segment) != {INTRO_ID, *BOXER_IDS}:
        fail(f"{scenario['id']}: segment_id non canonici: {sorted(by_segment)}")
    expected_stock = set(scenario.get("stock", set())) | ({INTRO_ID} if scenario.get("intro_stock") else set())
    actual_stock = {segment for segment, scene in by_segment.items() if scene.get("bindings", {}).get("stock")}
    if actual_stock != expected_stock:
        fail(f"{scenario['id']}: stock {sorted(actual_stock)} != {sorted(expected_stock)}")
    assignments = spec.get("visual_assignments", [])
    expected_assignments = scenario["intro"] + scenario["post_intro"] + sum(scenario["counts"].values())
    if len(assignments) != expected_assignments:
        fail(f"{scenario['id']}: assignments={len(assignments)}, expected {expected_assignments}")
    for segment, count in [(INTRO_ID, scenario["post_intro"]), *scenario["counts"].items()]:
        selected = [a for a in assignments if a.get("slot") == "post_segment" and a.get("segment_id") == segment]
        if len(selected) != count or sorted(a.get("position") for a in selected) != list(range(count)):
            fail(f"{scenario['id']}: {segment} clip positions/count invalid")
        if len({a.get("asset_id") for a in selected}) != count:
            fail(f"{scenario['id']}: {segment} duplicate clip asset")
    intro_assignments = [a for a in assignments if a.get("slot") == "intro"]
    if len(intro_assignments) != scenario["intro"] or sorted(a.get("position") for a in intro_assignments) != list(range(scenario["intro"])):
        fail(f"{scenario['id']}: intro positions/count invalid")
    # intro-hook is a normal narrative segment. When its stock binding is
    # present the scene must keep the intro kind and carry a usable text.
    intro_scene = by_segment.get(INTRO_ID)
    if intro_scene is None:
        fail(f"{scenario['id']}: scena intro-hook assente")
    if scenario.get("intro_stock"):
        intro_stock = intro_scene.get("bindings", {}).get("stock")
        if not intro_stock:
            fail(f"{scenario['id']}: intro-hook stock assente")
        if intro_scene.get("kind") != "intro":
            fail(f"{scenario['id']}: intro-hook kind={intro_scene.get('kind')}, want intro")
    elif intro_scene.get("bindings", {}).get("stock"):
        fail(f"{scenario['id']}: intro-hook stock inatteso senza intro_stock")
    # A scene with a stock binding AND a position-0 post-segment clip must
    # expose both bindings.stock and bindings.clip (nothing is overwritten).
    for segment, scene in by_segment.items():
        if not scene.get("bindings", {}).get("stock"):
            continue
        has_post = any(a.get("slot") == "post_segment" and a.get("segment_id") == segment and a.get("position") == 0 for a in assignments)
        if has_post and not scene.get("bindings", {}).get("clip"):
            fail(f"{scenario['id']}: {segment} non espone stock+clip conviventi")
    # Adding the intro-hook stock binding must never shift the boxer
    # bindings: every boxer scene keeps its canonical asset_id.
    base_stock = {binding["segment_id"]: binding.get("asset_id") for binding in base["items"][0]["output"]["stock_bindings"]}
    for segment, scene in by_segment.items():
        if segment in BOXER_IDS and segment in base_stock:
            expected = base_stock[segment]
            got = scene.get("bindings", {}).get("stock", {}).get("asset_id")
            if expected and got != expected:
                fail(f"{scenario['id']}: stock di {segment} cambiato ({got} != {expected})")
    names = {segment: subject for segment, subject in BOXERS}
    for segment, scene in by_segment.items():
        text = str(scene.get("text", "")).casefold()
        if len(text.split()) < 100:
            fail(f"{scenario['id']}: testo troppo breve in {segment}")
        if segment != INTRO_ID:
            if names[segment] not in text:
                fail(f"{scenario['id']}: soggetto assente in {segment}")
            for other_segment, other_name in names.items():
                if other_segment != segment and other_name in text:
                    fail(f"{scenario['id']}: leakage {other_name} -> {segment}")
        elif any(name in text for name in names.values()):
            fail(f"{scenario['id']}: intro nomina un pugile")
        if translated and scene.get("bindings", {}).get("voiceover", {}).get("status") != "completed":
            fail(f"{scenario['id']}: voiceover incompleto in {segment}")


def run_matrix(args: argparse.Namespace) -> None:
    base = load_base()
    clips = catalog()
    intro_assets = generic_intro_assets()
    selected = scenarios()
    if args.scenario:
        selected = [item for item in selected if item["id"] in set(args.scenario)]
    if not selected:
        fail("nessuno scenario selezionato")
    print(f"MATRIX scenarios={len(selected)} render=DISABLED")
    for scenario in selected:
        item = make_item(scenario["id"], scenario["counts"], scenario["intro"], scenario["post_intro"], scenario.get("stock", set()), mode=scenario.get("mode", "manual"), intro_stock_enabled=bool(scenario.get("intro_stock")), translate_voiceover=bool(scenario.get("voiceover")), base=base, clips=clips, intro_assets=intro_assets)
        body = submit(item)
        assert_contract(body, scenario, base, translated=bool(scenario.get("voiceover")))
        if args.save_out:
            savedir = Path(args.save_out)
            savedir.mkdir(parents=True, exist_ok=True)
            (savedir / f"{scenario['id']}.json").write_text(json.dumps(output(body), ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"PASS {scenario['id']} scenes=6 assignments={scenario['intro'] + scenario['post_intro'] + sum(scenario['counts'].values())} stock={len(scenario.get('stock', set())) + int(bool(scenario.get('intro_stock')))}")


def assert_ownership(body: dict[str, Any], case: str, clips: dict[str, list[str]], base: dict[str, Any]) -> None:
    out = output(body)
    assignments = out.get("specscene", {}).get("visual_assignments", [])
    owners = {asset: segment for segment, values in clips.items() for asset in values}
    for assignment in assignments:
        segment = assignment.get("segment_id")
        asset = assignment.get("asset_id")
        if segment in BOXER_IDS and asset in owners and owners[asset] != segment:
            fail(f"{case}: clip {asset} di {owners[asset]} assegnata a {segment}")
    expected_folders = {binding["segment_id"]: binding.get("folder_id", "") for binding in base["items"][0]["output"]["stock_bindings"]}
    for scene in out.get("specscene", {}).get("scenes", []):
        binding = scene.get("bindings", {}).get("stock")
        segment = scene.get("segment_id")
        if binding and segment in expected_folders and binding.get("folder_id") != expected_folders[segment]:
            fail(f"{case}: stock folder scambiata in {segment}")


def run_negative(args: argparse.Namespace) -> None:
    base = load_base()
    clips = catalog()
    selected = negative_cases(base, clips)
    if args.scenario:
        selected = [case for case in selected if case[0] in set(args.scenario)]
    for name, item, gate_only in selected:
        try:
            body = submit(item)
        except RuntimeError:
            if gate_only:
                print(f"PASS {name} rejected-before-gate")
                continue
            print(f"PASS {name} rejected")
            continue
        if not gate_only:
            fail(f"{name}: payload negativo accettato")
        try:
            assert_ownership(body, name, clips, base)
        except RuntimeError:
            print(f"PASS {name} canonical-gate")
            continue
        fail(f"{name}: ownership gate non ha rilevato lo scambio")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--scenario", action="append", help="run only this scenario (repeatable)")
    parser.add_argument("--dry-run", action="store_true", help="print the matrix without calling the API")
    parser.add_argument("--negative-only", action="store_true", help="run only the negative cases")
    parser.add_argument("--save-out", metavar="DIR", help="write each scenario's output JSON to DIR")
    args = parser.parse_args()
    if args.dry_run:
        for scenario in scenarios():
            total = scenario["intro"] + scenario["post_intro"] + sum(scenario["counts"].values())
            print(f"{scenario['id']} assignments={total} stock={len(scenario.get('stock', set())) + int(bool(scenario.get('intro_stock')))} voiceover={bool(scenario.get('voiceover'))}")
        negative = negative_cases(load_base(), catalog())
        for name, _item, _gate_only in negative:
            print(f"{name} negative=true")
        print(f"MATRIX_COUNT={len(scenarios()) + len(negative)} RENDER=DISABLED")
        return 0
    try:
        if args.negative_only:
            run_negative(args)
        else:
            run_matrix(args)
    except RuntimeError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
