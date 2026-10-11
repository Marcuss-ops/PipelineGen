#!/usr/bin/env python3
"""Chunk downloaded YouTube VTT captions into deduplicated timestamped blocks.

Operator helper (not production code): reads the Italian auto-caption VTT files
in /tmp/crepet_subs and emits, per video, a text file of `[MM:SS-MM:SS] words`
blocks. YouTube auto-captions roll (each cue repeats the previous line), so a
naive concatenation inflates the text ~2-3x; we drop the overlap the same way
scripts/tclip.py does.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

TAG = re.compile(r"<[^>]+>")
TS = re.compile(r"(\d{1,2}):(\d{2}):(\d{2})[.,](\d{1,3})")


def parse_vtt(path: Path):
    cues = []
    lines = path.read_text(errors="ignore").splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        if "-->" in line:
            m1 = TS.search(line.split("-->")[0])
            m2 = TS.search(line.split("-->")[1]) if "-->" in line else None
            if m1:
                start = int(m1.group(1)) * 3600 + int(m1.group(2)) * 60 + int(m1.group(3))
                start_ms = start * 1000 + int(m1.group(4).ljust(3, "0"))
                end_ms = start_ms
                if m2:
                    end = int(m2.group(1)) * 3600 + int(m2.group(2)) * 60 + int(m2.group(3))
                    end_ms = end * 1000 + int(m2.group(4).ljust(3, "0"))
                text_parts = []
                i += 1
                while i < len(lines) and lines[i].strip():
                    text_parts.append(TAG.sub("", lines[i]))
                    i += 1
                text = " ".join(text_parts).strip()
                if text:
                    cues.append((start_ms, end_ms, text))
        i += 1
    return cues


def dedup_append(acc, words, max_overlap=120):
    n = min(len(words), len(acc), max_overlap)
    for L in range(n, 0, -1):
        if acc[-L:] == words[:L]:
            acc.extend(words[L:])
            return
    acc.extend(words)


def hms(ms: int) -> str:
    s = max(0, ms // 1000)
    h, s = divmod(s, 3600)
    m, s = divmod(s, 60)
    return f"{h:02d}:{m:02d}:{s:02d}" if h else f"{m:02d}:{s:02d}"


def main() -> int:
    if len(sys.argv) < 4:
        print("usage: crepet_chunk_subs.py <vtt> <chunk_sec> <out.txt>", file=sys.stderr)
        return 2
    vtt, chunk_sec, out = Path(sys.argv[1]), int(sys.argv[2]), Path(sys.argv[3])
    cues = parse_vtt(vtt)
    if not cues:
        print(f"no cues parsed from {vtt}", file=sys.stderr)
        return 1
    chunk = chunk_sec * 1000
    lines = []
    cur_bucket = None
    acc: list[str] = []
    bstart = bpts = 0
    for start_ms, end_ms, text in cues:
        bucket = start_ms // chunk
        if cur_bucket is None:
            cur_bucket = bucket
            bstart = bpts = start_ms
        if bucket != cur_bucket:
            lines.append(f"[{hms(bstart)}-{hms(bpts)}] {' '.join(acc)}")
            acc = []
            cur_bucket = bucket
            bstart = bpts = start_ms
        dedup_append(acc, text.split())
        bpts = end_ms
    if acc:
        lines.append(f"[{hms(bstart)}-{hms(bpts)}] {' '.join(acc)}")
    out.write_text("\n".join(lines) + "\n")
    print(f"{out}: {len(lines)} blocks, last={hms(cues[-1][1])}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
