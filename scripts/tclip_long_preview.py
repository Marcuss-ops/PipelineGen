#!/usr/bin/env python3
"""Print the transcript text of the planned 10-minute windows (review helper)."""
from __future__ import annotations

import pathlib
import re
import sys

D = pathlib.Path("data/tclip/persons/vannacci")
W = {
    "WvDLMyYZW9A": [(0, 600), (1080, 1680), (3840, 4440), (4440, 5040)],
    "Sh7RIPIVhnA": [(0, 600), (1080, 1680)],
    "1yZ8qsfPgiY": [(0, 600), (600, 1200)],
    "8gv34dcRtyw": [(0, 600), (600, 1200)],
    "iRceplkQe2s": [(300, 900), (900, 1500)],
    "ecN7BXbw_VQ": [(0, 600), (600, 1200)],
    "dlzbmV1nfGQ": [(0, 600), (720, 1320)],
    "8IJBRlAJTW4": [(660, 1260), (1260, 1860)],
    "oMNPlX0RYDU": [(0, 600), (600, 1200), (1200, 1800)],
    "DPjFqKlQecQ": [(0, 600), (1080, 1680)],
    "13KIAWf4B_8": [(0, 600), (600, 1200), (2550, 3150)],
}


def parse(vid: str):
    rows = []
    for ln in (D / f"{vid}.chunks60.txt").read_text(errors="ignore").splitlines():
        m = re.match(r"\[(\d\d):(\d\d):(\d\d)-", ln)
        if not m:
            continue
        t = int(m.group(1)) * 3600 + int(m.group(2)) * 60 + int(m.group(3))
        rows.append((t, ln[ln.index("]") + 2:].strip()))
    return rows


def main(vids: list[str]) -> None:
    for vid in vids:
        rows = parse(vid)
        print(f"\n########## {vid}")
        for a, b in W[vid]:
            txt = " ".join(t for ts, t in rows if a <= ts < b)
            print(f"\n--- [{a}-{b}] chars={len(txt)}")
            print(txt[:450])
            if len(txt) > 450:
                print("   ...TAIL:", txt[-250:])


if __name__ == "__main__":
    main(sys.argv[1:] or list(W))
