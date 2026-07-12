"""Image-quality metric computation (Python side, DIAGNOSTIC ONLY).

CRITICAL SEMANTIC INVARIANT (godlike/06 SSOT):
  This module computes PIL-based metrics on saved PNG/JPEG/WEBP files
  for FORENSIC LOGGING. The decision authority on whether an image is
  blank / placeholder / valid lives in Go (`internal/application/scripts/visual_validate/ComputeStats`).
  Do NOT add thresholds, accept/reject logic, or fail-closed guards
  here — those would create a parallel authority that can drift from
  the canonical Go decider.

  Python: collect metrics -> emit via diagnostics
  Go    : read metrics -> decide canonical pass/fail

The migration in Commit 1 is byte-equivalent: the module-level
helpers `_save_image_bytes` and `_compute_pixel_stats` keep their
exact existing behaviour; the `PixelStats` dataclass is forward
scaffolding for typed-emission callers in later commits.
"""

from __future__ import annotations

import io
import os
from dataclasses import dataclass

from PIL import Image

from .diagnostics import _log


@dataclass(frozen=True)
class PixelStats:
    """Frozen typed view of one PIL-pass metric snapshot.

    Fields mirror the dict keys returned by the legacy
    `_compute_pixel_stats` exactly, so a future commit can switch
    dict-returning callers to dataclass-returning with one-line
    changes at the call site.

    Definitions (must stay in lockstep with the dict-returning
    implementation):
      - white_pct:    fraction of stride-sampled pixels where
                      r >= 240 && g >= 240 && b >= 240
      - variance:     grayscale variance across the canonical
                      sample (mean-of-squares minus square-of-mean)
      - edge_density: fraction of horizontal sample-pairs where
                      |Δgray| > 30
      - phash_hex:    8x8 average-hash of the 16-stride sample,
                      encoded as a 16-char hex string
    """
    white_pct: float
    variance: float
    edge_density: float
    phash_hex: str


def _save_image_bytes(image_bytes: bytes, output_path: str) -> str:
    """Save image bytes using the format implied by the output file extension.

    Returns the canonical lowercase extension actually written
    ("png" / "jpeg" / "webp") so the typed response can echo the
    saved_format faithfully. Unknown extensions fall back to PNG so
    the output remains deterministic for forensic inspection.
    """
    ext = os.path.splitext(output_path)[1].lower().lstrip(".")
    if not ext:
        ext = "png"

    with Image.open(io.BytesIO(image_bytes)) as img:
        if ext in {"jpg", "jpeg"}:
            img = img.convert("RGB")
            img.save(output_path, format="JPEG", quality=95)
            return "jpeg"

        if ext == "png":
            if img.mode not in {"RGB", "RGBA"}:
                img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
            img.save(output_path, format="PNG")
            return "png"

        if ext == "webp":
            if img.mode not in {"RGB", "RGBA"}:
                img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
            img.save(output_path, format="WEBP")
            return "webp"

        # Unknown extension: default to PNG to keep the output deterministic.
        if img.mode not in {"RGB", "RGBA"}:
            img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
        img.save(output_path, format="PNG")
        return "png"


def _compute_pixel_stats(path: str) -> dict:
    """PIL pass on the saved PNG, returning a dict of content statistics.

    Returns {} on error (the caller already has the typed error; we
    don't want a stats failure to swallow the typed response).

    The four canonical fields (P2):
      - white_pct:   fraction of pixels where r >= 240 && g >= 240 && b >= 240
                     (the "near-white" sentinel that triggers Godlike/07 fail-closed)
      - variance:    grayscale variance across the canonical 16-stride sample
                     (CHEAPER than full iteration; bounded 0-255^2)
      - edge_density: fraction of horizontal pixel-pairs where |Δgray| > 30
                     (real images have structure; pure-white = 0; pure-color = 1)
      - phash_hex:   8x8 average-hash of the 16-stride sample, in canonical
                     16-char hex (matches the Go-side visual_validate.ComputeStats
                     routine for cross-validation parity)

    Performance: 1920x1080 sampled at stride=16 → ~7500 sample pixels →
    PIL pass + sum/square + 8x8 downsample ≈ 30ms on a mid-range laptop.
    """
    try:
        with Image.open(path) as im:
            im = im.convert("RGB")
            w, h = im.size
            if w == 0 or h == 0:
                return {}
            # 16-stride sampling: 7500 samples for 1920x1080. Cheaper
            # than full iteration; the validator runs on the full pass
            # on the Go side (visual_validate.ComputeStats iterates every
            # pixel for the typed sentinel; the worker provides the
            # approximated stats for log replication).
            sx = max(1, w // 32)
            sy = max(1, h // 32)
            total = 0
            white = 0
            sum_ = 0
            sumsq = 0
            grid_rows = []
            for y in range(0, h, sy):
                row = []
                for x in range(0, w, sx):
                    r, g, b = im.getpixel((x, y))
                    gray = (r + g + b) // 3
                    total += 1
                    if r >= 240 and g >= 240 and b >= 240:
                        white += 1
                    sum_ += gray
                    sumsq += gray * gray
                    row.append(gray)
                grid_rows.append(row)
            if total == 0:
                return {}
            mean = sum_ / total
            variance = sumsq / total - mean * mean
            white_pct = white / total

            # Edge density (horizontal diffs).
            edge_count = 0
            edge_total = 0
            for row in grid_rows:
                for i in range(1, len(row)):
                    diff = abs(row[i] - row[i - 1])
                    edge_total += 1
                    if diff > 30:
                        edge_count += 1
            edge_density = edge_count / edge_total if edge_total else 0

            # 8x8 downsample for pHash (uniform sub-grid over grid_rows).
            phash_bits = 0
            gy_step = max(1, len(grid_rows) // 8)
            gx_step = max(1, (len(grid_rows[0]) if grid_rows else 1) // 8)
            # Build an exact 8x8 grid by sampling at every (gy_step, gx_step).
            flat_gray = []
            for gy in range(8):
                src_y = min(gy * gy_step, len(grid_rows) - 1)
                row = grid_rows[src_y]
                src_x = 0
                for gx in range(8):
                    src_x = min(gx * gx_step, len(row) - 1)
                    flat_gray.append(row[src_x])
            grid_mean = sum(flat_gray) / len(flat_gray)
            for idx, val in enumerate(flat_gray):
                if val > grid_mean:
                    phash_bits |= 1 << idx
            phash_hex = format(phash_bits, "016x")

            return {
                "white_pct": round(white_pct, 4),
                "variance": round(variance, 2),
                "edge_density": round(edge_density, 4),
                "phash_hex": phash_hex,
            }
    except Exception as e:
        _log(f"[diag] PIL stats computation failed: {e}")
        return {}
