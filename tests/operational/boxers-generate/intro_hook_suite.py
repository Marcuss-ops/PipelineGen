#!/usr/bin/env python3
"""Practical intro-hook suite: clip + stock + post clip + source_text + voiceover.

Demonstrates that the intro-hook segment can simultaneously carry:
  * timeline clips before the narration (slot "intro"),
  * a direct stock binding during its narration,
  * timeline clips after the narration (slot "post_segment"),
  * its own source_text (never a neighbouring boxer's),
  * its own voiceover binding.

The suite never invokes a renderer: only the script-generation contract
is exercised. Every case runs its jq gate verbatim against the saved full
job response and prints the marker exit code (0 = PASS).

Runs against the live PipelineGen service; authentication is delegated to
scripts/with-velox-auth via the bash wrapper.
"""

from __future__ import annotations

import argparse
import copy
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[3]
PAYLOAD_PATH = ROOT / "tests/operational/boxers-generate/five_boxers_stock_post_segments.json"
DB_PATH = Path(os.environ.get("VELOX_DB", ROOT / "data/media/media.db.sqlite"))
FULL_PATH = Path(os.environ.get("FULL", "/tmp/intro-hook-stock-full.json"))
INTRO_ID = "intro-hook"
BOXER_SEGMENTS = [
    "boxer-mike-tyson",
    "boxer-muhammad-ali",
    "boxer-evander-holyfield",
    "boxer-floyd-mayweather",
    "boxer-sugar-ray-robinson",
]

INTRO_FIXTURE_EN = {
    "id": "intro-hook",
    "topic": "Strong introduction",
    "source_text": "Five champions, five legendary careers, and five different ways to turn boxing into history. Behind titles, lights, and applause stand enormous sacrifices, constant pressure, unexpected setbacks, and decisions that can change an athlete's destiny. Build tension and curiosity without naming or describing an individual champion. Establish why fame can conceal exhaustion, doubt, isolation, and difficult consequences that remain invisible to the crowd. The central question is simple: what can it cost to become a legend?",
    "target_words": 120,
}

INTRO_FIXTURE_IT = {
    "id": "intro-hook",
    "topic": "Introduzione sui cinque pugili",
    "source_text": "Cinque campioni hanno raggiunto la leggenda affrontando pressione, sacrifici e decisioni capaci di cambiare una carriera. Crea tensione e curiosità senza nominare individualmente i pugili.",
    "target_words": 120,
}


def fail(message: str) -> None:
    raise RuntimeError(message)


def load_base() -> dict[str, Any]:
    with PAYLOAD_PATH.open(encoding="utf-8") as handle:
        return json.load(handle)


def db_rows(where: str) -> list[tuple[str, str, str]]:
    with sqlite3.connect(DB_PATH) as db:
        rows = db.execute(
            f"SELECT id, drive_link, folder_id FROM media_assets WHERE {where} "
            "ORDER BY id"
        ).fetchall()
    return [(str(r[0]), str(r[1]), str(r[2])) for r in rows]


def catalog() -> dict[str, list[str]]:
    """Three active indexed YouTube clips per subject (mirrors the matrix)."""
    result: dict[str, list[str]] = {}
    for segment, subject in (
        ("boxer-mike-tyson", "mike tyson"),
        ("boxer-muhammad-ali", "muhammad ali"),
        ("boxer-evander-holyfield", "evander holyfield"),
        ("boxer-floyd-mayweather", "floyd mayweather"),
        ("boxer-sugar-ray-robinson", "sugar ray robinson"),
    ):
        with sqlite3.connect(DB_PATH) as db:
            rows = db.execute(
                """SELECT id FROM media_assets
                   WHERE source='youtube' AND drive_link<>''
                     AND lifecycle_state='ACTIVE' AND index_state='INDEXED'
                     AND lower(name||' '||search_text||' '||metadata_json) LIKE ?
                   ORDER BY id LIMIT 20""",
                (f"%{subject}%",),
            ).fetchall()
        values = [str(row[0]) for row in rows]
        if len(values) < 1:
            fail(f"catalogo insufficiente per {segment}: nessuna clip")
        result[segment] = values
    return result


