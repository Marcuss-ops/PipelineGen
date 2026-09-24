#!/usr/bin/env python3
"""Cache full cue lists of one or more YouTube videos to data/tclip/transcripts/<vid>.json."""
from __future__ import annotations
import json, sys, urllib.parse, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "data/tclip/transcripts"
BASE = "http://127.0.0.1:8000"


def token() -> str:
    env = ROOT / ".env"
    for line in env.read_text(errors="ignore").splitlines():
        if line.strip().startswith("VELOX_ADMIN_TOKEN="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def sec(s: str) -> int:
    p = [int(x) for x in s.split(":")]
    return p[0] * 3600 + p[1] * 60 + p[2] if len(p) == 3 else p[0] * 60 + p[1]


def fetch(vid: str, a: int = 0, b: int = 0):
    q = urllib.parse.urlencode({"url": f"https://www.youtube.com/watch?v={vid}",
                                "start": a, "end": b})
    req = urllib.request.Request(f"{BASE}/api/clips/transcript?{q}",
                                 headers={"X-Velox-Admin-Token": token()})
    with urllib.request.urlopen(req, timeout=180) as r:
        return json.load(r)


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    raws = sys.argv[1:]
    ok, bad = [], []
    for vid in raws:
        try:
            body = fetch(vid)
            cues = body.get("cues") or body.get("segments") or []
            if not cues:
                bad.append((vid, "no cues: " + json.dumps(body)[:160]))
                continue
            (OUT / f"{vid}.json").write_text(json.dumps(cues, ensure_ascii=False))
            ok.append((vid, len(cues)))
        except Exception as e:  # noqa: BLE001
            bad.append((vid, f"{type(e).__name__}: {e}"))
    for vid, n in ok:
        print(f"OK   {vid} cues={n}")
    for vid, why in bad:
        print(f"FAIL {vid} {why}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
