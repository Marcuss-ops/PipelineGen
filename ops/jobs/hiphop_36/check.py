#!/usr/bin/env python3
"""HipHop 36 — payload validator (deterministic, no LLM).

Checks every <slug>/process.json produced by hand against the wire
contract of POST /api/clips/process (dto.ExtractRequest):

  * url is a youtube watch url, destination.folder_id is the artist
    folder and create_subfolder is false
  * every segment: MM:SS timestamps parse, 4s <= duration <= 60s,
    main clips <= 60s, hook clips <= 15s and INSIDE the matching main
    window, no duplicate (start,end) pair -> no clip-id collision
  * dense metadata present: name/summary/hook/topics/speakers +
    texts[0] with title/summary/description in it

Exit code 1 when any payload fails, so it can gate the submit step.

Usage:
  check.py [slug ...]        # default: every directory with process.json
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent

# Artist folder -> folder id (verified live with cmd/admin drive-ls on 1wES95cH_RVYxl5I3kxnppbv-AlymCRqG).
FOLDERS = {
    "abba": "1BYKuBGYaeTe37n70gFbE_Fn-CM23mKpV",
    "alanis_morissette": "1caCIxFhn24q1MJySB1Dzybq71-0wBsGA",
    "aretha_franklin": "1IrydTfYW8OoMofD3XJKiEfXZnGHhmsx",
    "backstreet_boys": "1Q9JyIC4anqf_Oa6stZQ7AdX2gf84yKbe",
    "barry_white": "1PjhPMm8fzahbp7yVo0T4qXUeB8m4n7yZ",
    "bee_gees": "1sBf3_MviwHkdoJTUo3Um32I-lITT9qCW",
    "bob_marley": "15cyx1VsJ428eEuw4k7a6ruWwpvuSE359",
    "bon_jovi": "1WHij1cIrMFiYFOyCurSNG25ZBISy0DTM",
    "bryan_adams": "1SZ91zretBH2pB139yM_8DQqb_xgoMJi8",
    "celine_dion": "187yn6YywuATzzHBikHV_QVtXK61D9W1Y",
    "cher": "15xs9_Br44n9Qy-ZcTz0A1qOHnUSD7i-T",
    "cyndi_lauper": "1m-z83yQXp5UJuP2dBPPg_DNzJffRMn0v",
    "david_bowie": "1omBF1Nn_291b_VGu5824Lg2r0WiB46XQ",
    "diana_ross": "1nHzvgsiEh5k4BWkldpMEkb9GDpXKGPqQ",
    "donna_summer": "1KWie6iy4_G0n9z5EQhbqJ4czkP-cLxc0",
    "elton_john": "1-tRyH-hjLnfiemmxFkv8Ehnn5Kb7jnCP",
    "elvis_presley": "1xO5ECk6uKgGkx_GBV86EX2TfzEUUMD8t",
    "frank_sinatra": "1SOlfDdT26lefbrUIrDZOZvgdEGjVBVZ9",
    "freddie_mercury": "1XjmETXySwb3V6aRLvRUyVb5EKeh0YxFq",
    "george_michael": "19CWgvlV1IPbfeAwGQSnIBFv01ysRpp1T",
    "gloria_gaynor": "1Z3Z-Jx49VlOLOQUoL0MD-1j_1zRAmzeZ",
    "janet_jackson": "1DnfxXPkOkILweTeFIxj7WQeggKSvUDmE",
    "jimi_hendrix": "1Jh3nViXfIXIbKNNJ7TTERPUM14M7nLyK",
    "john_lennon": "1AgzygU5-XqS4Z_pHHfgEVoTWKbfCGySL",
    "lionel_richie": "1sQbmZhinwUP92jPlouwvEpqCQzkqdjU0",
    "madonna": "144ki_Hn4yOAOuF3P3fMvXR6-GE6R7jSL",
    "mariah_carey": "1p4D-unVJeK4NdSTdGjEnWvpF9trze-xM",
    "paul_mccartney": "1eLLHXrb7g89zwDt9YQdgpDdkAyvnlBGO",
    "phil_collins": "1WPYx1_VF10rCfodVdazDYGsriJeu0ebB",
    "queen": "1K-twA8EZ9_xs9-xTB-rtjU7e_X_WxJnO",
    "rod_stewart": "1GNjncBBkOhTX2vQ7txu7NDrR3jFJNFOk",
    "spice_girls": "1XO81gpUrzyxllKLGIcKiXClNCwbn1Xu3",
    "stevie_wonder": "1gWBXP4xjRerl2au_HKjlszwOFkldq_Ls",
    "the_beatles": "1vkUb0FRz8mK-6cNpoNQ0wtKABeNLS_46",
    "the_rolling_stones": "1DQE0gBxuEUX2cBzY982pMdXtFUE5D8ri",
    "tina_turner": "1oPqpaj15gF_e0GjmacWWUEsi-ZLinwMw",
    "whitney_houston": "1JNHiylD-yLrYQiu_gs87tQB8bElI3h6m",
    "prince": "1jfB96z8Mcgx1R8f6MyDN4A9AqypEdVMi",
    "beyonce": "1x0iBNB6jmfGCTO36zBnlyFBeqLM_eBk_",
    "rihanna": "1u1rYpWeTkcedezM2f-lX6FTrq2hiIZJL",
    "the_weeknd": "19rnS2EiF9RfSFRaFXDfuJhlMvFgSiRtl",
    "justin_timberlake": "1EBVkSLSLDeoVZTDRybsJ14wsHTTDHHse",
    "michael_jackson": "1QMbexs6jFky-NjgVNgq_muMm9CixB_YC",
}


def ts_to_sec(value: str) -> int:
    parts = [int(p) for p in str(value).split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def check(slug: str, path: Path) -> list[str]:
    errs: list[str] = []
    try:
        doc = json.loads(path.read_text())
    except Exception as exc:  # noqa: BLE001
        return [f"{slug}: invalid JSON: {exc}"]

    dest = doc.get("destination") or {}
    if dest.get("folder_id") != FOLDERS.get(slug):
        errs.append(f"{slug}: destination.folder_id={dest.get('folder_id')!r} != {FOLDERS.get(slug)!r}")
    if dest.get("create_subfolder") is not False:
        errs.append(f"{slug}: create_subfolder must be false")
    for legacy in ("group", "folder_id", "folder_path", "subfolder_name", "create_subfolder"):
        if legacy in doc:
            errs.append(f"{slug}: legacy top-level {legacy!r} would be rejected with 400")
    if not re.match(r"^https://www\.youtube\.com/watch\?v=[A-Za-z0-9_-]{6,}$", str(doc.get("url", ""))):
        errs.append(f"{slug}: url is not a youtube watch url: {doc.get('url')!r}")
    if doc.get("strategy") != "verify":
        errs.append(f"{slug}: strategy should be 'verify' (got {doc.get('strategy')!r})")

    segments = doc.get("segments") or []
    if not segments:
        errs.append(f"{slug}: no segments")
    mains = [s for s in segments if "hook" not in [t.lower() for t in s.get("tags", [])]]
    hooks = [s for s in segments if s not in mains]
    if len(mains) < 4:
        errs.append(f"{slug}: only {len(mains)} main clips (want >= 4)")

    seen: dict[tuple[int, int], str] = {}
    for seg in segments:
        name = str(seg.get("name", "?"))[:48]
        try:
            start, end = ts_to_sec(seg["start"]), ts_to_sec(seg["end"])
        except Exception:  # noqa: BLE001
            errs.append(f"{slug}/{name}: bad timestamps {seg.get('start')!r}-{seg.get('end')!r}")
            continue
        dur = end - start
        is_hook = seg in hooks
        if is_hook and dur > 15:
            errs.append(f"{slug}/{name}: hook is {dur}s (>15s)")
        if not is_hook and not 4 <= dur <= 60:
            errs.append(f"{slug}/{name}: main duration {dur}s outside 4-60s")
        if dur < 4:
            errs.append(f"{slug}/{name}: duration {dur}s below the 4s SegmentPolicy floor")
        key = (start, end)
        if key in seen:
            errs.append(f"{slug}: duplicate window {seg['start']}-{seg['end']} ({seen[key]} / {name}) -> clip id collision")
        seen[key] = name
        for field in ("name", "summary", "hook"):
            if len(str(seg.get(field, "")).strip()) < 15:
                errs.append(f"{slug}/{name}: field {field!r} too thin for search_text")
        topics = seg.get("topics") or []
        dense = " ".join(topics).strip()
        # Hook openers are short by design; their search_text also carries the
        # hook sentence, so the density floor is lower there.
        if len(dense) < (100 if is_hook else 120):
            errs.append(f"{slug}/{name}: topics too thin ({len(dense)} chars) for a dense embedding")
        if not seg.get("speakers"):
            errs.append(f"{slug}/{name}: speakers empty")
        texts = seg.get("texts") or []
        if not texts or not texts[0].get("title") or not texts[0].get("summary"):
            errs.append(f"{slug}/{name}: texts[0] needs title+summary (indexed as asset_text_tracks)")
        if is_hook:
            inside = any(
                ts_to_sec(m["start"]) <= start and end <= ts_to_sec(m["end"]) for m in mains
            )
            if not inside:
                errs.append(f"{slug}/{name}: hook window {seg['start']}-{seg['end']} is not inside any main clip")
    for seg in mains:
        tags = [t.lower() for t in seg.get("tags", [])]
        if "celebrity_interview" not in tags:
            errs.append(f"{slug}/{str(seg.get('name'))[:40]}: missing category tag")
    return errs


def main() -> int:
    slugs = sys.argv[1:] or sorted(p.name for p in HERE.iterdir() if (p / "process.json").exists())
    all_errs: list[str] = []
    for slug in slugs:
        path = HERE / slug / "process.json"
        if not path.exists():
            all_errs.append(f"{slug}: {path} missing")
            continue
        errs = check(slug, path)
        doc = json.loads(path.read_text())
        segs = doc.get("segments", [])
        mains = [s for s in segs if "hook" not in [t.lower() for t in s.get("tags", [])]]
        hooks = [s for s in segs if s not in mains]
        durs = [ts_to_sec(s["end"]) - ts_to_sec(s["start"]) for s in mains]
        status = "OK " if not errs else "FAIL"
        print(f"{status} {slug:<22} mains={len(mains)} hooks={len(hooks)} "
              f"main_dur={min(durs) if durs else 0}-{max(durs) if durs else 0}s")
        all_errs.extend(errs)
    for err in all_errs:
        print(f"  ! {err}")
    print(f"\n{len(all_errs)} problem(s)")
    return 1 if all_errs else 0


if __name__ == "__main__":
    raise SystemExit(main())
