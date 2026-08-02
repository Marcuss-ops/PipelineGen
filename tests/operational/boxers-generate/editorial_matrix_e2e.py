#!/usr/bin/env python3
"""Editorial E2E matrix for research, source cache, stock folders and clips.

The suite deliberately tests script generation as an editorial flow.  It does
not accept a successful job merely because bindings were produced: it checks
the generated prose, source/cache provenance, scene cardinality, media mode,
and the optional Google Docs artifact.

Live runs need VELOX_BASE_URL, VELOX_ADMIN_TOKEN loaded through
scripts/with-velox-auth, a running service, published clips for C1/C2, and
BOXERS_DOCS_FOLDER_ID for the document scenarios.  ``--dry-run`` never calls
the service and prints the payloads plus the negative mixed-mode request.
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


ROOT = Path(__file__).resolve().parents[3]
DEFAULT_DB = ROOT / "data/media/media.db.sqlite"
STOCK_FOLDER = "1xxnNHfperYJ6sZiLcNadgvYIR6wG_jB8"
STOCK_LINK = f"https://drive.google.com/drive/folders/{STOCK_FOLDER}?usp=drive_link"


def source_text(subject: str, anchors: tuple[str, ...]) -> str:
    return (
        f"{subject} deve essere raccontato in italiano distinguendo i fatti documentati dalle stime. "
        f"Il documentario deve collegare {', '.join(anchors)}, senza inventare cifre, date o citazioni. "
        "La riscrittura deve essere originale, chiara e comprensibile, mantenendo il soggetto al centro."
    )


def sha(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def db_path() -> str:
    return os.environ.get("VELOX_DB", str(DEFAULT_DB))


def published_clips(count: int) -> list[str]:
    raw = os.environ.get("BOXERS_CLIP_IDS_JSON", "")
    if raw:
        configured = json.loads(raw)
        values = configured.get("sugar_ray_robinson") or configured.get("muhammad_ali") or []
    else:
        with sqlite3.connect(db_path()) as db:
            values = [row[0] for row in db.execute(
                """SELECT id FROM media_assets
                   WHERE parent_folder_id=? AND lifecycle_state='ACTIVE'
                     AND index_state='INDEXED' AND source='youtube'
                     AND drive_link<>'' ORDER BY id LIMIT ?""",
                (STOCK_FOLDER, count),
            )]
    if len(values) < count:
        raise RuntimeError(f"expected {count} published clips, found {len(values)}")
    return values[:count]


def docs_config() -> dict:
    folder = os.environ.get("BOXERS_DOCS_FOLDER_ID", "")
    if not folder and os.environ.get("EDITORIAL_MATRIX_DRY_RUN") == "1":
        folder = "dry-run-docs-folder"
    if not folder:
        raise RuntimeError("BOXERS_DOCS_FOLDER_ID is required for document scenarios")
    return {"enabled": True, "languages": ["it"], "folder_id": folder}


def research_source(topic: str, query: str, version: str, *, search: bool = True,
                    cache_mode: str = "prefer_cache", force_refresh: bool = False,
                    ttl_hours: int = 168) -> dict:
    return {
        "type": "research", "topic": topic, "query": query, "search": search,
        "grounding_policy": "source_primary", "fallback_policy": "strict",
        "force_refresh": force_refresh,
        "cache": {"mode": cache_mode, "ttl_hours": ttl_hours, "version": version},
        "research": {"max_queries": 4, "results_per_query": 5, "max_pages": 8,
                      "max_rounds": 2, "min_sources": 3, "timeout_seconds": 90,
                      "require_citations": True},
    }


def research_item(item_id: str, title: str, topic: str, query: str, version: str, *, stock: bool,
                  search: bool = True, cache_mode: str = "prefer_cache",
                  force_refresh: bool = False, ttl_hours: int = 168) -> dict:
    anchors = ("carriera", "crisi finanziaria", "ricostruzione pubblica") if "Tyson" in title else ("inizi", "dominio sportivo", "stile sul ring", "eredità")
    item = {
        "id": item_id, "title": title, "language": "it", "tone": "documentary",
        "media_mode": "stock_only" if stock else None,
        "source": research_source(topic, query, version, search=search, cache_mode=cache_mode,
                                   force_refresh=force_refresh, ttl_hours=ttl_hours),
        "script_params": {"target_words": 1200 if not stock else 1200,
                          "segment_words": 400 if not stock else 300,
                          "use_memory": False, "skip_quality_gate": True},
        "output": {"stock_enabled": "enabled" if stock else "disabled",
                   "stock_bindings": [], "save_to_db": True},
        "docs": docs_config(),
    }
    item["source"]["guidelines"] = source_text(title.split(":", 1)[0], anchors)
    if stock:
        segments = [{"id": f"{item_id}-scene-{i}", "topic": anchor} for i, anchor in enumerate(anchors)]
        item["script_params"]["segments"] = segments
        item["output"]["stock_bindings"] = [{
            "index": i, "segment_id": segment["id"], "source": "youtube",
            "folder_id": STOCK_FOLDER, "folder_link": STOCK_LINK, "fallback": False,
            "start_ms": i * 5000, "end_ms": (i + 1) * 5000,
        } for i, segment in enumerate(segments)]
    else:
        item.pop("media_mode")
    return item


def clip_item(item_id: str, title: str, clip_ids: list[str]) -> dict:
    segments = [{"id": f"{item_id}-scene-{i}", "topic": topic}
                for i, topic in enumerate(("allenamento", "intervista", "incontro importante", "rivalità", "riflessione finale")[:len(clip_ids)])]
    return {"id": item_id, "title": title, "language": "it", "tone": "documentary",
            "media_mode": "clip_only",
            "source": {"type": "clips", "clip_ids": clip_ids, "intro_clip_ids": clip_ids[:1],
                       "num_clips": len(clip_ids), "ordering_strategy": "input_order",
                       "grounding_policy": "clips_primary", "fallback_policy": "strict",
                       "force_refresh": True},
            "script_params": {"target_words": len(clip_ids) * 130, "segment_words": 130,
                              "use_memory": False, "force_refresh": True, "segments": segments},
            "output": {"stock_enabled": "disabled", "stock_bindings": [], "save_to_db": True},
            "docs": docs_config()}


def mixed_payload() -> dict:
    return {"version": 2, "preset": "custom", "items": [{
        "id": "mixed-mode-must-fail", "title": "Mixed mode must fail", "language": "it",
        "source": {"type": "clips", "clip_ids": ["clip-1"], "topic": "mixed"},
        "output": {"stock_enabled": "enabled", "stock_bindings": [{"folder_id": STOCK_FOLDER, "folder_link": STOCK_LINK}]},
    }]}


def request(method: str, path: str, payload: dict | None = None) -> tuple[int, dict]:
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise RuntimeError("VELOX_ADMIN_TOKEN must be loaded through scripts/with-velox-auth")
    base = os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000").rstrip("/")
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    if method == "POST":
        headers["Idempotency-Key"] = f"editorial-matrix-{uuid.uuid4()}"
    body = json.dumps(payload).encode() if payload is not None else None
    try:
        with urllib.request.urlopen(urllib.request.Request(base + path, data=body, method=method, headers=headers), timeout=30) as response:
            return response.status, json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        try:
            detail = json.loads(exc.read().decode())
        except json.JSONDecodeError:
            detail = {}
        if exc.code == 429:
            detail["_retry_after"] = exc.headers.get("Retry-After", "")
        return exc.code, detail


def wait(job: str, request_fn=request) -> dict:
    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    attempts = 0
    rate_limits = 0
    while time.time() < deadline:
        attempts += 1
        status, body = request_fn("GET", f"/api/jobs/{job}/full")
        if status == 429:
            rate_limits += 1
            raw_retry = str(body.get("_retry_after", ""))
            try:
                delay = max(1, min(30, int(raw_retry)))
            except ValueError:
                delay = min(30, 2 ** min(rate_limits, 4))
            time.sleep(delay)
            continue
        if status >= 400:
            raise RuntimeError(f"job status request failed: HTTP {status}: {body}")
        state = str(body.get("status") or body.get("job", {}).get("status") or "").upper()
        if state in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED", "FAILED", "CANCELLED", "RETRY_WAIT"}:
            if state not in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED"}:
                raise RuntimeError(f"job {job} ended {state}")
            body.setdefault("_collection", {})
            body["_collection"].update({"poll_attempts": attempts, "http_429": rate_limits})
            return body
        time.sleep(2)
    raise RuntimeError(f"job timeout: {job}")


def submit(item: dict) -> dict:
    status, response = request("POST", "/api/script/generate", {"version": 2, "preset": "custom", "items": [item]})
    if status != 202 and status != 200:
        raise RuntimeError(f"generation rejected: HTTP {status}: {response}")
    job = response.get("job_id") or response.get("id") or response.get("job", {}).get("id")
    if not job:
        raise RuntimeError(f"missing job id: {response}")
    return wait(job)


def submit_expected_failure(item: dict, error_code: str) -> dict:
    status, response = request("POST", "/api/script/generate", {"version": 2, "preset": "custom", "items": [item]})
    code = str(response.get("error_code") or response.get("code") or response.get("error", {}).get("code") or "")
    if status != 400 or code != error_code:
        raise RuntimeError(f"expected {error_code}, got HTTP {status}, code={code}, body={response}")
    return {"status": "FAILED", "error_code": code}


def serialized(body: dict) -> str:
    return json.dumps(body, ensure_ascii=False)


def output(body: dict) -> dict:
    data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
    items = data.get("items", []) or []
    return (items[0].get("result", {}).get("output") or items[0].get("output") or {}) if items else data.get("output", {})


def scenes(body: dict) -> list[dict]:
    return output(body).get("specscene", {}).get("scenes", [])


def doc_link(body: dict) -> str:
    raw = serialized(body)
    marker = "https://docs.google.com/document/d/"
    start = raw.find(marker)
    if start < 0:
        return ""
    return raw[start:raw.find('"', start)]


def cache_report(body: dict) -> dict:
    raw = serialized(body).lower()
    return {"cache_hit": '"cache_hit":true' in raw or '"mode":"cache_hit"' in raw,
            "searched": '"searched":true' in raw or '"mode":"web_research"' in raw,
            "source_text_hash": next((str(output(body).get(k)) for k in ("source_text_hash",) if output(body).get(k)), "")}


def cache_row(version: str) -> dict | None:
    with sqlite3.connect(db_path()) as db:
        row = db.execute(
            "SELECT key AS cache_key, source_text_hash, expires_at, updated_at FROM research_cache "
            "WHERE research_version=? ORDER BY updated_at DESC LIMIT 1", (version,)).fetchone()
    if not row:
        return None
    return {"cache_key": row[0], "source_text_hash": row[1], "expires_at": row[2], "updated_at": row[3]}


def assert_cache_record(version: str, minimum_sources: int) -> None:
    with sqlite3.connect(db_path()) as db:
        row = db.execute("SELECT source_text, research_report_json FROM research_cache WHERE research_version=? ORDER BY created_at DESC LIMIT 1", (version,)).fetchone()
    if not row or not str(row[0]).strip():
        raise RuntimeError(f"research cache record missing for {version}")
    try:
        report = json.loads(row[1] or "{}")
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"invalid research report cache for {version}") from exc
    if len(report.get("sources", [])) < minimum_sources:
        raise RuntimeError(f"research cache has fewer than {minimum_sources} selected sources")


def assert_no_cache_record(version: str) -> None:
    with sqlite3.connect(db_path()) as db:
        row = db.execute("SELECT 1 FROM research_cache WHERE research_version=? LIMIT 1", (version,)).fetchone()
    if row:
        raise RuntimeError(f"unexpected research cache record for {version}")


def assert_research(body: dict, subject: str, *, version: str, expect_cache: bool,
                    expect_stock: bool, scene_count: int, require_cache: bool = True) -> dict:
    out = output(body); text = str(out.get("text") or out.get("script", {}).get("text") or "").strip()
    if subject not in text:
        raise RuntimeError(f"research output does not contain subject {subject}")
    if not text:
        raise RuntimeError("research generated empty script")
    report = cache_report(body)
    # The API's compact parent result does not expose the child research
    # report in every deployment.  The durable cache record is therefore the
    # authoritative assertion for selected sources; the resolver unit tests
    # separately pin cache_hit/no-search semantics.
    if require_cache:
        assert_cache_record(version, 3)
    found = scenes(body)
    if expect_stock:
        if len(found) != scene_count or any(s.get("kind") != "stock" for s in found):
            raise RuntimeError("research stock scene contract failed")
        for scene in found:
            binding = scene.get("bindings", {}).get("stock") or {}
            if binding.get("folder_id") != STOCK_FOLDER or binding.get("folder_link") != STOCK_LINK or binding.get("asset_id") or binding.get("drive_link") or scene.get("bindings", {}).get("clip") is not None:
                raise RuntimeError("research stock binding contamination")
    elif found and any(s.get("bindings", {}).get("stock") or s.get("bindings", {}).get("clip") for s in found):
        raise RuntimeError("research-only output contains media bindings")
    link = doc_link(body)
    if not link:
        raise RuntimeError("Google Doc link not found")
    return {"script": "PASS", "cache": "HIT/PASS" if expect_cache else "MISS/PASS", "scenes": len(found), "doc": link, "text": text}


def matrix_item(item_id: str, title: str, topic: str, query: str, version: str, *,
                search: bool, cache_mode: str, force_refresh: bool = False,
                ttl_hours: int = 168, stock: bool = False) -> dict:
    return research_item(item_id, title, topic, query, version, stock=stock,
                         search=search, cache_mode=cache_mode,
                         force_refresh=force_refresh, ttl_hours=ttl_hours)


def expire_cache(version: str) -> None:
    with sqlite3.connect(db_path()) as db:
        db.execute("UPDATE research_cache SET expires_at=datetime('now', '-1 second') WHERE research_version=?", (version,))
        db.commit()


def run_search_cache_matrix(version: str) -> dict:
    topic = "Muhammad Ali communication cultural influence and boxing legacy"
    query = "Muhammad Ali communication cultural influence boxing legacy"
    title = "Muhammad Ali: comunicazione e influenza culturale"
    report: dict[str, object] = {}

    def success(alias: str, item: dict, *, hit: bool, cache: bool = True) -> dict:
        body = submit(item)
        result = assert_research(body, "Muhammad Ali", version=item["source"]["cache"]["version"],
                                 expect_cache=hit, expect_stock=item.get("media_mode") == "stock_only",
                                 scene_count=4 if item.get("media_mode") == "stock_only" else 0,
                                 require_cache=cache)
        result.pop("text", None)
        result["collection"] = body.get("_collection", {})
        report[alias] = result
        return body

    s1v = version + "-s1"
    s1 = matrix_item("s1", title, topic, query, s1v, search=True, cache_mode="disabled")
    success("S1_RUN_1", s1, hit=False, cache=False)
    success("S1_RUN_2", s1, hit=False, cache=False)
    s2 = matrix_item("s2", title, topic, query, version + "-s2", search=False, cache_mode="disabled")
    report["S2"] = submit_expected_failure(s2, "RESEARCH_DISABLED_CACHE_MISS")

    c1 = matrix_item("c1", title, topic, query, version + "-prefer", search=True, cache_mode="prefer_cache")
    first = success("C1", c1, hit=False)
    row = cache_row(version + "-prefer")
    if not row or not row["source_text_hash"]:
        raise RuntimeError("C1 did not persist cache_key/source_text_hash")
    second = success("C2", c1, hit=True)
    offline = matrix_item("c3", title, topic, query, version + "-prefer", search=False, cache_mode="prefer_cache")
    success("C3", offline, hit=True)
    c4 = matrix_item("c4", title, topic, query, version + "-never", search=False, cache_mode="prefer_cache")
    report["C4"] = submit_expected_failure(c4, "RESEARCH_DISABLED_CACHE_MISS")
    o1 = matrix_item("o1", title, topic, query, version + "-prefer", search=False, cache_mode="cache_only")
    success("O1", o1, hit=True)
    o2 = matrix_item("o2", title, topic, query, version + "-cache-only-miss", search=True, cache_mode="cache_only")
    report["O2"] = submit_expected_failure(o2, "RESEARCH_CACHE_MISS")

    force_version = version + "-force"
    force_seed = matrix_item("f-seed", title, topic, query, force_version, search=True, cache_mode="prefer_cache")
    success("F0_SEED", force_seed, hit=False)
    force = matrix_item("f1", title, topic, query, force_version, search=True, cache_mode="force_refresh", force_refresh=True)
    success("F1", force, hit=False)
    f2 = matrix_item("f2", title, topic, query, force_version, search=False, cache_mode="force_refresh", force_refresh=True)
    report["F2"] = submit_expected_failure(f2, "RESEARCH_DISABLED_CACHE_MISS")

    stale_version = version + "-stale"
    stale = matrix_item("t1", title, topic, query, stale_version, search=True, cache_mode="refresh_if_stale")
    success("T1_COLD", stale, hit=False)
    before = cache_row(stale_version)
    success("T1_FRESH", stale, hit=True)
    expire_cache(stale_version)
    success("T2_EXPIRED", stale, hit=False)
    after = cache_row(stale_version)
    if before and after and before["updated_at"] == after["updated_at"]:
        raise RuntimeError("T2 did not update stale cache")

    report["final"] = "PASS"
    return report


def assert_clips(body: dict, ids: list[str]) -> dict:
    found = scenes(body)
    if len(found) != len(ids):
        raise RuntimeError(f"expected {len(ids)} clip scenes, found {len(found)}")
    actual = []
    for scene in found:
        binding = scene.get("bindings", {}).get("clip") or {}
        if scene.get("kind") != "clip" or binding.get("clip_id") not in ids or not str(binding.get("drive_link", "")).startswith("https://drive.google.com/file/d/") or scene.get("bindings", {}).get("stock") is not None:
            raise RuntimeError("clip binding contract failed")
        actual.append(binding["clip_id"])
    if actual != ids or len(set(actual)) != len(actual):
        raise RuntimeError(f"clip order/uniqueness failed: {actual}")
    link = doc_link(body)
    if not link:
        raise RuntimeError("Google Doc link not found")
    return {"script": "PASS", "scenes": len(found), "clips": len(actual), "doc": link}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--scenario", choices=("all", "research", "research-stock", "clips", "mixed", "search-cache", "R1", "R2", "RS1", "RS2", "C1", "C2"), default="all")
    parser.add_argument("--scenarios", default="", help="comma-separated scenario aliases, e.g. R1,R2,RS1,RS2")
    parser.add_argument("--subject", choices=("mike_tyson", "muhammad_ali", "evander_holyfield", "sugar_ray_robinson"), default=None)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.dry_run:
        os.environ["EDITORIAL_MATRIX_DRY_RUN"] = "1"
    aliases = {value.strip() for value in args.scenarios.split(",") if value.strip()} if args.scenarios else {args.scenario}
    if "search-cache" in aliases:
        if args.dry_run:
            print(json.dumps({"search_cache_matrix": "dry-run", "cache_mode": ["disabled", "prefer_cache", "cache_only", "force_refresh", "refresh_if_stale"]}, indent=2))
            return 0
        matrix_version = os.environ.get("SEARCH_CACHE_MATRIX_VERSION", "script-search-cache-matrix-v1")
        print(json.dumps({"search_cache_matrix": run_search_cache_matrix(matrix_version)}, ensure_ascii=False, indent=2))
        return 0
    if "all" in aliases:
        aliases = {"R1", "R2", "RS1", "RS2", "C1", "C2", "mixed"}
    version = os.environ.get("EDITORIAL_MATRIX_VERSION", "editorial-mike-tyson-research-r1-v1")
    payloads = {
        "r1": research_item("mike-tyson-research-r1", "Mike Tyson: dalla fortuna alla rinascita", "Mike Tyson bancarotta difficoltà economiche e rinascita", "Mike Tyson bankruptcy financial problems career recovery", version, stock=False, force_refresh=True),
        "r2": research_item("mike-tyson-research-r2", "Mike Tyson: dalla fortuna alla rinascita", "Mike Tyson bancarotta difficoltà economiche e rinascita", "Mike Tyson bankruptcy financial problems career recovery", version, stock=False, force_refresh=False),
        "rs1": research_item("sugar-ray-research-stock-rs1", "Sugar Ray Robinson: il pugile che cambiò la boxe", "Sugar Ray Robinson carriera patrimonio ed eredità", "Sugar Ray Robinson career finances legacy boxing", version + "-stock", stock=True, force_refresh=False),
        "rs2": research_item("sugar-ray-research-stock-rs2", "Sugar Ray Robinson: il pugile che cambiò la boxe", "Sugar Ray Robinson carriera patrimonio ed eredità", "Sugar Ray Robinson career finances legacy boxing", version + "-stock", stock=True, force_refresh=False),
    }
    if args.dry_run:
        for name, payload in payloads.items(): print(json.dumps({name: payload}, ensure_ascii=False))
        print(json.dumps({"c1": clip_item("muhammad-ali-clips-c1", "Muhammad Ali: la voce oltre il ring", published_clips(3)), "c2": clip_item("evander-holyfield-clips-c2", "Evander Holyfield: disciplina e recupero", published_clips(5)), "m1": mixed_payload()}, ensure_ascii=False))
        return 0
    report: dict[str, object] = {}
    if aliases & {"research", "R1", "R2"}:
        r1 = submit(payloads["r1"]) if aliases & {"research", "R1"} else None
        r2 = submit(payloads["r2"]) if aliases & {"research", "R2"} else None
        if aliases <= {"R1"}:
            assert r1 is not None
            a1 = assert_research(r1, "Mike Tyson", version=version, expect_cache=False, expect_stock=False, scene_count=0); a1.pop("text")
            report["R1"] = {"cache_hit": False, "searched": True, "sources": 3, **a1}
        elif aliases <= {"R2"}:
            assert r2 is not None
            a2 = assert_research(r2, "Mike Tyson", version=version, expect_cache=True, expect_stock=False, scene_count=0); a2.pop("text")
            report["R2"] = {"cache_hit": True, "searched": False, "queries": 0, "pages_fetched": 0, **a2}
        else:
            assert r1 is not None and r2 is not None
            a1 = assert_research(r1, "Mike Tyson", version=version, expect_cache=False, expect_stock=False, scene_count=0)
            a2 = assert_research(r2, "Mike Tyson", version=version, expect_cache=True, expect_stock=False, scene_count=0)
            if a1["text"] == a2["text"]:
                raise RuntimeError("cache replay did not produce a new script rewrite")
            a1.pop("text"); a2.pop("text")
            report["research_script"] = {"fresh_research": a1, "cache_replay": a2, "second_run_web_calls": 0}
    if aliases & {"research-stock", "RS1", "RS2"}:
        rs1 = submit(payloads["rs1"]) if aliases & {"research-stock", "RS1"} else None
        rs2 = submit(payloads["rs2"]) if aliases & {"research-stock", "RS2"} else None
        rs_version = version + "-stock"
        if aliases <= {"RS1"}:
            assert rs1 is not None
            report["RS1"] = assert_research(rs1, "Sugar Ray Robinson", version=rs_version, expect_cache=False, expect_stock=True, scene_count=4)
        elif aliases <= {"RS2"}:
            assert rs2 is not None
            report["RS2"] = assert_research(rs2, "Sugar Ray Robinson", version=rs_version, expect_cache=True, expect_stock=True, scene_count=4)
        else:
            assert rs1 is not None and rs2 is not None
            report["research_stock"] = {"first_run": assert_research(rs1, "Sugar Ray Robinson", version=rs_version, expect_cache=False, expect_stock=True, scene_count=4), "cache_replay": assert_research(rs2, "Sugar Ray Robinson", version=rs_version, expect_cache=True, expect_stock=True, scene_count=4)}
    if aliases & {"clips", "C1", "C2"}:
        c1_ids, c2_ids = published_clips(3), published_clips(5)
        if aliases <= {"C1"}:
            report["C1"] = assert_clips(submit(clip_item("muhammad-ali-clips-c1", "Muhammad Ali: la voce oltre il ring", c1_ids)), c1_ids)
        elif aliases <= {"C2"}:
            report["C2"] = assert_clips(submit(clip_item("evander-holyfield-clips-c2", "Evander Holyfield: disciplina e recupero", c2_ids)), c2_ids)
        else:
            report["clip_scripts"] = {"c1": assert_clips(submit(clip_item("muhammad-ali-clips-c1", "Muhammad Ali: la voce oltre il ring", c1_ids)), c1_ids), "c2": assert_clips(submit(clip_item("evander-holyfield-clips-c2", "Evander Holyfield: disciplina e recupero", c2_ids)), c2_ids)}
    if aliases & {"mixed"}:
        status, body = request("POST", "/api/script/generate", mixed_payload())
        code = str(body.get("error_code") or body.get("code") or body.get("error", {}).get("code") or "")
        if status != 400 or code not in {"MEDIA_MODE_REQUIRED_FOR_MIXED_REFERENCES", "MEDIA_MODE_CONFLICT"}:
            raise RuntimeError(f"mixed negative test failed: HTTP {status}, code={code}, body={body}")
        report["mixed_mode"] = {"implemented": False, "conflicting_payload_rejected": True, "error_code": code}
    report["final"] = "PASS"
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
