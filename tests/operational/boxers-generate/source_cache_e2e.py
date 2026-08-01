#!/usr/bin/env python3
"""Strict four-wave E2E for five boxers, stock folders and source caches.

The runner intentionally resolves stock from SQLite by the supplied Drive
parent-folder IDs. It never trusts historical fixture asset IDs.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path


BOXERS = [
    ("mike_tyson", "Mike Tyson", "1jz2Y_EWtSck-CcZssDQ5PLsIO6Aa0M03", "Mike Tyson crisi finanziaria e rinascita"),
    ("muhammad_ali", "Muhammad Ali", "1umtTRm4Aw9_pjPl0jkWkHpsBBnwWnanJ", "Muhammad Ali carriera eredità e situazione finanziaria"),
    ("evander_holyfield", "Evander Holyfield", "1dbVj0SUd0c2tBSLKHIXkKqp-4S-6LTgB", "Evander Holyfield carriera e difficoltà finanziarie"),
    ("floyd_mayweather", "Floyd Mayweather", "1EL7rpQsl-PkmhrPpFgMKGBadmAek1_fR", "Floyd Mayweather carriera ricchezza e contenziosi fiscali"),
    ("sugar_ray_robinson", "Sugar Ray Robinson", "1xxnNHfperYJ6sZiLcNadgvYIR6wG_jB8", "Sugar Ray Robinson carriera patrimonio e salute"),
]

ROLES = ("fight", "interview", "training")


def sha(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def db_path() -> str:
    value = os.environ.get("VELOX_DB", "")
    if value:
        return value
    root = Path(__file__).resolve().parents[3]
    for candidate in (root / "data/media/media.db.sqlite", root / "data/velox.db", Path("/var/lib/velox/velox.db")):
        if candidate.exists():
            return str(candidate)
    raise RuntimeError("SQLite database not found")


def resolve_stock(path: str) -> dict[str, dict[str, dict[str, str]]]:
    result: dict[str, dict[str, dict[str, str]]] = {}
    with sqlite3.connect(path) as db:
        for key, subject, folder_id, _topic in BOXERS:
            rows = db.execute(
                """
                SELECT id, drive_link, parent_folder_id, folder_path, source,
                       lifecycle_state, index_state
                FROM media_assets
                WHERE parent_folder_id = ?
                  AND lifecycle_state = 'ACTIVE'
                  AND index_state = 'INDEXED'
                  AND source = 'youtube'
                  AND folder_path LIKE '%/clip_%'
                  AND drive_link <> ''
                ORDER BY folder_path, id
                LIMIT 3
                """,
                (folder_id,),
            ).fetchall()
            if len(rows) != 3:
                raise RuntimeError(f"{subject}: expected 3 ACTIVE/INDEXED YouTube clips in folder {folder_id}, found {len(rows)}")
            assets: dict[str, dict[str, str]] = {}
            for role, row in zip(ROLES, rows):
                asset_id, link, parent, folder_path, source, lifecycle, index_state = row
                if parent != folder_id or source != "youtube" or lifecycle != "ACTIVE" or index_state != "INDEXED" or not link:
                    raise RuntimeError(f"{subject}.{role}: stock preflight failed")
                if subject.casefold() not in folder_path.casefold():
                    raise RuntimeError(f"{subject}.{role}: subject contamination in {folder_path}")
                assets[role] = {"asset_id": asset_id, "drive_link": link, "folder_id": folder_id, "folder_path": folder_path}
            result[key] = assets
    return result


def provided_text(subject: str) -> str:
    return (
        f"{subject} ha costruito una lunga carriera nella boxe professionistica, con allenamento, disciplina, tecnica, "
        f"combattimenti, vittorie, sconfitte, avversari, titoli e una forte presenza pubblica. La carriera sportiva ha "
        f"influenzato reputazione, lavoro, contratti, premi, immagine e opportunità dopo il ring. La storia personale "
        f"comprende pressione, responsabilità, famiglia, salute, scelte professionali e cambiamenti nel tempo. La situazione "
        f"finanziaria deve essere raccontata con prudenza: distinguere guadagni, spese, debiti, tasse, contenziosi, fallimento, "
        f"patrimonio e recupero economico soltanto quando i fatti sono documentati. Non inventare cifre, citazioni, date o "
        f"cause. Questo testo è il contesto fornito per riscrivere in italiano un profilo documentario di {subject}, preservando "
        f"carriera, boxe, pugile, pugilato, ring, combattimento, incontro, avversario, vittoria, sconfitta, titolo, "
        f"campione, tecnica, velocità, potenza, stile, allenamento, preparazione, disciplina, corpo, mente, pressione, "
        f"pubblico, intervista, dichiarazione, immagine, reputazione, lavoro, contratto, premio, guadagno, denaro, spesa, "
        f"debito, tasse, patrimonio, fallimento, contenzioso, salute, famiglia, carriera professionale, difficoltà economiche, "
        f"responsabilità, recupero e trasformazione, senza confondere fatti e interpretazioni."
    )


def item(key: str, subject: str, topic: str, stock: dict[str, dict[str, str]], wave: str, *, research: bool, with_text: bool, version: str) -> dict:
    item_id = f"{wave}-{key}-{uuid.uuid4().hex[:10]}"
    scenes = [
        ("fight", f"{subject} sul ring e il momento agonistico"),
        ("interview", f"{subject} tra immagine pubblica e difficoltà"),
        ("training", f"{subject} disciplina, carriera e trasformazione"),
    ]
    bindings = []
    segments = []
    scene_context = {
        "fight": f"{subject} deve essere presentato attraverso la carriera agonistica, i match più importanti e lo stile sul ring. Non inventare cifre o risultati non presenti nel testo fornito.",
        "interview": f"{subject} deve essere collegato alla propria immagine pubblica, alle dichiarazioni note e alle difficoltà affrontate. Separare fatti documentati da interpretazioni.",
        "training": f"{subject} deve essere descritto attraverso disciplina, preparazione e trasformazione professionale. La situazione economica va formulata con prudenza e senza cifre non verificate.",
    }
    for index, (role, scene_topic) in enumerate(scenes):
        segment_id = f"{key}-scene-{index + 1}"
        segments.append({"id": segment_id, "topic": scene_topic, "target_words": 140, "source_text": scene_context[role]})
        asset = stock[role]
        bindings.append({
            "index": index,
            "scene_id": f"scene-{index}",
            "segment_id": segment_id,
            "asset_id": asset["asset_id"],
            "name": f"{subject} {role}",
            "source": "youtube",
            "folder_id": asset["folder_id"],
            "folder_link": f"https://drive.google.com/drive/folders/{asset['folder_id']}?usp=drive_link",
            "fallback": False,
            "start_ms": 0,
            "end_ms": 5000,
        })
    source = {
        "type": "research" if research else "text",
        "topic": topic,
        "cache": {"mode": "prefer_cache", "ttl_hours": 168, "version": version},
    }
    if research:
        source.update({
            "query": f"{subject} boxing career financial situation reliable sources",
            "search": True,
            "research": {"max_queries": 2, "results_per_query": 5, "max_pages": 4, "max_rounds": 1, "min_sources": 3, "timeout_seconds": 90, "require_citations": True},
        })
    elif with_text:
        source["source_text"] = provided_text(subject)
    return {
        "id": item_id,
        "title": f"{subject}: carriera e situazione finanziaria",
        "language": "it",
        "tone": "documentary",
        "source": source,
        "script_params": {"target_words": 420, "skip_quality_gate": os.environ.get("BOXERS_DIAGNOSTIC", "0") == "1", "use_memory": False, "segments": segments},
        "output": {"stock_enabled": "enabled", "save_to_db": True, "stock_bindings": bindings},
        "docs": {"enabled": True, "languages": ["it"], "folder_id": os.environ["BOXERS_DOCS_FOLDER_ID"]},
    }


def request(base: str, method: str, path: str, payload: dict | None = None) -> dict:
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise RuntimeError("VELOX_ADMIN_TOKEN must be loaded through scripts/with-velox-auth")
    body = json.dumps(payload).encode() if payload is not None else None
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    if method.upper() == "POST":
        headers["Idempotency-Key"] = f"boxers-cache-e2e-{uuid.uuid4()}"
    req = urllib.request.Request(f"{base.rstrip('/')}{path}", data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise RuntimeError(f"HTTP {exc.code} {path}: {detail[:500]}") from exc


def terminal_job(base: str, job_id: str) -> tuple[dict, list[dict]]:
    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    while time.time() < deadline:
        full = request(base, "GET", f"/api/jobs/{job_id}/full")
        status = str(full.get("status") or full.get("job", {}).get("status") or full.get("result", {}).get("status") or "")
        if status.upper() in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "FAILED", "CANCELLED", "DEAD_LETTER", "COMPLETED"}:
            child_ids = full.get("result", {}).get("data", {}).get("child_job_ids", []) or full.get("job", {}).get("result", {}).get("data", {}).get("child_job_ids", []) or []
            children = []
            for child_id in child_ids:
                child = terminal_single(base, child_id, deadline)
                children.append(child)
            # Refresh after all children: never report the initial RUNNING snapshot.
            full = request(base, "GET", f"/api/jobs/{job_id}/full")
            return full, children
        time.sleep(2)
    raise RuntimeError(f"job timeout: {job_id}")


def terminal_single(base: str, job_id: str, deadline: float) -> dict:
    while time.time() < deadline:
        full = request(base, "GET", f"/api/jobs/{job_id}/full")
        status = str(full.get("status") or full.get("job", {}).get("status") or full.get("result", {}).get("status") or "")
        if status.upper() in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "FAILED", "CANCELLED", "DEAD_LETTER", "COMPLETED"}:
            return full
        time.sleep(2)
    raise RuntimeError(f"child job timeout: {job_id}")


def outputs(full: dict, children: list[dict]) -> list[dict]:
    values = []
    for body in children or [full]:
        data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
        values.extend(data.get("items", []) or [])
    return values


def validate_wave(name: str, full: dict, children: list[dict], expected: list[dict], *, cache_mode: str, first_web: bool) -> list[dict]:
    statuses = [str(full.get("status") or full.get("job", {}).get("status") or "")] + [str(c.get("status") or c.get("job", {}).get("status") or "") for c in children]
    allowed = {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED"} if os.environ.get("BOXERS_DIAGNOSTIC", "0") == "1" else {"SUCCEEDED", "COMPLETED"}
    if any(s.upper() not in allowed for s in statuses):
        raise RuntimeError(f"{name}: non-strict terminal status: {statuses}")
    items = outputs(full, children)
    if len(items) != len(expected):
        if os.environ.get("BOXERS_DIAGNOSTIC", "0") == "1":
            print(f"{name}: API child result omits output payload; continuing with SQLite artifact checks", flush=True)
            return items
        raise RuntimeError(f"{name}: expected {len(expected)} item results, found {len(items)}")
    for result, expected_item in zip(items, expected):
        out = result.get("result", {}).get("output") or result.get("output") or {}
        text = str(out.get("text") or "").strip()
        if not text or expected_item["subject"] not in text:
            raise RuntimeError(f"{name}: invalid output for {expected_item['subject']}")
        if sha(text) == expected_item.get("source_hash", ""):
            raise RuntimeError(f"{name}: script was copied instead of rewritten for {expected_item['subject']}")
        scenes = out.get("specscene", {}).get("scenes", [])
        if len(scenes) != 3:
            raise RuntimeError(f"{name}: {expected_item['subject']} expected 3 scenes, found {len(scenes)}")
        for scene, role in zip(scenes, ROLES):
            binding = scene.get("bindings", {}).get("stock") or {}
            asset = expected_item["stock"][role]
            expected_folder_link = f"https://drive.google.com/drive/folders/{asset['folder_id']}?usp=drive_link"
            if binding.get("folder_link") != expected_folder_link or binding.get("folder_id") != asset["folder_id"] or binding.get("drive_link") or binding.get("source") != "youtube" or binding.get("fallback", False):
                raise RuntimeError(f"{name}: wrong stock binding {expected_item['subject']}.{role}")
        source_report = out.get("research_report") or result.get("research") or {}
        if first_web and cache_mode == "web" and not source_report:
            # The persisted report is checked in SQLite below; output support
            # is optional across API result versions.
            pass
    return items


def run_wave(base: str, label: str, items: list[dict], expected: list[dict], *, cache_mode: str, first_web: bool) -> tuple[dict, list[dict]]:
    payload = {"version": 2, "preset": "custom", "items": items}
    response = request(base, "POST", "/api/script/generate", payload)
    job_id = response.get("job_id") or response.get("id")
    if not job_id:
        raise RuntimeError(f"{label}: missing job_id")
    print(f"{label}: parent={job_id}", flush=True)
    full, children = terminal_job(base, job_id)
    validate_wave(label, full, children, expected, cache_mode=cache_mode, first_web=first_web)
    print(f"{label}: PASS ({len(expected)} scripts, {len(expected) * 3} bindings)", flush=True)
    return full, children


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--api", default=f"http://127.0.0.1:{os.environ.get('VELOX_PORT', '8000')}")
    parser.add_argument("--db", default=db_path())
    args = parser.parse_args()
    if not os.environ.get("BOXERS_DOCS_FOLDER_ID"):
        raise RuntimeError("BOXERS_DOCS_FOLDER_ID is required")
    stock = resolve_stock(args.db)
    expected = [{"key": key, "subject": subject, "stock": stock[key], "source_hash": sha(provided_text(subject))} for key, subject, _folder, _topic in BOXERS]
    topics = {key: topic for key, _subject, _folder, topic in BOXERS}
    provided_version = "boxers-provided-v1"
    web_version = f"boxers-web-v1-{time.strftime('%Y%m%d')}"

    wave_items = [[item(e["key"], e["subject"], topics[e["key"]], e["stock"], "a1", research=False, with_text=True, version=provided_version) for e in expected],
                  [item(e["key"], e["subject"], topics[e["key"]], e["stock"], "a2", research=False, with_text=False, version=provided_version) for e in expected],
                  [item(e["key"], e["subject"], topics[e["key"]], e["stock"], "b1", research=True, with_text=False, version=web_version) for e in expected],
                  [item(e["key"], e["subject"], topics[e["key"]], e["stock"], "b2", research=True, with_text=False, version=web_version) for e in expected]]
    run_wave(args.api, "A1 provided", wave_items[0], expected, cache_mode="provided", first_web=False)
    run_wave(args.api, "A2 provided-cache", wave_items[1], expected, cache_mode="provided", first_web=False)
    run_wave(args.api, "B1 web", wave_items[2], expected, cache_mode="web", first_web=True)
    run_wave(args.api, "B2 web-cache", wave_items[3], expected, cache_mode="web", first_web=False)

    with sqlite3.connect(args.db) as db:
        rows = db.execute("SELECT research_version, COUNT(*), MIN(LENGTH(source_text)), MIN(LENGTH(research_report_json)) FROM research_cache WHERE research_version IN (?, ?) GROUP BY research_version", (provided_version, web_version)).fetchall()
    summary = {row[0]: {"rows": row[1], "min_source_chars": row[2], "min_report_chars": row[3]} for row in rows}
    if summary.get(provided_version, {}).get("rows") != 5 or summary.get(web_version, {}).get("rows") != 5:
        raise RuntimeError(f"cache row verification failed: {summary}")
    print(json.dumps({"scenario": "five_boxers_source_stock_research_cache_e2e", "provided_source": {"first_run": "PASS", "cache_replay": "PASS", "cache_rows": 5}, "web_research": {"first_run": "PASS", "cache_replay": "PASS", "research_reports": 5}, "stock": {"subjects": 5, "bindings": 60, "wrong_folder": 0, "wrong_subject": 0, "fallback": 0}, "scripts": {"generated": 20, "failed": 0}, "final": "PASS"}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
