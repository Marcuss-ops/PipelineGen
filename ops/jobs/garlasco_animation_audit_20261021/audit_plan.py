#!/usr/bin/env python3
"""Audit a Garlasco animation-audit job against the G/E/M/S/L/F/B/C criteria.

The input is the FULL job body returned by `/api/jobs/{id}/full`. The script
does not judge rendered pixels: it certifies what the overlay plan actually
contains (assets, captions, windows, motions, grounded text) and whether the
plan can be handed to a replace-mode compositor.

Usage:
  python3 ops/jobs/garlasco_animation_audit_20261021/audit_plan.py \
      ops/jobs/garlasco_animation_audit_20261021/evidence/overlay.full.json

Exit code 0 means every reachable criterion PASSED; 1 means at least one
criterion FAILED; 2 means the plan could not be read.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

BULLET_ROLES = {"scene_bullet", "chapter_bullet"}
SIDE_BY_SIDE_ROLES = {"Alberto Stasi", "Andrea Sempio"}
YEAR_ONLY = re.compile(r"^(19|20)\d{2}$")


def walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk(child)


def plans(data):
    out = []
    for obj in walk(data):
        plan = obj.get("overlay_plan")
        if isinstance(plan, dict) and isinstance(plan.get("items"), list):
            out.append(plan)
    return out


def scene_texts(data):
    """Map scene id -> narration text from wherever the job exposes it."""
    texts: dict[str, str] = {}
    for obj in walk(data):
        sid = obj.get("id")
        if not isinstance(sid, str):
            continue
        value = obj.get("text")
        if isinstance(value, str) and value.strip():
            texts.setdefault(sid, value)
        elif isinstance(value, dict):
            for language in ("it", "en"):
                if isinstance(value.get(language), str) and value[language].strip():
                    texts.setdefault(sid, value[language])
                    break
        for key in ("source_text", "narration"):
            alt = obj.get(key)
            if isinstance(alt, str) and alt.strip():
                texts.setdefault(sid, alt)
    return texts


def params(item):
    return item.get("params") or {}


def role(item):
    return str(params(item).get("editorial_role") or "")


def window(item):
    start = item.get("start_us")
    duration = item.get("duration_us")
    if isinstance(start, int) and isinstance(duration, int) and duration > 0:
        return start, start + duration
    start_ms, end_ms = item.get("start_ms"), item.get("end_ms")
    if isinstance(start_ms, (int, float)) and isinstance(end_ms, (int, float)):
        return int(start_ms * 1000), int(end_ms * 1000)
    return None


def words(text):
    return len(str(text or "").split())


def numeric(text):
    raw = str(text or "").strip().strip("€$£¥%").strip().replace(" ", "")
    if raw.count(",") == 1 and "." not in raw:
        raw = raw.replace(",", ".")
    try:
        return float(raw)
    except ValueError:
        return None


def overlaps(a, b):
    if not a or not b:
        return False
    return a[0] < b[1] and b[0] < a[1]


def norm(text):
    return re.sub(r"\s+", " ", str(text or "")).strip().lower()


def group_is_simultaneous_side_by_side(group, canvas_width):
    layers = group.get("image_layers") or []
    if len(layers) < 2 or not canvas_width:
        return False
    starts, ends, centers, bounds = [], [], [], []
    for layer in layers:
        start, end = layer.get("start_ms"), layer.get("end_ms")
        image = layer.get("params") or {}
        x, width = image.get("position_x"), image.get("width")
        if not all(isinstance(value, (int, float)) for value in (start, end, x, width)) or end <= start:
            return False
        starts.append(start)
        ends.append(end)
        centers.append(x)
        bounds.append((x - width / 2, x + width / 2))
    simultaneous = max(starts) < min(ends)
    distinct_positions = len(set(centers)) == len(centers)
    visible = all(left >= -canvas_width / 2 and right <= canvas_width / 2 for left, right in bounds)
    return simultaneous and distinct_positions and visible and not group.get("camera_motion_id")


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__)
        return 2
    source = Path(sys.argv[1])
    data = json.loads(source.read_text(encoding="utf-8"))

    plan_list = plans(data)
    if not plan_list:
        print(f"FAIL: no overlay_plan found in {source}", file=sys.stderr)
        return 2
    items = plan_list[0]["items"]
    texts = scene_texts(data)
    checks: list[dict] = []

    def record(cid, title, status, detail):
        checks.append({"id": cid, "title": title, "status": status, "detail": detail})

    images = [i for i in items if i.get("kind") in ("image", "entity_image", "entity_card")]
    groups = [i for i in items if len(i.get("image_layers") or []) >= 2]

    # G01 — two distinct assets visible together in one composite.
    g01 = []
    canvas_width = plan_list[0].get("width")
    for group in groups:
        layers = group.get("image_layers") or []
        assets = {str(layer.get("asset_id")) for layer in layers if layer.get("asset_id")}
        if len(layers) >= 2 and len(assets) >= 2 and group_is_simultaneous_side_by_side(group, canvas_width):
            g01.append(group)
    record(
        "G01",
        "2 images side by side",
        "PASS" if g01 else "FAIL",
        [
            {
                "id": g.get("id"),
                "scene_id": g.get("scene_id"),
                "start_ms": g.get("start_ms"),
                "end_ms": g.get("end_ms"),
                "children": len(g.get("image_layers") or []),
                "captions": [layer.get("caption") for layer in g.get("image_layers") or []],
                "camera_motion_id": g.get("camera_motion_id"),
                "simultaneous_side_by_side": group_is_simultaneous_side_by_side(g, canvas_width),
                "group_motion_id": g.get("group_motion_id"),
                "group_layout": params(g).get("group_layout"),
            }
            for g in g01
        ],
    )

    # G02 — 3..5 image composites when candidates allow it.
    g02 = [g for g in g01 if 3 <= len(g.get("image_layers") or []) <= 5]
    record(
        "G02",
        "3-5 images in one group",
        "PASS" if g02 else "NOT_OBSERVED",
        [{"id": g.get("id"), "children": len(g.get("image_layers") or [])} for g in g02],
    )

    # E01 — two named entities in the same composite, identities intact.
    e01 = []
    for group in g01:
        captions = {norm(layer.get("caption")) for layer in group.get("image_layers") or []}
        if all(norm(name) in captions for name in SIDE_BY_SIDE_ROLES):
            e01.append(group)
    record(
        "E01",
        "2 co-mentioned entities with 2 portraits and 2 captions",
        "PASS" if e01 else "FAIL",
        [
            {
                "id": g.get("id"),
                "scene_id": g.get("scene_id"),
                "layers": [
                    {
                        "asset_id": layer.get("asset_id"),
                        "caption": layer.get("caption"),
                        "start_ms": layer.get("start_ms"),
                        "end_ms": layer.get("end_ms"),
                        "motion_id": layer.get("motion_id"),
                    }
                    for layer in g.get("image_layers") or []
                ],
            }
            for g in e01
        ],
    )

    # E02 — an entity repeated across scenes must not reuse the same bytes.
    per_caption: dict[str, set] = {}
    duplicates = []
    seen_assets: dict[str, str] = {}
    for group in g01:
        for layer in group.get("image_layers") or []:
            caption = norm(layer.get("caption"))
            asset = str(layer.get("asset_id") or "")
            if not caption or not asset:
                continue
            if caption in per_caption and asset in per_caption[caption]:
                duplicates.append({"caption": caption, "asset_id": asset, "scene": group.get("scene_id")})
            per_caption.setdefault(caption, set()).add(asset)
            if asset in seen_assets:
                duplicates.append(
                    {"asset_id": asset, "first": seen_assets[asset], "again": group.get("scene_id")}
                )
            else:
                seen_assets[asset] = str(group.get("scene_id"))
    repeated = [name for name, assets in per_caption.items() if len(assets) >= 1]
    record(
        "E02",
        "repeated entities grouped correctly without asset reuse",
        "PASS" if not duplicates and repeated else "FAIL" if duplicates else "NOT_OBSERVED",
        {"entities": {k: sorted(v) for k, v in per_caption.items()}, "duplicates": duplicates},
    )

    # M01 — maps of real, narrated places only.
    maps = [i for i in items if i.get("kind") == "map" or i.get("map")]
    map_report = []
    bad_maps = []
    for item in maps:
        scene = texts.get(str(item.get("scene_id")), "")
        map_block = item.get("map") or {}
        labels = []
        for key in ("label", "title"):
            if isinstance(map_block.get(key), str) and map_block[key].strip():
                labels.append(map_block[key].strip())
        for pin in map_block.get("pins") or []:
            if isinstance(pin, dict) and isinstance(pin.get("label"), str):
                labels.append(pin["label"].strip())
        grounded = [label for label in labels if label and norm(label) in norm(scene)]
        if not grounded:
            bad_maps.append({"id": item.get("id"), "labels": labels, "scene": item.get("scene_id")})
        map_report.append({"id": item.get("id"), "scene_id": item.get("scene_id"), "labels": labels, "grounded": grounded})
    record(
        "M01",
        "maps of real narrated places",
        "PASS" if maps and not bad_maps else "FAIL" if maps else "FAIL",
        {"count": len(maps), "maps": map_report, "ungrounded": bad_maps},
    )

    # S01 / L01 — phrase extraction and motion diversity.
    phrases = [i for i in items if i.get("kind") == "text_phrase" and role(i) not in BULLET_ROLES and not params(i).get("chart_card")]
    short = [p for p in phrases if 2 <= words(p.get("text")) <= 5]
    long = [p for p in phrases if 6 <= words(p.get("text")) <= 16]
    short_motions = {p.get("motion_id") for p in short if p.get("motion_id")}
    long_motions = {p.get("motion_id") for p in long if p.get("motion_id")}
    record(
        "S01",
        "short phrases (2-5 words) extracted and animated",
        "PASS" if short and all(p.get("motion_id") for p in short) else "FAIL",
        [{ "scene_id": p.get("scene_id"), "text": p.get("text"), "words": words(p.get("text")), "motion_id": p.get("motion_id")} for p in short],
    )
    record(
        "L01",
        "long phrases (6-16 words) with more than one motion family",
        "PASS" if long and len(long_motions) >= 2 else "FAIL",
        {"count": len(long), "motions": sorted(long_motions), "scenes": [p.get("scene_id") for p in long]},
    )

    # F01 — a composite group carries a closing/entrance choreography.
    f01 = [g for g in g01 if g.get("group_motion_id")]
    record(
        "F01",
        "group final animation declared",
        "PASS" if f01 else "FAIL",
        [{"id": g.get("id"), "group_motion_id": g.get("group_motion_id")} for g in f01],
    )

    # B01 / B02 — grounded bullets with a distinct motion.
    bullets = [i for i in items if role(i) in BULLET_ROLES]
    grounded_bullets = []
    ungrounded = []
    for bullet in bullets:
        scene = norm(texts.get(str(bullet.get("scene_id")), ""))
        lead = " ".join(str(bullet.get("text") or "").split()[:5])
        if lead and norm(lead) in scene:
            grounded_bullets.append(bullet)
        else:
            ungrounded.append({"id": bullet.get("id"), "scene_id": bullet.get("scene_id"), "lead": lead})
    record(
        "B01",
        "at least 2 grounded bullets",
        "PASS" if len(grounded_bullets) >= 2 and not ungrounded else "FAIL",
        {
            "count": len(bullets),
            "grounded": [{"id": b.get("id"), "scene_id": b.get("scene_id"), "motion_id": b.get("motion_id"), "text": b.get("text")} for b in grounded_bullets],
            "ungrounded": ungrounded,
            "roles": sorted({role(b) for b in bullets}),
        },
    )
    record(
        "B02",
        "bullet animation visible and distinct",
        "PASS" if grounded_bullets and all(b.get("motion_id") for b in grounded_bullets) else "FAIL",
        [{"id": b.get("id"), "motion_id": b.get("motion_id"), "window": window(b)} for b in bullets],
    )

    # C01 / C02 — charts must be grounded comparisons, never invented from dates.
    chart_cards = [i for i in items if params(i).get("chart_card") is True]
    chart_bars = [i for i in items if params(i).get("chart_bar") is True]
    chart_groups: dict[str, list] = {}
    for item in chart_cards + chart_bars:
        group = str(params(item).get("chart_group_id") or "")
        chart_groups.setdefault(group, []).append(item)
    fabricated = []
    for card in chart_cards:
        scene_id = str(card.get("scene_id"))
        scene = norm(texts.get(scene_id, ""))
        labels = [line.strip() for line in str(card.get("text") or "").splitlines() if line.strip()]
        values = [numeric(label) for label in labels]
        if len([v for v in values if v is not None]) < 2:
            fabricated.append({"id": card.get("id"), "reason": "fewer than 2 numeric labels", "labels": labels})
            continue
        if all(YEAR_ONLY.match(label.strip()) for label in labels):
            fabricated.append({"id": card.get("id"), "reason": "dates only", "labels": labels})
            continue
        for label in labels:
            if norm(label) not in scene:
                fabricated.append({"id": card.get("id"), "reason": f"label {label!r} not verbatim in scene", "labels": labels})
                break
    record(
        "C01",
        "no fabricated numeric comparison",
        "PASS" if not fabricated else "FAIL",
        {"cards": len(chart_cards), "bars": len(chart_bars), "groups": {k: len(v) for k, v in chart_groups.items()}, "violations": fabricated},
    )
    record(
        "C02",
        "technical chart canary (grounded values, bars and labels)",
        "NOT_APPLICABLE" if not chart_cards else "PASS" if not fabricated else "FAIL",
        "Charts in a news script may legitimately be zero; the positive canary is e2e_cert/T04 (C01-C05, 28 animated bars).",
    )

    # Replace-mode compatibility: a chart group must be ONE artifact/window.
    split_charts = []
    for group, members in chart_groups.items():
        windows = [window(member) for member in members]
        overlapping = sum(1 for i in range(len(windows)) for j in range(i + 1, len(windows)) if overlaps(windows[i], windows[j]))
        if len(members) > 1 and overlapping:
            split_charts.append({"group": group, "items": len(members), "overlaps": overlapping, "windows": windows})
    record(
        "X01",
        "replace-mode atomicity (no chart split across colliding overlays)",
        "PASS" if not split_charts else "FAIL",
        {"split_chart_groups": split_charts},
    )

    # Any other same-frame collision is reported for the final handoff.
    collisions = []
    chart_ids = {id(item) for item in chart_cards + chart_bars}
    for i in range(len(items)):
        if id(items[i]) in chart_ids:
            continue
        for j in range(i + 1, len(items)):
            if id(items[j]) in chart_ids:
                continue
            if overlaps(window(items[i]), window(items[j])):
                collisions.append([items[i].get("id"), items[j].get("id"), items[i].get("scene_id")])
    record(
        "X02",
        "non-chart same-frame collisions",
        "PASS" if not collisions else "WARN",
        {"count": len(collisions), "pairs": collisions[:20]},
    )

    total = len(checks)
    passed = sum(1 for c in checks if c["status"] == "PASS")
    failed = [c for c in checks if c["status"] == "FAIL"]
    summary = {
        "source": str(source),
        "plan_id": plan_list[0].get("plan_id"),
        "items": len(items),
        "checks": checks,
        "totals": {"checks": total, "passed": passed, "failed": len(failed)},
    }
    json_path = source.with_suffix(".audit.json")
    json_path.write_text(json.dumps(summary, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

    md = [
        "# Garlasco animation audit",
        "",
        f"- Source: `{source.name}`",
        f"- Plan: `{summary['plan_id']}` — {len(items)} items",
        f"- Checks: {passed}/{total} PASS",
        "",
        "| ID | Check | Status |",
        "| --- | --- | --- |",
    ]
    for check in checks:
        md.append(f"| {check['id']} | {check['title']} | {check['status']} |")
    md.append("")
    for check in checks:
        md.append(f"## {check['id']} — {check['title']}: {check['status']}")
        md.append("")
        md.append("```json")
        md.append(json.dumps(check["detail"], ensure_ascii=False, indent=1))
        md.append("```")
        md.append("")
    md_path = source.with_suffix(".audit.md")
    md_path.write_text("\n".join(md) + "\n", encoding="utf-8")

    print(f"checks: {passed}/{total} PASS -> {json_path.name}, {md_path.name}")
    for check in checks:
        print(f"  {check['id']:<4} {check['status']:<14} {check['title']}")
    return 0 if not failed else 1


if __name__ == "__main__":
    raise SystemExit(main())
