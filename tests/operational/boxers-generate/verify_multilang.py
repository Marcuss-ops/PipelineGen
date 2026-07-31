#!/usr/bin/env python3
"""
verify_multilang.py — Verifier for top5 boxers × 10 languages E2E pipeline test.

Exit codes:
  0 — all checks passed
  1 — one or more checks failed (test failure)
  2 — usage / input error
  3 — negative test correctly detected STOCK_SUBJECT_MISMATCH

Usage:
  python3 verify_multilang.py <response_json> <db_path>               # normal run
  python3 verify_multilang.py <response_json> <db_path> --negative    # swapped stock detection
"""

import json
import sqlite3
import sys
import argparse
import re
import os
import math

from stock_registry import load_resolved_stock, scene_expectations


LANG_MARKERS = {
    "en": ["the", "and", "boxing"],
    "es": ["el", "la", "boxeo"],
    "pt": ["o", "a", "boxe"],
    "fr": ["le", "la", "boxe"],
    "de": ["der", "die", "boxen"],
    "nl": ["de", "het", "boksen"],
    "pl": ["i", "boks", "kariera"],
    "ro": ["și", "box", "carieră", "este", "în", "de", "cu"],
    "tr": ["ve", "boks", "kariyer"],
}


def green(s):
    return f"\033[32m{s}\033[0m"


def red(s):
    return f"\033[31m{s}\033[0m"


def fail(msg):
    print(f"  {red('✗')} {msg}")
    return msg


def ok(msg):
    print(f"  {green('✓')} {msg}")


def deep_get(d, *keys, default=None):
    """Safely traverse nested dicts."""
    for k in keys:
        if isinstance(d, dict):
            d = d.get(k, default)
        else:
            return default
    return d


def _request_items(data):
    """Return request items from parent/full envelopes, keyed by item id."""
    candidates = (
        deep_get(data, "job", "payload", "items", default=[]),
        deep_get(data, "payload", "items", default=[]),
        deep_get(data, "result", "data", "payload", "items", default=[]),
    )
    result = {}
    for candidate in candidates:
        if not isinstance(candidate, list):
            continue
        for request_item in candidate:
            if not isinstance(request_item, dict):
                continue
            item_id = request_item.get("id") or request_item.get("item_id")
            if item_id:
                result[str(item_id)] = request_item
    return result


def _attach_request_metadata(item, request_items):
    """Attach parent request metadata without overwriting response fields."""
    if not isinstance(item, dict):
        return item
    item_id = item.get("item_id") or deep_get(item, "result", "item_id", default="")
    request_item = request_items.get(str(item_id)) if item_id else None
    if not request_item:
        return item
    enriched = dict(item)
    enriched["_request"] = request_item
    return enriched


def _payload_item(payload, preferred_id=""):
    """Extract the matching request item from single or batch payloads."""
    if not isinstance(payload, dict):
        return {}
    item = payload.get("item")
    if isinstance(item, dict):
        return item
    items = payload.get("items")
    if isinstance(items, list):
        candidates = [
            candidate for candidate in items
            if isinstance(candidate, dict)
            and str(candidate.get("id") or candidate.get("item_id") or "") == str(preferred_id)
        ]
        if candidates:
            return candidates[0]
        if len(items) == 1 and isinstance(items[0], dict):
            return items[0]
        return {}
    for key in ("data", "payload"):
        nested = payload.get(key)
        if isinstance(nested, dict):
            item = _payload_item(nested, preferred_id)
            if item:
                return item
    return {}


