#!/usr/bin/env python3
"""Normalize every video under refactored/data/tmp to the canonical profile.

Canonical profile (SSOT user spec):
  MP4 / H.264 High@4.1 / yuv420p / 1920x1080 / 24 CFR / AAC-LC 48kHz stereo 192k / faststart

Usage:
  python3 scripts/tools/normalize_canonical_profile.py [--dry-run] [--workers N] [--root data/tmp]

Behavior:
  - Scans all .mp4/.mov/.mkv/.webm under --root (default data/tmp).
  - Skips files already matching the canonical profile.
  - Skips files with no readable video stream (broken/partial artifacts).
  - Backs up every file it re-encodes under <root>/.canonical-backup/ (path-mangled names).
  - Video-only inputs stay video-only (no invented audio track).
  - Re-encodes in place (atomic tmp+rename), parallel across workers.
"""
import argparse
import hashlib
import os
import shutil
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

CANON = ("h264", "High", "1920", "1080", "yuv420p", "24/1")

FFMPEG = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-nostdin"]


def probe_video(path):
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "v:0",
         "-show_entries", "stream=codec_name,profile,width,height,r_frame_rate,pix_fmt",
         "-of", "csv=p=0", path],
        capture_output=True, text=True)
    v = out.stdout.strip()
    if not v or out.returncode != 0:
        return None
    return v


def probe_audio(path):
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "a:0",
         "-show_entries", "stream=codec_name", "-of", "csv=p=0", path],
        capture_output=True, text=True)
    a = out.stdout.strip()
    return a if out.returncode == 0 and a else ""


def is_canonical(v):
    return bool(v) and all(k in v for k in CANON)


def backup_path(root, src):
    rel = os.path.relpath(src, root)
    mangled = hashlib.sha256(rel.encode()).hexdigest()[:16] + "__" + os.path.basename(src)
    return os.path.join(root, ".canonical-backup", mangled)


def encode_one(args):
    root, src, dry = args
    v = probe_video(src)
    if v is None:
        return ("SKIP", src, "no readable video stream")
    if is_canonical(v):
        return ("SKIP", src, "already canonical")
    a = probe_audio(src)
    if dry:
        return ("DRY", src, f"would normalize {v} audio={a or 'none'}")

    bkp = backup_path(root, src)
    os.makedirs(os.path.dirname(bkp), exist_ok=True)
    shutil.copy2(src, bkp)

    tmp = src + ".canonical.tmp.mp4"
    cmd = FFMPEG + ["-i", src,
                    "-map", "0:v:0", "-map_metadata", "-1", "-map_chapters", "-1",
                    "-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
                    "-profile:v", "high", "-level", "4.1",
                    "-pix_fmt", "yuv420p", "-r", "24", "-vsync", "cfr"]
    if a:
        cmd += ["-map", "0:a:0?", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2"]
    cmd += ["-movflags", "+faststart", tmp]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0 or not os.path.exists(tmp) or os.path.getsize(tmp) <= 0:
        if os.path.exists(tmp):
            os.remove(tmp)
        return ("FAIL", src, (r.stderr or "")[-300:])
    os.replace(tmp, src)
    return ("OK", src, f"{v} -> canonical (audio {a or 'none'})")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default="data/tmp")
    ap.add_argument("--workers", type=int, default=8)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    root = args.root
    files = []
    for dp, dns, fns in os.walk(root):
        dns[:] = [d for d in dns if not d.startswith(".")]  # skip backup/hidden dirs
        for fn in fns:
            if fn.lower().endswith((".mp4", ".mov", ".mkv", ".webm", ".m4a")):
                files.append(os.path.join(dp, fn))
    print(f"scanned {len(files)} media files under {root}")

    counts = {"OK": 0, "SKIP": 0, "FAIL": 0, "DRY": 0}
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futs = [pool.submit(encode_one, (root, f, args.dry_run)) for f in files]
        for i, fut in enumerate(as_completed(futs), 1):
            status, src, msg = fut.result()
            counts[status] += 1
            if status in ("OK", "FAIL"):
                print(f"[{i}/{len(files)}] {status}: {os.path.relpath(src, root)} — {msg}", flush=True)
            if status == "FAIL":
                sys.stderr.write(f"FAIL {src}: {msg}\n")
    print("done:", counts)


if __name__ == "__main__":
    main()