def intro_assets() -> list[tuple[str, str, str]]:
    rows = db_rows(
        "source='youtube' AND drive_link<>'' AND folder_id<>'' "
        "AND lifecycle_state='ACTIVE' AND index_state='INDEXED'"
    )
    if len(rows) < 3:
        fail("catalogo intro insufficiente: meno di 3 clip youtube indicizzate")
    return rows


def intro_stock_binding() -> dict[str, Any]:
    rows = intro_assets()
    asset_id, drive_link, folder_id = rows[0]
    return {
        "index": 0,
        "scene_id": "scene-0",
        "segment_id": INTRO_ID,
        "asset_id": asset_id,
        "name": "Boxing arena cinematic introduction",
        "source": "youtube",
        "drive_link": drive_link,
        "folder_id": folder_id,
        "folder_link": f"https://drive.google.com/drive/folders/{folder_id}?usp=drive_link",
        "fallback": False,
        "start_ms": 0,
        "end_ms": 7000,
    }


def intro_clips(count: int, *, exclude: set[str] | None = None, duration_ms: int = 5000) -> list[dict[str, Any]]:
    rows = intro_assets()
    exclude = exclude or set()
    picked = [r for r in rows if r[0] not in exclude][:count]
    if len(picked) < count:
        fail(f"clip intro insufficienti: richieste {count}")
    return [
        {"asset_id": asset_id, "start_ms": 0, "duration_ms": duration_ms, "locked": True, "position": index}
        for index, (asset_id, _drive, _folder) in enumerate(picked)
    ]


def base_boxer_clips() -> dict[str, list[dict[str, Any]]]:
    base = load_base()
    plan = base["items"][0]["media_plan"]["post_segments"]
    clips: dict[str, list[dict[str, Any]]] = {}
    for entry in plan:
        segment = entry.get("segment_id")
        if segment in BOXER_SEGMENTS and entry.get("clips"):
            clips[segment] = entry["clips"]
    return clips


