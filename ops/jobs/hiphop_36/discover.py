#!/usr/bin/env python3
"""HipHop 36 — candidate discovery + transcript fetch (no LLM).

For each artist: one query against GET /api/clips/search, then a
heuristic pick (interview-ish title, 4-40 min, captions, English
preferred) and immediately a transcript dump to
cache/txt/<slug>_<video>.txt in the same `[HH:MM:SS] text` shape as the
transcripts the payloads were hand-picked from.

The ranking is deliberately simple and inspectable: the agent reads the
transcript and picks the windows by hand, this script only removes the
mechanical search/download step. Results are cached on disk, so a
re-run is free and a bad pick can be re-read.

Usage:
  discover.py diana_ross cher …       # explicit slugs (queries in QUERIES)
  discover.py --all                   # every QUERIES entry without a txt yet
  discover.py --pick <slug> <index>   # switch candidate for a slug
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tx  # noqa: E402

HERE = Path(__file__).resolve().parent
SEARCH = HERE / "cache" / "search"
TXT = HERE / "cache" / "txt"

INTERVIEW_HINTS = (
    "interview", "intervista", "talk", "show", "tonight", "letterman", "wogan",
    "parkinson", "carson", "donahue", "oprah", "arsenio", "conan", "live",
    "press conference", "conference", "q&a", "documentary", "special",
)
BAD_HINTS = (
    "reaction", "cover", "tribute band", "karaoke", "lyrics", "remix", "ai ",
    "interview about the death", "full album", "playlist", "shorts",
)

QUERIES: dict[str, str] = {
    "alanis_morissette": "Alanis Morissette interview Jagged Little Pill",
    "bob_marley": "Bob Marley interview 1977 1979",
    "celine_dion": "Celine Dion interview 1990s career",
    "cher": "Cher interview 1980s career",
    "donna_summer": "Donna Summer interview 1970s disco",
    "frank_sinatra": "Frank Sinatra interview 1970 show",
    "george_michael": "George Michael interview Wham solo career",
    "gloria_gaynor": "Gloria Gaynor interview I Will Survive",
    "janet_jackson": "Janet Jackson interview Rhythm Nation",
    "jimi_hendrix": "Jimi Hendrix interview 1969 1970",
    "john_lennon": "John Lennon interview 1971 Beatles",
    "lionel_richie": "Lionel Richie interview Commodores solo",
    "mariah_carey": "Mariah Carey interview 1990s debut",
    "paul_mccartney": "Paul McCartney interview Wings 1970s",
    "phil_collins": "Phil Collins interview Genesis solo 1980s",
    "rod_stewart": "Rod Stewart interview 1970s solo career",
    "spice_girls": "Spice Girls interview 1996 1997",
    "stevie_wonder": "Stevie Wonder interview 1970s Motown",
    "the_beatles": "Beatles interview 1964 press conference",
    "the_rolling_stones": "Rolling Stones interview 1965 1970s",
    "tina_turner": "Tina Turner interview 1980s comeback",
}


def search(slug: str) -> list[dict]:
    SEARCH.mkdir(parents=True, exist_ok=True)
    path = SEARCH / f"{slug}.json"
    if not path.exists():
        data = tx.get("/api/clips/search", {"q": QUERIES[slug], "limit": 12}, timeout=120)
        path.write_text(json.dumps(data, ensure_ascii=False, indent=1))
    return json.loads(path.read_text()).get("results", [])


def score(slug: str, r: dict) -> int:
    title = str(r.get("title") or "").lower()
    dur = int(r.get("duration") or 0)
    langs = [str(l).lower() for l in (r.get("caption_languages") or [])]
    s = 0
    if any(h in title for h in INTERVIEW_HINTS):
        s += 40
    if any(b in title for b in BAD_HINTS):
        s -= 80
    if "en" in langs:
        s += 25
    if r.get("has_captions"):
        s += 15
    if 300 <= dur <= 1500:
        s += 25
    elif 240 <= dur <= 2400:
        s += 10
    else:
        s -= 30
    words = [w for w in slug.split("_") if len(w) > 3]
    if any(w in title for w in words):
        s += 10
    if int(r.get("view_count") or 0) > 20000:
        s += 5
    return s


def fetch(slug: str, video_id: str, title: str) -> Path:
    TXT.mkdir(parents=True, exist_ok=True)
    out = TXT / f"{slug}_{video_id}.txt"
    if out.exists() and out.stat().st_size > 500:
        return out
    dur = tx.duration_of(video_id)
    data = tx.raw_cues(video_id, 0, dur)
    lines = tx.dedupe(data.get("cues", []))
    header = (
        f"# {video_id} | {title} | dur={dur}s | lang={data.get('language')} "
        f"| raw_cues={len(data.get('cues', []))} | lines={len(lines)}\n"
        f"# window 00:00:00 -> {tx.mmss(dur)}\n"
    )
    body = "\n".join(f"[{tx.mmss(c['start'] / 1000)}] {c['text']}" for c in lines)
    out.write_text(header + body + "\n")
    return out


def run(slug: str, index: int = 0) -> None:
    results = search(slug)
    if not results:
        print(f"{slug}: NO RESULTS")
        return
    ranked = sorted(results, key=lambda r: score(slug, r), reverse=True)
    print(f"{slug}: {len(ranked)} candidates")
    for rank, r in enumerate(ranked[:5]):
        mark = "->" if rank == index else "  "
        print(f" {mark} {r.get('video_id')} {int(r.get('duration') or 0):>5}s "
              f"caps={r.get('has_captions')} {str(r.get('title'))[:70]}")
    chosen = ranked[index] if index < len(ranked) else ranked[0]
    out = fetch(slug, chosen["video_id"], str(chosen.get("title")))
    print(f"   transcript: {out.relative_to(HERE)}  ({out.stat().st_size} bytes)")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("slugs", nargs="*")
    ap.add_argument("--all", action="store_true")
    ap.add_argument("--pick", nargs=2, metavar=("SLUG", "INDEX"))
    args = ap.parse_args()
    if args.pick:
        run(args.pick[0], int(args.pick[1]))
        return 0
    slugs = args.slugs
    if args.all or not slugs:
        slugs = [s for s in QUERIES if not any(TXT.glob(f"{s}_*.txt"))]
    for slug in slugs:
        try:
            run(slug)
        except Exception as exc:  # noqa: BLE001 — one bad artist must not stop the batch
            print(f"{slug}: ERROR {type(exc).__name__}: {exc}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
