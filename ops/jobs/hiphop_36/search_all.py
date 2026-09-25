#!/usr/bin/env python3
"""HipHop 36 — candidate discovery (deterministic, no LLM).

Runs one /api/clips/search per artist (query '<artist> interview'),
prints a compact shortlist and caches the full JSON so the choice can
be re-inspected without burning another search. Paces the calls and
retries on timeout/429 with a growing backoff, because the YouTube
backed search is slow (~15-20s) and occasionally stalls.

Usage:
  search_all.py                       # default artist list
  search_all.py "Frank Sinatra" ...   # explicit list
"""

from __future__ import annotations

import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
BASE = "http://127.0.0.1:8000"
CACHE = HERE / "cache" / "search"

DEFAULT = [
    "Frank Sinatra",
    "George Michael",
    "Gloria Gaynor",
    "Janet Jackson",
    "Jimi Hendrix",
    "John Lennon",
    "Lionel Richie",
    "Mariah Carey",
    "Paul McCartney",
    "Phil Collins",
    "Rod Stewart",
    "Spice Girls",
    "Stevie Wonder",
    "The Beatles",
    "The Rolling Stones",
    "Tina Turner",
]

SLUG = {
    "Frank Sinatra": "frank_sinatra",
    "George Michael": "george_michael",
    "Gloria Gaynor": "gloria_gaynor",
    "Janet Jackson": "janet_jackson",
    "Jimi Hendrix": "jimi_hendrix",
    "John Lennon": "john_lennon",
    "Lionel Richie": "lionel_richie",
    "Mariah Carey": "mariah_carey",
    "Paul McCartney": "paul_mccartney",
    "Phil Collins": "phil_collins",
    "Rod Stewart": "rod_stewart",
    "Spice Girls": "spice_girls",
    "Stevie Wonder": "stevie_wonder",
    "The Beatles": "the_beatles",
    "The Rolling Stones": "the_rolling_stones",
    "Tina Turner": "tina_turner",
}


def token() -> str:
    for line in (REPO / ".env").read_text().splitlines():
        if line.startswith("VELOX_ADMIN_TOKEN="):
            return line.split("=", 1)[1].strip()
    sys.exit("token not found")


def search(query: str, attempts: int = 3) -> dict:
    url = f"{BASE}/api/clips/search?{urllib.parse.urlencode({'q': query, 'limit': 12})}"
    wait = 20
    for attempt in range(1, attempts + 1):
        req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token()}"})
        try:
            with urllib.request.urlopen(req, timeout=90) as resp:
                return json.loads(resp.read().decode())
        except Exception as exc:  # noqa: BLE001
            print(f"  ! attempt {attempt}/{attempts} failed: {type(exc).__name__}: {exc}", flush=True)
            if attempt < attempts:
                time.sleep(wait)
                wait *= 2
    return {}


def main() -> int:
    artists = sys.argv[1:] or DEFAULT
    CACHE.mkdir(parents=True, exist_ok=True)
    for artist in artists:
        slug = SLUG.get(artist, artist.lower().replace(" ", "_"))
        print(f"\n=== {artist} ({slug})", flush=True)
        data = search(f"{artist} interview")
        if not data.get("ok"):
            print("  SEARCH FAILED", flush=True)
            continue
        (CACHE / f"{slug}.json").write_text(json.dumps(data, ensure_ascii=False, indent=1))
        for r in data.get("results", []):
            print(
                f"  {r['video_id']} dur={r['duration']:>5}s caps={str(r.get('has_captions'))[:1]} "
                f"views={r.get('view_count', 0):>9} | {r['title'][:74]} | ch={r.get('channel_name', '')[:26]}",
                flush=True,
            )
        time.sleep(8)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