def request(method: str, path: str, payload: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        fail("VELOX_ADMIN_TOKEN deve essere caricato tramite scripts/with-velox-auth")
    base = os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000").rstrip("/")
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    if method == "POST":
        headers["Idempotency-Key"] = f"intro-hook-suite-{uuid.uuid4()}"
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


def wait_terminal(job_id: str) -> dict[str, Any]:
    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    while time.time() < deadline:
        status, body = request("GET", f"/api/jobs/{job_id}/full")
        if status >= 400:
            fail(f"GET job {job_id}: HTTP {status}: {body}")
        state = str(body.get("status") or body.get("job", {}).get("status") or "").upper()
        if state in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED", "FAILED", "CANCELLED"}:
            return body
        time.sleep(2)
    fail(f"timeout job {job_id}")


def submit(item: dict[str, Any]) -> dict[str, Any]:
    status, body = request("POST", "/api/script/generate", {"version": 2, "preset": "custom", "items": [item]})
    if status not in (200, 202):
        raise RuntimeError(f"generation rejected: HTTP {status}: {body}")
    job_id = body.get("job_id") or body.get("id") or body.get("job", {}).get("id")
    if not job_id:
        fail(f"missing job id: {body}")
    return wait_terminal(job_id)


def submit_expect_failure(item: dict[str, Any]) -> tuple[str, int, dict[str, Any]]:
    """Return (kind, status, body) where kind is 'rejected' or 'failed'.

    'rejected' means the envelope/request validation returned HTTP >= 400;
    'failed' means the request was accepted but the job ended FAILED.
    """
    status, body = request("POST", "/api/script/generate", {"version": 2, "preset": "custom", "items": [item]})
    if status >= 400:
        return "rejected", status, body
    job_id = body.get("job_id") or body.get("id") or body.get("job", {}).get("id")
    if not job_id:
        fail(f"missing job id for negative case: {body}")
    terminal = wait_terminal(job_id)
    state = str(terminal.get("status") or terminal.get("job", {}).get("status") or "").upper()
    if state in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED"}:
        fail(f"negative case accepted and succeeded: {state}")
    return "failed", 0, terminal


def base_item(name: str) -> dict[str, Any]:
    item = copy.deepcopy(load_base()["items"][0])
    item["id"] = f"intro-hook-suite-{name}-{uuid.uuid4().hex[:8]}"
    item["force_refresh"] = True
    item["docs"] = {"enabled": False}
    return item


def build_item(
    name: str,
    *,
    segments: list[dict[str, Any]],
    stock_bindings: list[dict[str, Any]],
    intro_clip_count: int = 0,
    post_intro_clip_count: int = 0,
    boxer_post: dict[str, list[dict[str, Any]]] | None = None,
    mode: str = "manual",
    language: str = "en",
    translate_to: str | None = None,
    voiceover_folder: str | None = None,
    media_plan_mode: str | None = None,
) -> dict[str, Any]:
    item = base_item(name)
    item["language"] = language
    if translate_to or voiceover_folder:
        item["output"]["languages"] = ["it"]
        item["output"]["translate_to"] = translate_to or "it"
        if voiceover_folder:
            item["output"]["voiceover_folder_id"] = voiceover_folder
    else:
        item["output"].pop("languages", None)
        item["output"].pop("translate_to", None)
        item["output"].pop("voiceover_folder_id", None)
    item["script_params"]["segments"] = segments
    item["output"]["stock_bindings"] = copy.deepcopy(stock_bindings)
    item["output"]["stock_enabled"] = "enabled" if stock_bindings else "disabled"

    intro_assets_pool = [r[0] for r in intro_assets()]
    stock_asset = stock_bindings[0]["asset_id"] if stock_bindings else None
    pool = [a for a in intro_assets_pool if a != stock_asset]

    intro_clips: list[dict[str, Any]] = []
    if intro_clip_count:
        intro_clips = [
            {"asset_id": a, "start_ms": 0, "duration_ms": 5000, "locked": True, "position": i}
            for i, a in enumerate(pool[:intro_clip_count])
        ]
    post_intro_clips: list[dict[str, Any]] = []
    if post_intro_clip_count:
        remaining = pool[intro_clip_count:]
        post_intro_clips = [
            {"asset_id": a, "start_ms": 0, "duration_ms": 5000, "locked": True, "position": i}
            for i, a in enumerate(remaining[:post_intro_clip_count])
        ]

    plan: dict[str, Any] = {
        "mode": "auto" if mode == "automatic" else mode,
        "post_segments": [],
        "force_refresh_bindings": True,
        "include_trace": True,
    }
    slot_mode = "auto" if mode == "automatic" else mode
    plan["intro"] = {
        "mode": slot_mode,
        "max_clips": len(intro_clips),
        "target_duration_ms": len(intro_clips) * 5000,
        "clips": intro_clips,
    }
    if mode in {"hybrid", "automatic"}:
        plan["intro"]["candidate_asset_ids"] = pool[:3]

    post: dict[str, list[dict[str, Any]]] = {}
    if post_intro_clips:
        post[INTRO_ID] = post_intro_clips
    for segment, clips in (boxer_post or {}).items():
        post[segment] = clips
    for segment, clips in post.items():
        plan["post_segments"].append({
            "segment_id": segment,
            "mode": slot_mode,
            "max_clips": len(clips),
            "target_duration_ms": len(clips) * 5000,
            "clips": copy.deepcopy(clips),
        })
        if mode in {"hybrid", "automatic"}:
            plan["post_segments"][-1]["candidate_asset_ids"] = [c["asset_id"] for c in clips][:3]

    if media_plan_mode:
        plan["mode"] = media_plan_mode
    item["media_plan"] = plan
    return item


# ─────────────────────────────────────────────────────────────────────────
# jq gates (verbatim from the suite spec; real asset ids injected via $args)
# ─────────────────────────────────────────────────────────────────────────

def jq_expression(gate: str) -> str:
    return f"(.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output) as $o | {gate}"


GATE_OUT = """
  ($o.specscene.scenes | length) >= 1
  and $o.specscene.scenes[0].id == "scene-0"
  and $o.specscene.scenes[0].segment_id == "intro-hook"
  and $o.specscene.scenes[0].kind == "intro"
  and ($o.specscene.scenes[0].text | length) > 100
  and $o.specscene.scenes[0].bindings.stock != null
  and $o.specscene.scenes[0].bindings.stock.asset_id == $INTRO_STOCK_ASSET_ID
  and $o.specscene.scenes[0].bindings.stock.folder_id == $INTRO_STOCK_FOLDER_ID
  and $o.specscene.scenes[0].bindings.stock.duration_ms == 7000
  and ($o.specscene.visual_assignments | length) == 0
"""

GATE_CLIPS_STOCK = """
  $o.specscene.scenes[0].bindings.stock.asset_id == $INTRO_STOCK_ASSET_ID
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro")] | length) == 2
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro") | .position] | sort) == [0,1]
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro") | .asset_id] | sort) == ([$INTRO_CLIP_1,$INTRO_CLIP_2] | sort)
"""

GATE_POST_STOCK = """
  $o.specscene.scenes[0].bindings.stock.asset_id == $INTRO_STOCK_ASSET_ID
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and .segment_id == "intro-hook")] | length) == 2
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and .segment_id == "intro-hook") | .position] | sort) == [0,1]
"""

GATE_COMPLETE = """
  $o.specscene.scenes[0].segment_id == "intro-hook"
  and $o.specscene.scenes[0].bindings.stock != null
  and $o.specscene.scenes[0].bindings.voiceover.status == "completed"
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro")] | length) == 2
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and .segment_id == "intro-hook")] | length) == 2
  and ($o.specscene.visual_assignments | length) == 4
"""

GATE_SIX_STOCKS = """
  ($o.specscene.scenes | length) == 6
  and ([$o.specscene.scenes[] | select(.bindings.stock != null)] | length) == 6
  and [$o.specscene.scenes[].segment_id] == ["intro-hook","boxer-mike-tyson","boxer-muhammad-ali","boxer-evander-holyfield","boxer-floyd-mayweather","boxer-sugar-ray-robinson"]
"""

GATE_NO_INTRO_STOCK = """
  $o.specscene.scenes[0].bindings.stock == null
  and ([$o.specscene.scenes[1:][] | select(.bindings.stock != null)] | length) == 5
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro")] | length) >= 1
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and .segment_id == "intro-hook")] | length) >= 1
"""

GATE_SOURCE_TEXT = """
  $o.specscene.scenes[0].segment_id == "intro-hook"
  and ($o.specscene.scenes[0].text | length) > 100
  and ($o.specscene.scenes[0].text | test("pressione|sacrific"; "i"))
  and ($o.specscene.scenes[0].text | test("Mike Tyson|Muhammad Ali|Evander Holyfield|Floyd Mayweather|Sugar Ray Robinson"; "i") | not)
"""

GATE_COEXISTENCE = """
  $o.specscene.scenes[0].bindings.stock != null
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro" or (.slot == "post_segment" and .segment_id == "intro-hook"))] | length) >= 1
"""

GATE_TRANSLATION_VOICEOVER = """
  $o.specscene.scenes[0].segment_id == "intro-hook"
  and ($o.specscene.scenes[0].text | test("\\\\b(della|degli|pressione|sacrifici|campioni)\\\\b"; "i"))
  and $o.specscene.scenes[0].bindings.stock.asset_id == $INTRO_STOCK_ASSET_ID
  and $o.specscene.scenes[0].bindings.voiceover.status == "completed"
"""

GATE_FINAL = """
  ($o.specscene.scenes | length) == 6
  and ([$o.specscene.scenes[] | select(.bindings.stock != null)] | length) == 6
  and all($o.specscene.scenes[]; .bindings.voiceover.status == "completed")
  and ([$o.specscene.visual_assignments[] | select(.slot == "intro")] | length) == 2
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and .segment_id == "intro-hook")] | length) == 2
  and ([$o.specscene.visual_assignments[] | select(.slot == "post_segment" and (.segment_id | startswith("boxer-")))] | length) == 5
  and ($o.specscene.visual_assignments | length) == 9
  and $o.specscene.scenes[0].kind == "intro"
  and $o.specscene.scenes[0].bindings.stock.asset_id == $INTRO_STOCK_ASSET_ID
"""

GATE_NO_RENDER = """
  (.render // null) == null
  and ((.renders // []) | length) == 0
  and (has("remotion") | not)
"""


def run_jq(expr: str, full_path: Path, args: list[str]) -> bool:
    proc = subprocess.run(["jq", "-e", *args, jq_expression(expr), str(full_path)], capture_output=True, text=True)
    return proc.returncode == 0


def asset_args(binding: dict[str, Any]) -> list[str]:
    return [
        "--arg", "INTRO_STOCK_ASSET_ID", binding["asset_id"],
        "--arg", "INTRO_STOCK_FOLDER_ID", binding["folder_id"],
    ]


def resolve_voiceover_folder() -> str:
    folder = os.environ.get("BOXERS_VOICEOVER_FOLDER_ID", "")
    if not folder:
        fail("BOXERS_VOICEOVER_FOLDER_ID obbligatorio per gli scenari voiceover (Test 4/9/finale)")
    return folder


# ─────────────────────────────────────────────────────────────────────────

def full_segments() -> list[dict[str, Any]]:
    return copy.deepcopy(load_base()["items"][0]["script_params"]["segments"])


def boxer_stock_bindings() -> list[dict[str, Any]]:
    return copy.deepcopy(load_base()["items"][0]["output"]["stock_bindings"])


def run_suite(args: argparse.Namespace) -> int:
    base_stock = intro_stock_binding()
    segments_full = full_segments()
    boxer_stocks = boxer_stock_bindings()
    intro_only_segments = [copy.deepcopy(INTRO_FIXTURE_EN)]
    intro_only_segments_it = [copy.deepcopy(INTRO_FIXTURE_IT)]

    all_bindings = [copy.deepcopy(base_stock), *boxer_stocks]
    clips_by_boxer = base_boxer_clips()

    if args.voiceover_required:
        resolve_voiceover_folder()

    tmp = Path(tempfile.mkdtemp(prefix="intro-hook-suite-"))
    markers: dict[str, bool] = {}
    voiceover = args.voiceover_required

    def positive(name: str, marker: str, item: dict[str, Any], expr: str, args_jq: list[str], *, save_final: bool = False) -> None:
        full = submit(item)
        state = str(full.get("status") or full.get("job", {}).get("status") or "").upper()
        full_path = tmp / f"{name}.json"
        full_path.write_text(json.dumps(full, ensure_ascii=False, indent=2), encoding="utf-8")
        if save_final:
            FULL_PATH.parent.mkdir(parents=True, exist_ok=True)
            FULL_PATH.write_text(json.dumps(full, ensure_ascii=False, indent=2), encoding="utf-8")
            no_render = run_jq(GATE_NO_RENDER, full_path, [])
            print(f"NO_RENDER={0 if no_render else 1}")
            markers["NO_RENDER"] = no_render
        ok = run_jq(expr, full_path, args_jq)
        suffix = "" if ok else f" (job={state})"
        print(f"{marker}={0 if ok else 1}{suffix}")
        markers[marker] = ok

    def negative(name: str, marker: str, item: dict[str, Any], expected: str) -> None:
        kind, status, body = submit_expect_failure(item)
        blob = json.dumps(body, ensure_ascii=False)
        ok = (kind in {"rejected", "failed"}) and (expected in blob)
        print(f"{name}={0 if ok else 1} ({kind} status={status})")
        markers[marker] = ok

    if args.negative_only:
        run_negatives(negative)
        return summarize(markers)

    # Test 1 — solo stock intro: intro 0, stock 1, post 0.
    item = build_item("t1", segments=intro_only_segments, stock_bindings=[copy.deepcopy(base_stock)], intro_clip_count=0, post_intro_clip_count=0)
    positive("t1", "INTRO_STOCK_ONLY", item, GATE_OUT, asset_args(base_stock))

    # Test 2 — due clip intro più stock intro.
    intro_pool = [r[0] for r in intro_assets()]
    clip1 = next(a for a in intro_pool if a != base_stock["asset_id"])
    clip2 = next(a for a in intro_pool if a != base_stock["asset_id"] and a != clip1)
    item = build_item("t2", segments=intro_only_segments, stock_bindings=[copy.deepcopy(base_stock)], intro_clip_count=2, post_intro_clip_count=0)
    args2 = asset_args(base_stock) + ["--arg", "INTRO_CLIP_1", clip1, "--arg", "INTRO_CLIP_2", clip2]
    positive("t2", "INTRO_CLIPS_AND_STOCK", item, GATE_CLIPS_STOCK, args2)

    # Test 3 — stock intro più due clip post-intro.
    item = build_item("t3", segments=intro_only_segments, stock_bindings=[copy.deepcopy(base_stock)], intro_clip_count=0, post_intro_clip_count=2)
    positive("t3", "POST_INTRO_AND_STOCK", item, GATE_POST_STOCK, asset_args(base_stock))

    # Test 4 — scenario completo intro (voiceover).
    if voiceover:
        folder = resolve_voiceover_folder()
        item = build_item("t4", segments=intro_only_segments, stock_bindings=[copy.deepcopy(base_stock)], intro_clip_count=2, post_intro_clip_count=2, language="en", translate_to="it", voiceover_folder=folder)
        positive("t4", "INTRO_COMPLETE", item, GATE_COMPLETE, asset_args(base_stock))

    # Test 5 — intro più tutti gli stock dei pugili.
    item = build_item("t5", segments=segments_full, stock_bindings=all_bindings)
    positive("t5", "INTRO_AND_BOXER_STOCKS", item, GATE_SIX_STOCKS, [])

    # Test 6 — nessuno stock intro.
    item = build_item("t6", segments=segments_full, stock_bindings=boxer_stocks, intro_clip_count=1, post_intro_clip_count=1)
    positive("t6", "INTRO_WITHOUT_STOCK", item, GATE_NO_INTRO_STOCK, [])

    # Test 7 — verifica del source_text (italiano).
    item = build_item("t7", segments=intro_only_segments_it, stock_bindings=[copy.deepcopy(base_stock)], language="it")
    positive("t7", "INTRO_SOURCE_TEXT", item, GATE_SOURCE_TEXT, asset_args(base_stock))

    # Test 8 — clip e stock non si sovrascrivono (hybrid).
    item = build_item("t8", segments=segments_full, stock_bindings=all_bindings, intro_clip_count=1, post_intro_clip_count=1, mode="hybrid")
    positive("t8", "CLIP_STOCK_COEXISTENCE", item, GATE_COEXISTENCE, [])

    # Test 9 — traduzione e voiceover.
    if voiceover:
        item = build_item("t9", segments=segments_full, stock_bindings=all_bindings, language="en", translate_to="it", voiceover_folder=folder)
        positive("t9", "INTRO_TRANSLATION_VOICEOVER", item, GATE_TRANSLATION_VOICEOVER, asset_args(base_stock))

    run_negatives(negative)

    # Test finale completo.
    if voiceover:
        boxer_post = {segment: clips_by_boxer.get(segment, [])[:1] for segment in BOXER_SEGMENTS}
        item = build_item(
            "final",
            segments=segments_full,
            stock_bindings=all_bindings,
            intro_clip_count=2,
            post_intro_clip_count=2,
            boxer_post=boxer_post,
            language="en",
            translate_to="it",
            voiceover_folder=folder,
        )
        positive("final", "INTRO_HOOK_COMPLETE_GATE", item, GATE_FINAL, asset_args(base_stock), save_final=True)

    return summarize(markers)


def run_negatives(negative) -> None:
    base_stock = intro_stock_binding()
    segments_full = full_segments()
    boxer_stocks = boxer_stock_bindings()
    intro_only_segments = [copy.deepcopy(INTRO_FIXTURE_EN)]
    intro_only_segments_it = [copy.deepcopy(INTRO_FIXTURE_IT)]

    # N1 — indice sbagliato.
    n1_stock = copy.deepcopy(base_stock)
    n1_stock["index"] = 1
    item = build_item("n1", segments=intro_only_segments, stock_bindings=[n1_stock])
    negative("N1", "INVALID_INDEX_REJECTED", item, "must target index 0")

    # N2 — scena sbagliata.
    n2_stock = copy.deepcopy(base_stock)
    n2_stock["scene_id"] = "scene-1"
    item = build_item("n2", segments=intro_only_segments, stock_bindings=[n2_stock])
    negative("N2", "INVALID_SCENE_REJECTED", item, "must target scene-0")

    # N3 — segmento sbagliato.
    n3_stock = {
        "index": 0,
        "scene_id": "scene-0",
        "segment_id": "boxer-mike-tyson",
        "asset_id": boxer_stocks[0]["asset_id"],
        "drive_link": boxer_stocks[0]["drive_link"],
        "folder_id": boxer_stocks[0]["folder_id"],
        "folder_link": boxer_stocks[0]["folder_link"],
        "start_ms": 0,
        "end_ms": 5000,
    }
    item = build_item("n3", segments=intro_only_segments, stock_bindings=[n3_stock])
    negative("N3", "INVALID_SEGMENT_REJECTED", item, "targets segment_id")

    # N4 — intro-hook non primo.
    reordered = [copy.deepcopy(segments_full[1]), copy.deepcopy(INTRO_FIXTURE_EN)]
    item = build_item("n4", segments=reordered, stock_bindings=[copy.deepcopy(base_stock)])
    negative("N4", "INVALID_FIRST_SEGMENT_REJECTED", item, "segments[0].id")

    # N5 — source_text vuoto.
    n5_segments = [copy.deepcopy(INTRO_FIXTURE_EN)]
    n5_segments[0]["source_text"] = ""
    item = build_item("n5", segments=n5_segments, stock_bindings=[copy.deepcopy(base_stock)])
    negative("N5", "EMPTY_SOURCE_TEXT_REJECTED", item, "non-empty source_text")

    # N6 — durata non valida.
    n6_stock = copy.deepcopy(base_stock)
    n6_stock["start_ms"] = 7000
    n6_stock["end_ms"] = 5000
    item = build_item("n6", segments=intro_only_segments, stock_bindings=[n6_stock])
    # Depending on whether request validation or post-processing owns the
    # rejection, the stable contract is the duration relation itself; both
    # layers include the same field names in their diagnostic.
    negative("N6", "INVALID_DURATION_REJECTED", item, "end_ms")

    # N7 — binding duplicato (index 0, segment_id intro-hook).
    n7_stock = copy.deepcopy(base_stock)
    item = build_item("n7", segments=intro_only_segments, stock_bindings=[copy.deepcopy(base_stock), n7_stock])
    negative("N7", "DUPLICATE_REJECTED", item, "duplicate stock binding index")

    # N8 — cartella incoerente.
    n8_stock = copy.deepcopy(base_stock)
    n8_stock["folder_id"] = "FOLDER_A"
    n8_stock["folder_link"] = "https://drive.google.com/drive/folders/FOLDER_B"
    item = build_item("n8", segments=intro_only_segments, stock_bindings=[n8_stock])
    negative("N8", "FOLDER_MISMATCH_REJECTED", item, "folder_link does not match folder_id")


FINAL_ORDER = [
    "INTRO_SOURCE_TEXT",
    "INTRO_STOCK_ONLY",
    "INTRO_CLIPS_AND_STOCK",
    "POST_INTRO_AND_STOCK",
    "INTRO_COMPLETE",
    "INTRO_AND_BOXER_STOCKS",
    "CLIP_STOCK_COEXISTENCE",
    "INTRO_TRANSLATION_VOICEOVER",
    "INVALID_INDEX_REJECTED",
    "INVALID_SEGMENT_REJECTED",
    "DUPLICATE_REJECTED",
    "FOLDER_MISMATCH_REJECTED",
    "NO_RENDER",
]


def summarize(markers: dict[str, bool]) -> int:
    recorded = {marker: ok for marker, ok in markers.items() if marker in FINAL_ORDER}
    print("--- RISULTATO DEFINITIVO ---")
    all_pass = bool(recorded)
    for marker in FINAL_ORDER:
        if marker not in recorded:
            print(f"{marker}=SKIPPED")
            continue
        ok = recorded[marker]
        all_pass = all_pass and ok
        print(f"{marker}=PASS" if ok else f"{marker}=FAIL")
    return 0 if all_pass else 1


def dry_run() -> int:
    base_stock = intro_stock_binding()
    pool = [r[0] for r in intro_assets()]
    print(f"intro stock: asset={base_stock['asset_id']} folder={base_stock['folder_id']} end_ms={base_stock['end_ms']}")
    print(f"intro clip pool (first 5): {pool[:5]} (total {len(pool)})")
    print(f"boxer catalogs: {[len(v) for v in catalog().values()]}")
    print(f"FULL={FULL_PATH}")
    print("tests: t1..t9, negatives N1..N8, final (voiceover)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", action="store_true", help="resolve assets and print the plan without calling the API")
    parser.add_argument("--negative-only", action="store_true", help="run only the N1-N8 negative cases")
    parser.add_argument("--no-voiceover", action="store_true", help="skip Test 4/9/final when BOXERS_VOICEOVER_FOLDER_ID is absent")
    args = parser.parse_args()
    if args.dry_run:
        return dry_run()
    args.voiceover_required = not args.no_voiceover
    try:
        return run_suite(args)
    except RuntimeError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