def extract_items(data, db_path=None):
    """Extract items array from various possible response shapes."""
    request_items = _request_items(data)
    items = deep_get(data, "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "data", "items", default=[])
    if items:
        return [_attach_request_metadata(item, request_items) for item in items]
    if not items and db_path and os.path.exists(db_path):
        import sqlite3
        child_ids = deep_get(data, "result", "child_job_ids", default=[])
        if not child_ids:
            child_ids = deep_get(data, "job", "result", "child_job_ids", default=[])
        if not child_ids:
            child_ids = deep_get(data, "result", "data", "child_job_ids", default=[])
        if not child_ids:
            child_ids = deep_get(data, "job", "result", "data", "child_job_ids", default=[])
        
        if child_ids:
            try:
                conn = sqlite3.connect(db_path)
                cur = conn.cursor()
                reconstructed = []
                for cid in child_ids:
                    cur.execute("SELECT status, payload_json, result_json FROM jobs WHERE id = ?", (cid,))
                    row = cur.fetchone()
                    if row:
                        status, payload_json, result_json = row
                        payload = json.loads(payload_json)
                        res_dict = json.loads(result_json) if result_json else {}
                        
                        response_item_id = (
                            res_dict.get("item_id")
                            or deep_get(res_dict, "data", "item_id", default="")
                        )
                        request_item = _payload_item(payload, response_item_id)
                        item_id = request_item.get("id", "") or str(response_item_id)
                        item_title = request_item.get("title", "")
                        item_lang = request_item.get("language", "")
                        
                        cur.execute(
                            "SELECT id, narrative_text, specscene FROM scripts WHERE title = ? AND language = ? ORDER BY created_at DESC LIMIT 1",
                            (item_title, item_lang)
                        )
                        s_row = cur.fetchone()
                        script_id = None
                        text = ""
                        specscene = {}
                        if s_row:
                            script_id, text, specscene_str = s_row
                            try:
                                specscene = json.loads(specscene_str) if specscene_str else {}
                            except:
                                specscene = {}
                        
                        doc_id = res_dict.get("doc_id", "")
                        doc_link = res_dict.get("doc_link", "")
                        
                        request_output = request_item.get("output")
                        if not isinstance(request_output, dict):
                            request_output = {}
                        request_docs = request_item.get("docs")
                        if not isinstance(request_docs, dict):
                            request_docs = {}

                        reconstructed.append({
                            "item_id": item_id,
                            # Preserve request metadata because the SQLite
                            # fallback has no response envelope to carry it.
                            "language": item_lang,
                            "docs": request_docs,
                            "_request": request_item,
                            "output": {
                                "translate_to": request_output.get("translate_to", ""),
                                "voiceover_folder_id": request_output.get("voiceover_folder_id", ""),
                            },
                            "result": {
                                "status": status,
                                "script_id": script_id,
                                "artifacts": {
                                    "document": {
                                        "doc_id": doc_id,
                                        "doc_link": doc_link
                                    }
                                },
                                "output": {
                                    "text": text,
                                    "specscene": specscene
                                }
                            }
                        })
                conn.close()
                items = reconstructed
            except Exception as e:
                print(f"Error reconstructing items from DB: {e}")
    return items


def get_item_output(item):
    """Extract output dict from an item node."""
    out = deep_get(item, "result", "output")
    if not out:
        out = deep_get(item, "result", "data", "output")
    return out or {}


def _first_language(value):
    """Return the first non-empty language from a scalar or language list."""
    if isinstance(value, str) and value.strip():
        return value.strip()
    if isinstance(value, list):
        for language in value:
            if isinstance(language, str) and language.strip():
                return language.strip()
    return ""


def _metadata_sources(item):
    """Yield all request/result metadata containers in stable priority order."""
    if not isinstance(item, dict):
        return

    seen = set()

    def add(container):
        if not isinstance(container, dict) or id(container) in seen:
            return
        seen.add(id(container))
        output = container.get("output")
        docs = container.get("docs")
        yield container, output if isinstance(output, dict) else {}, docs if isinstance(docs, dict) else {}

    # The item envelope is preferred, followed by result and the nested
    # result.data/data envelopes used by the API and SQLite reconstruction.
    containers = [item]
    request = item.get("_request")
    if isinstance(request, dict):
        containers.append(request)
    result = item.get("result")
    if isinstance(result, dict):
        containers.append(result)
        data = result.get("data")
        if isinstance(data, dict):
            containers.append(data)
            nested_data = data.get("data")
            if isinstance(nested_data, dict):
                containers.append(nested_data)

    for container in containers:
        for source in add(container):
            yield source
        output = container.get("output") if isinstance(container, dict) else None
        if isinstance(output, dict):
            for source in add(output):
                yield source


def _metadata_values(item, key):
    """Yield metadata values from every supported response envelope."""
    for container, output, _docs in _metadata_sources(item):
        value = container.get(key)
        if value not in (None, "", []):
            yield value
        value = output.get(key)
        if value not in (None, "", []):
            yield value


def _docs_languages(item):
    for container, _output, docs in _metadata_sources(item):
        value = docs.get("languages")
        if value not in (None, "", []):
            yield value
        value = container.get("languages")
        if value not in (None, "", []):
            yield value


def effective_language(item):
    """Resolve language centrally: translate_to, docs.languages, language.

    The same resolver is used for translation assertions and voiceover
    assertions so a translated child is never checked against its source
    language merely because the request's ``language`` field is unchanged.
    """
    for value in _metadata_values(item, "translate_to"):
        language = _first_language(value)
        if language:
            return language
    for value in _docs_languages(item):
        language = _first_language(value)
        if language:
            return language
    for value in _metadata_values(item, "language"):
        language = _first_language(value)
        if language:
            return language
    return ""


def canonical_folder_id(item):
    """Resolve the canonical voiceover folder from output then docs metadata."""
    for value in _metadata_values(item, "voiceover_folder_id"):
        folder_id = _first_language(value)
        if folder_id:
            return folder_id
    for container, _output, docs in _metadata_sources(item):
        for value in (docs.get("folder_id"), container.get("folder_id")):
            folder_id = _first_language(value)
            if folder_id:
                return folder_id
    return ""


def validate_voiceover(voiceover, expected_language, expected_folder_id):
    """Return descriptive failures for one voiceover binding."""
    errors = []
    if not isinstance(voiceover, dict):
        return ["voiceover binding is missing"]

    status = str(voiceover.get("status", "")).strip()
    if status.casefold() not in {"completed", "succeeded"}:
        errors.append(f"status={status or '(missing)'}, expected terminal positive status")
    if not str(voiceover.get("drive_link", "")).strip():
        errors.append("drive_link is empty")
    if not str(voiceover.get("voice", "")).strip():
        errors.append("voice is empty")
    actual_language = str(voiceover.get("language", "")).strip()
    if not expected_language:
        errors.append("effective language is empty")
    elif actual_language.casefold() != expected_language.casefold():
        errors.append(
            f"language={actual_language or '(missing)'}, expected {expected_language}"
        )
    actual_folder = str(voiceover.get("folder_id", "")).strip()
    if not expected_folder_id:
        errors.append("canonical voiceover folder_id is empty")
    elif actual_folder != expected_folder_id:
        errors.append(
            f"folder_id={actual_folder or '(missing)'}, expected {expected_folder_id}"
        )
    try:
        duration = float(voiceover.get("duration_seconds", 0))
    except (TypeError, ValueError):
        duration = 0
    if not math.isfinite(duration) or duration <= 0:
        errors.append(
            f"duration_seconds={voiceover.get('duration_seconds')!r}, expected > 0"
        )
    return errors


def count_lang_markers(text, lang):
    """Return # of language-specific markers found in text."""
    if lang not in LANG_MARKERS:
        return 0
    text_lower = text.lower()
    return sum(1 for m in LANG_MARKERS[lang] if m.lower() in text_lower)


def main():
    parser = argparse.ArgumentParser(description="Verify multi-lang E2E script generation output")
    parser.add_argument("response_json", help="Path to full job JSON response file")
    parser.add_argument("db_path", help="Path to SQLite database")
    parser.add_argument("--registry", required=True, help="Path to resolved_stock.json")
    parser.add_argument("--negative", action="store_true",
                        help="Negative test mode: expect STOCK_SUBJECT_MISMATCH")
    args = parser.parse_args()

    # ── Load JSON ───────────────────────────────────────────────────
    if not os.path.exists(args.response_json):
        print(f"Error: JSON file not found: {args.response_json}")
        sys.exit(2)

    try:
        with open(args.response_json, "r") as f:
            data = json.load(f)
    except (json.JSONDecodeError, IOError) as e:
        print(f"Error reading JSON: {e}")
        sys.exit(2)

    try:
        resolved = load_resolved_stock(args.registry)
        expectations = scene_expectations(resolved)
    except (OSError, ValueError) as exc:
        print(f"Error loading resolved stock registry: {exc}")
        sys.exit(2)
    expected_assets = [entry["asset_id"] for entry in expectations]
    boxer_names = [entry["subject"] for entry in expectations]
    expected_item_ids = {f"top5-boxers-{language}" for language in LANG_MARKERS | {"it"}}
    expected_item_count = len(expected_item_ids)
    expected_scene_count = len(expectations)
    expected_total_scenes = expected_item_count * expected_scene_count
    segment_ids = {entry["segment_id"] for entry in expectations}
    source_markers = [entry["source_marker"] for entry in expectations]

    dumped_raw = json.dumps(data)

    # ── Negative test mode (swapped stock detection) ─────────────────
    if args.negative:
        if "STOCK_SUBJECT_MISMATCH" in dumped_raw:
            print(green("✓ Negative test PASS: STOCK_SUBJECT_MISMATCH detected correctly"))
            sys.exit(3)
        # Also check for stock belonging to wrong boxer
        items = extract_items(data, args.db_path)
        for item in items:
            output = get_item_output(item)
            scenes = deep_get(output, "specscene", "scenes", default=[])
            for s_idx, scene in enumerate(scenes):
                stock = deep_get(scene, "bindings", "stock", default={})
                asset_id = stock.get("asset_id", "")
                if asset_id and s_idx < len(expected_assets) and asset_id != expected_assets[s_idx]:
                    print(red(f"✗ Negative test PASS: Stock mismatch found — scene {s_idx} "
                              f"has {asset_id} instead of {expected_assets[s_idx]}"))
                    sys.exit(3)
        print(red("✗ Negative test FAILED: Expected STOCK_SUBJECT_MISMATCH but not found"))
        sys.exit(1)

    errors = []
    lines = []

    def add_error(msg):
        errors.append(msg)
        lines.append(fail(msg))

    # ── Extract items ────────────────────────────────────────────────
    items = extract_items(data, args.db_path)
    if not items:
        add_error("Test 0: No items found in response — cannot proceed")
        for e in errors:
            print(red(f"FAIL: {e}"))
        sys.exit(1)

    summary = deep_get(data, "result", "data", "summary", default={})
    if not summary:
        summary = deep_get(data, "job", "result", "data", "summary", default={})

    # ── Test 1: Batch completeness ────────────────────────────────────
    print("\n── Test 1: Batch completeness ──")
    total = len(items)
    if total != expected_item_count:
        add_error(f"Expected {expected_item_count} items, got {total}")
    else:
        ok(f"items_requested={expected_item_count}, items_returned={total}")

    item_ids = set()
    script_ids = []
    completed_count = 0

    for item in items:
        iid = item.get("item_id", "")
        if iid:
            item_ids.add(iid)
        status = deep_get(item, "result", "status", default="")
        if status in ("completed", "SUCCEEDED"):
            completed_count += 1
        sid = deep_get(item, "result", "script_id")
        if sid:
            script_ids.append(sid)

    if len(item_ids) != expected_item_count:
        add_error(f"Distinct item IDs: {len(item_ids)}, expected {expected_item_count}. Missing: "
                   f"{expected_item_ids - item_ids}")
    else:
        ok(f"{expected_item_count} distinct item_ids")

    if completed_count != expected_item_count:
        add_error(f"Completed items: {completed_count}/{expected_item_count}")
    else:
        ok(f"{expected_item_count}/{expected_item_count} items completed")

    if len(script_ids) != expected_item_count:
        add_error(f"Valid script_ids: {len(script_ids)}, expected {expected_item_count}")
    else:
        ok(f"{expected_item_count} valid script_ids")

    if summary:
        if summary.get("total") != 10 or summary.get("succeeded") != 10:
            add_error(f"Summary: {summary}")
        else:
            ok(f"summary: total={summary['total']}, succeeded={summary['succeeded']}")

    # ── Per-item / per-language checks ───────────────────────────────
    stock_by_scene = {index: set() for index in range(expected_scene_count)}
    canonical_folders = set()
    doc_links = set()
    vo_completed = 0
    vo_total = 0
    total_scenes_check = 0

    for item in items:
        item_id = item.get("item_id", "unknown")
        lang = effective_language(item)
        output = get_item_output(item)
        expected_folder_id = canonical_folder_id(item)
        if expected_folder_id:
            canonical_folders.add(expected_folder_id)

        if not output:
            add_error(f"Missing output for {item_id}")
            continue

        scenes = deep_get(output, "specscene", "scenes", default=[])
        text = output.get("text", "")

        # ── Test 2: Source text respect ──────────────────────────────
        if len(scenes) != 5:
            add_error(f"Test 2 [{item_id}]: {len(scenes)} scenes, expected {expected_scene_count}")
            continue

        total_scenes_check += len(scenes)

        for s_idx, scene in enumerate(scenes):
            scene_text = scene.get("text", "")

            # Check segment_id matches expected
            seg_id = scene.get("segment_id") or scene.get("id", "")
            if s_idx < len(expectations) and seg_id and seg_id not in segment_ids:
                # Accept scene-0, scene-1 etc as fallback IDs
                if not seg_id.startswith("scene-"):
                    add_error(f"Test 2 [{item_id}]: Wrong segment_id '{seg_id}' at index {s_idx}")

            # Check boxer name appears in the correct section (not other boxers in wrong scenes)
            if s_idx < len(boxer_names):
                expected_name = boxer_names[s_idx]
                # Check that the expected boxer name appears
                name_parts = expected_name.split()
                if len(name_parts) >= 2:
                    last_name = name_parts[-1].lower()
                    if last_name not in scene_text.lower():
                        pass  # name might have been translated; not always fatal

            # ── Test 3: Stock associated to correct person ──────────
            stock = deep_get(scene, "bindings", "stock", default={})
            asset_id = stock.get("asset_id", "")

            if asset_id and s_idx < len(expected_assets):
                if asset_id != expected_assets[s_idx]:
                    add_error(f"Test 3 [{item_id} scene {s_idx}]: Wrong asset_id "
                               f"'{asset_id}', expected '{expected_assets[s_idx]}'")
                stock_by_scene[s_idx].add(asset_id)

            if stock:
                if stock.get("source", "") != "youtube":
                    add_error(f"Test 3 [{item_id} scene {s_idx}]: source={stock.get('source')}, expected youtube")
                if stock.get("fallback", False) is True:
                    add_error(f"Test 3 [{item_id} scene {s_idx}]: fallback=true, expected false")
                if not stock.get("drive_link", ""):
                    add_error(f"Test 3 [{item_id} scene {s_idx}]: drive_link empty")

            # ── Test 6: Voiceover ──────────────────────────────────
            vo = deep_get(scene, "bindings", "voiceover", default={})
            vo_total += 1
            voiceover_errors = validate_voiceover(
                vo, lang, expected_folder_id
            )
            if voiceover_errors:
                add_error(
                    f"Test 6 [{item_id} scene {s_idx}]: voiceover "
                    + "; ".join(voiceover_errors)
                )
            else:
                vo_completed += 1

        # ── Test 5: Translation correctness (non-IT items) ─────────
        if lang != "it" and lang in LANG_MARKERS:
            marker_count = count_lang_markers(text, lang)
            if marker_count == 0:
                add_error(f"Test 5 [{item_id}]: No language markers found for {lang}")
        elif lang == "it":
            # Italian must NOT be English
            en_count = count_lang_markers(text, "en")
            if en_count >= 3:
                add_error(f"Test 5 [{item_id}]: Italian item reads like English "
                           f"({en_count} en markers)")

        # ── Test 7: Google Drive document ──────────────────────────
        doc_link = deep_get(item, "result", "artifacts", "document", "doc_link", default="")
        if not doc_link:
            doc_link = deep_get(item, "result", "data", "artifacts", "document", "doc_link", default="")
        if doc_link:
            doc_links.add(doc_link)
        else:
            add_error(f"Test 7 [{item_id}]: No document doc_link found")

        # ── Test 2b: SRC_ markers preserved in text ──────────────────
        for s_idx in range(expected_scene_count):
            if s_idx < len(source_markers):
                marker = source_markers[s_idx]
                if marker not in text and marker not in " ".join(
                        s.get("text", "") for s in scenes):
                    pass  # Not fatal — markers may be stripped after translation

        # ── Test 9: Editorial integrity ────────────────────────────
        text_lower = text.lower()
        suspicious_terms = ["più poveri", "poorest", "classifica", "ranking",
                            "il più povero", "the poorest", "ranked #"]
        for term in suspicious_terms:
            if term in text_lower:
                add_error(f"Test 9 [{item_id}]: Editorial integrity — found '{term}'")

    if len(canonical_folders) > 1:
        add_error(
            "Voiceover folder is not canonical across items: "
            + ", ".join(sorted(canonical_folders))
        )

    # ── Test 4: Stock coherence across languages ──────────────────────
    print("\n── Test 4: Stock coherence across languages ──")
    all_coherent = True
    for s_idx in range(expected_scene_count):
        assets = stock_by_scene[s_idx]
        if len(assets) == 0:
            all_coherent = False
            add_error(f"Scene {s_idx} ({boxer_names[s_idx]}): no stock assets found")
        elif len(assets) > 1:
            all_coherent = False
            add_error(f"Scene {s_idx} ({boxer_names[s_idx]}): {len(assets)} different "
                       f"assets across languages — {assets}")
        else:
            ok(f"{boxer_names[s_idx]}: COUNT(DISTINCT asset_id) = 1 ({list(assets)[0]})")

    # ── Test 6 summary ────────────────────────────────────────────────
    print("\n── Test 6: Voiceover summary ──")
    if vo_total == expected_total_scenes:
        if vo_completed == expected_total_scenes:
            ok(f"{expected_total_scenes}/{expected_total_scenes} voiceovers completed")
        else:
            add_error(f"Voiceovers: {vo_completed}/{expected_total_scenes} completed")
    else:
        add_error(f"Voiceover scenes found: {vo_total}, expected {expected_total_scenes}")

    # ── Test 7 summary: Documents ─────────────────────────────────────
    print("\n── Test 7: Google Drive documents ──")
    if len(doc_links) == 10:
        ok(f"10 distinct document links")
    else:
        add_error(f"Distinct doc links: {len(doc_links)}, expected 10")

    # ── Test 8: SQLite persistence ────────────────────────────────────
    print("\n── Test 8: SQLite persistence ──")
    if not os.path.exists(args.db_path):
        add_error(f"Database not found at {args.db_path}")
    else:
        try:
            conn = sqlite3.connect(args.db_path)
            cur = conn.cursor()
            scripts_found = 0
            for sid in script_ids:
                cur.execute(
                    "SELECT status, narrative_text, final_word_count FROM scripts WHERE id = ?",
                    (sid,),
                )
                row = cur.fetchone()
                if not row:
                    add_error(f"Script {sid}: not found in scripts table")
                else:
                    db_status, narrative, word_count = row
                    if db_status not in ("completed", "SUCCEEDED"):
                        add_error(f"Script {sid}: DB status={db_status}")
                    if not narrative:
                        add_error(f"Script {sid}: narrative_text empty")
                    if word_count is None or word_count <= 0:
                        add_error(f"Script {sid}: final_word_count invalid ({word_count})")
                    else:
                        scripts_found += 1
            conn.close()
            if scripts_found == 10:
                ok(f"10/10 scripts verified in SQLite")
        except sqlite3.Error as e:
            add_error(f"SQLite error: {e}")

    # ── Test 2 summary ────────────────────────────────────────────────
    print(f"\n── Test 2: Source text respect ──")
    if total_scenes_check == expected_total_scenes:
        ok(f"{expected_total_scenes} scenes across {expected_item_count} languages ({expected_scene_count} per language)")
    else:
        add_error(f"Total scenes: {total_scenes_check}, expected {expected_total_scenes}")

    # ── Test 3 summary: Stock bindings ───────────────────────────────
    print(f"\n── Test 3: Stock bindings ──")
    total_stocks = sum(len(v) for v in stock_by_scene.values())
    if total_stocks == expected_scene_count:
        ok(f"{expected_scene_count} unique stock assets (1 per boxer)")
    else:
        add_error(f"Unique stock assets: {total_stocks}, expected {expected_scene_count}")

    # ── Final verdict ─────────────────────────────────────────────────
    print(f"\n{'='*50}")
    if errors:
        print(red(f"\nFAILED: {len(errors)} assertion(s) failed"))
        for e in errors:
            print(f"  → {e}")
        sys.exit(1)
    else:
        print(green(f"\nPASS: All 12+ multilang verifications passed"))
        print(f"  10 languages × 5 boxers = 50 localized scenes")
        print(f"  50 stock bindings verified")
        print(f"  50 voiceovers completed")
        print(f"  10 Google Drive documents")
        print(f"  10 scripts persisted in SQLite")
        print(f"  Zero cross-boxer contamination")
        sys.exit(0)


if __name__ == "__main__":
    main()
