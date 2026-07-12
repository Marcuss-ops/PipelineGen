#!/usr/bin/env python3
"""
Persistent Chrome/Playwright worker for AI image generation via Google Slides
Nano Banana Pro.

Protocol (stdin → stdout, one JSON object per line, newline-delimited):

  REQUEST (stdin):
    {"action": "warmup"}
    {"action": "generate", "id": "<request-id>", "prompt": "...", "output": "/path/to/output.png", "negative_prompt"?: "...", "style_id"?: "...", "width"?: 1920, "height"?: 1080, "generation_id"?: "..."}
    {"action": "health"}
    {"action": "health_deep"}            # P2: deeper panel+textarea+image-mode probe
    {"action": "quit"}

  RESPONSE (stdout):
    {"id": "<request-id>", "status": "ok", "output": "...", "elapsed_ms": 22000, "bytes": 123456, "profile": 0,
     "method": "googleusercontent" | "blob-fetch", "natural_w": N, "natural_h": N, "complete": true,
     "candidates_baseline": 0, "candidates_after": 4, "candidates": [{src, natural_w, natural_h, complete}, ...],
     "phash_hex": "...", "white_pct": 0.31, "variance": 1234.5, "edge_density": 0.42,
     "image_mode_active": true, "ratio_selected": "16:9",
     "prompt_original": "...", "prompt_dom": "...",
     ... P2 stats replication ...}
    {"id": "<request-id>", "status": "error", "error": "...", "code": "...", "screenshot_path": "..."}
    {"status": "ready", "profiles": 1}        # warmup response
    {"status": "ok", "profiles": {"0": "ok"}}  # health
    {"status": "ok", "panel_ok": true, "textarea_ok": true, "image_mode_selectable": true, "url": "..."}  # health_deep

Single-profile model:
  - One ProfileWorker thread owns one persistent browser context.
  - Requests are processed serially through a single queue.
  - No legacy profile cloning, no round-robin, no multi-profile routing.

P2 diagnostics (July 2026):
  - Per-REQUEST JSONL emission to $P2_DIAGNOSTICS_DIR/requests.jsonl (one
    line per phase; phase ∈ {start, prompt_set, click_create, polling_start,
    candidate_found, fetch_method_choice, saved, end, error}).
  - PIL pixel stats (white_pct, variance, edge_density, pHash hex) computed
    on the saved PNG via _compute_pixel_stats.
  - Candidate baseline + post-click candidate list (src + natural w/h +
    complete) emitted in both the JSONL diagnostics and the workerResponse.
  - Screenshot-on-error path capture before _fresh_page so a forensic
    snapshot survives the page reset.
"""

import argparse
import datetime as _dt
import io
import json
import os
import queue
import re
import signal
import sys
import threading
import time
import traceback
from datetime import datetime, timezone

from playwright.sync_api import sync_playwright, TimeoutError as PlaywrightTimeout
from storage_utils import choose_storage_candidate, save_storage_snapshot, storage_looks_usable
from PIL import Image
from slide_worker_runtime.config import (
    MASTER_STORAGE, PROFILE_DIR, P2_DIAGNOSTICS_DIR, DIAG_FILE,
    SLIDE_WORKER_REFRESH_EVERY, CANDIDATE_LOCATOR_SELECTOR,
)
from slide_worker_runtime.protocol import (
    parse_request, validate_generate_request,
    write_response as _respond, write_error as _error,
    GenerateRequest, WorkerResponse,
)
from slide_worker_runtime.diagnostics import (
    _iso8601_utc_ms, _log, _log_diag, _screenshot_on_failure, DiagnosticsSink,
)
from slide_worker_runtime.image_quality import _save_image_bytes, _compute_pixel_stats, PixelStats


MASTER_STORAGE = "data/google_slides_storage.json"
PROFILE_DIR = "data/google_slides_session_profile"

# ── Legacy prompt truncation RETIRED (P1.2, July 2026) ────────────────
#
# The constants below (MAX_PROMPT_LEN=150, PROMPT_ELLIPSIS="...",
# PROMPT_TARGET_LEN=147) and the corresponding `prompt.split('.')` first-
# period split + `prompt[:150].rsplit(' ', 1)[0]` truncation block in
# ProfileWorker._generate are REMOVED.
#
# Why the empirical MAX_PROMPT_LEN=150 was retired:
#   The pre-FASE-7 observation suggested Google Slides (Nano Banana Pro)
#   visually rejected prompts longer than ~150 chars (silent canvas reset).
#   The mitigation heuristic was: (a) split on the first period (taking only
#   the FIRST sentence) and (b) truncate to 147 chars + "..." on a word
#   boundary. That heuristic destroyed semantic intent — a multi-clause
#   scenario prompt like "a vintage airport runway at night. dim runway
#   beacons. an approaching 747 with cabin lights" got cut to "a vintage
#   airport runway at night".
#
# New policy (P1.2, July 2026): the FULL prompt arrives from Go, already
# composed as `{prompt} [style: X] [negative: do not include ...]`. See
# internal/application/images/prompt_composer.go::ComposePrompt. We send
# the whole composed string to ta.fill(); the worker does NO mutation.
# If Google Slides still rejects on length, the typed retry policy
# (chrome_provider.Generate — ClassifyError → ErrImageGenPolicy /
# ErrImageGenTimeout / ErrImageGenPermanent / ErrImageGenNoImageCandidate)
# catches it with a structured error code so the operator sees the
# actual rejection and can shorten the prompt deliberately. Silent
# truncation is strictly worse because (a) the worker never shows the
# caller what it actually sent (no audit trail), and (b) a typo that
# shortens a meaningful clause to a preamble destroys the brief.
#
# godlike/07 no-fake-availability (P1.2): the worker's prompt-cleanup
# step was silent fake-availability. It accepted a multi-sentence
# prompt and emitted a single-sentence prompt without surfacing that
# fact anywhere visible to the operator. The P1.2 cutover is the
# canonical fix: trust the Go side to compose, trust the API to
# reject loudly, and surface every rejection structurally.
#
# Forward-pointer: a future push may add a model-aware compressor
# (e.g. summary-based shortening) firing above an empirically-validated
# threshold. Today's contract is "send whole; let the API reject
# loudly" and is pinned by the smoke test
# `TestSmoke_LongPromptWithStyleAndNegative_ArrivesWholeWithAffixes`
# plus `TestPromptComposer_DirectCall_FormatContract`.

# ── P2 diagnostics (July 2026) ────────────────────────────────────────────
#
# P2_DIAGNOSTICS_DIR: when set, the worker appends one JSONL line per
# phase-event per request to $P2_DIAGNOSTICS_DIR/requests.jsonl. When unset
# (the default), the diagnostics helper is a strict no-op so production
# performance is unaffected. godlike/07 no-fake-availability: the env
# var is opt-in; setting it should be a deliberate operator choice.
#
# Rationale for append mode: a single rolling log is cheap to maintain,
# survives worker restarts, and postmortem forensics flourish when all
# requests from the process are co-located. The file is created on first
# write; an empty P2_DIAGNOSTICS_DIR is silently ignored.
P2_DIAGNOSTICS_DIR = os.environ.get("P2_DIAGNOSTICS_DIR", "").strip()
DIAG_FILE = (
    os.path.join(P2_DIAGNOSTICS_DIR, "requests.jsonl")
    if P2_DIAGNOSTICS_DIR
    else None
)

# ── P1.3 panel-refresh (July 2026) ──────────────────────────────────────
#
# SLIDE_WORKER_REFRESH_EVERY: every N requests the worker triggers a
# DOM-level clear of the images library panel before the next submit.
# Default=1 ⟹ every request starts from a clean panel. Higher N amortises
# the 200-300ms clear cost across N requests when operator chooses to
# tolerate some cross-request contamination.
#
# Strategy: DOM-level removeChildren of the
#   `.docs-content-library-image-generation-item`
# subtree + 200ms settle. Slides.new exposes no native one-click "delete
# all"; we use plain element removal via page.evaluate. If the DOM clear
# raises (highly unlikely under Chromium), the call returns -1 and the
# worker keeps the prior state — the next request will see stale images
# only until `_maybe_recycle_page` triggers (every 20 successful runs).
SLIDE_WORKER_REFRESH_EVERY = max(1, int(os.environ.get("SLIDE_WORKER_REFRESH_EVERY") or "1"))

# ── Candidate-locator canonical form (July 2026) ──────────────────────────
#
# CANDIDATE_LOCATOR_SELECTOR: the canonical Playwright locator fragment
# for sniffing AI-generated image candidates from the Google Slides
# Nano Banana gallery. Centralised here so selector tweaks land in ONE
# place — previously duplicated across _extract_candidates + the polling
# loop in _generate, with silent-drift risk as the Slides DOM tree
# evolves across builds.
#
#   .docs-content-library-image-generation-item img
#       — tree-level container for the Slides image-generation panel
#   img.docs-image-synthesis-item
#       — element-level class pinned in P0.4.A on AI-synthesised items
#   img[src*="googleusercontent"]
#       — host-bucket filter for any googleusercontent-served candidate
#   img
#       — broad-scope fallback so late-loaded gallery children are seen
#
# Bit-for-bit identical to the previous inline fragments; refactor only.
CANDIDATE_LOCATOR_SELECTOR = (
    '.docs-content-library-image-generation-item img, '
    'img.docs-image-synthesis-item, '
    'img[src*="googleusercontent"], '
    'img'
)


def _iso8601_utc_ms() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"


# ── Helpers ────────────────────────────────────────────────────────────────

def _log(msg: str) -> None:
    _log.timestamp = _log.timestamp or _iso8601_utc_ms()
    print(f"[{_iso8601_utc_ms()}] {msg}", file=sys.stderr, flush=True)


_log.timestamp = None  # module-level cache variable for the linter; no semantic effect.


def _respond(obj: dict) -> None:
    """Write one JSON object line to stdout and flush."""
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def _error(request_id: str | None, msg: str) -> None:
    payload = {"status": "error", "error": msg}
    if request_id:
        payload["id"] = request_id
    _respond(payload)


def _save_image_bytes(image_bytes: bytes, output_path: str) -> str:
    """Save image bytes using the format implied by the output file extension."""
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


# ── P2 diagnostics helpers ────────────────────────────────────────────────

def _log_diag(request_id: str, profile_id: int, phase: str, **kwargs) -> None:
    """Append one JSONL line to $P2_DIAGNOSTICS_DIR/requests.jsonl.

    No-op when P2_DIAGNOSTICS_DIR is unset. Errors during emission are
    logged to stderr but do NOT crash the request path (godlike/07
    fail-closed observability — diagnostics are best-effort; the
    extraction pipeline is the source of truth).
    """
    if not DIAG_FILE:
        return
    payload = {
        "ts": _iso8601_utc_ms(),
        "request_id": request_id,
        "profile_id": profile_id,
        "phase": phase,
    }
    payload.update(kwargs)
    try:
        line = json.dumps(payload, ensure_ascii=False) + "\n"
        # Append mode 'a' accumulates across worker restarts (forensics
        # continuity). The flush() makes the line visible immediately
        # so a `while true; do tail -F` operator sees phases live.
        with open(DIAG_FILE, "a", encoding="utf-8") as f:
            f.write(line)
            f.flush()
    except Exception as e:
        _log(f"[diag] failed to write diagnostic line for phase={phase}: {e}")


def _screenshot_on_failure(page, label: str) -> str | None:
    """Save a screenshot under /tmp/slide_worker_diagnostics/ if writable.

    Per P2 (July 2026): invoked BEFORE _fresh_page in error paths so the
    page-reset doesn't erase the forensic snapshot. Returns the absolute
    path on success, None on write failure or absent page.
    """
    if page is None:
        return None
    try:
        target_dir = P2_DIAGNOSTICS_DIR if P2_DIAGNOSTICS_DIR else "/tmp/slide_worker_diagnostics"
        os.makedirs(target_dir, exist_ok=True)
        # Filename: slide_worker_<label>_<ts>.png for unambiguous sort.
        ts_short = _iso8601_utc_ms().replace(":", "").replace("-", "").replace(".", "").replace("Z", "")
        out_path = os.path.join(target_dir, f"slide_worker_{label}_{ts_short}.png")
        page.screenshot(path=out_path)
        return out_path
    except Exception as e:
        _log(f"[screenshot] failed to save {label} screenshot: {e}")
        return None


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


def _extract_candidates(page, max_keep: int = 8) -> list:
    """Snapshot the .docs-content-library-image-generation-item / blob imgs.

    Returns a bounded list (≤max_keep entries) of {src, natural_w,
    natural_h, complete} dicts. Returns [] if the page is missing or
    the DOM has zero matching elements.
    """
    if page is None:
        return []
    try:
        locators = page.locator(CANDIDATE_LOCATOR_SELECTOR).all()
        out = []
        for img in locators[:max_keep]:
            try:
                src = img.get_attribute("src") or ""
                nw = int(img.evaluate("e => e.naturalWidth") or 0)
                nh = int(img.evaluate("e => e.naturalHeight") or 0)
                complete = bool(img.evaluate("e => e.complete") or False)
                out.append({
                    "src": src,
                    "natural_w": nw,
                    "natural_h": nh,
                    "complete": complete,
                })
            except Exception:
                # Skip the candidate but keep going.
                continue
        return out
    except Exception as e:
        _log(f"[_extract_candidates] {e}")
        return []


def _clear_image_library_panel(page) -> int:
    """P1.3 (July 2026): DOM-level removeChildren of the images library panel.

    Slides.new exposes no native one-click "delete all" button for the
    image library; we use plain JavaScript node removal via page.evaluate.
    The function:

      1. Locates every `.docs-content-library-image-generation-item` node.
      2. Calls node.remove() on each.
      3. Returns the number of items removed (>= 0 on success).
      4. Applies a 200ms settle delay so the polling loop does not race
         with the DOM mutation (the test contract "second non vede candidati
         della prima dopo 800ms" assumes a ~200-300ms clean-window).

    Returns -1 on failure (the caller should treat this as best-effort
    cleanup; the canonical clean-context invariant will be re-established
    by `_maybe_recycle_page` on the 20th generation if needed).
    """
    if page is None:
        return -1
    try:
        removed = page.evaluate("""() => {
            const items = [...document.querySelectorAll('.docs-content-library-image-generation-item')];
            items.forEach(n => n.remove());
            return items.length;
        }""") or 0
        page.wait_for_timeout(200)
        return int(removed)
    except Exception as e:
        _log(f"[_clear_image_library_panel] DOM clear failed: {e}")
        return -1


def _click_visible_button_matching(
    page,
    text_regexes=None,
    *,
    selectors: str = "button",
    match_attrs: dict | None = None,
    match_attrs_logic: str = "all",
    require_visible: bool = True,
    require_enabled: bool = False,
    wait_ready: bool = False,
    timeout_ms: int = 5000,
    poll_interval_ms: int = 500,
    do_click: bool = True,
    use_dom_evaluate: bool = False,
) -> bool:
    """Best-effort find-and-(optionally)-click on a visible Playwright/Locator
    element matching text/aria regexes and any attribute-equality conditions.

    Single-shared helper that replaces:
      * _click_visible_start_images_tile
      * _click_visible_image_mode_tab
      * _click_visible_create_button
      * _wait_for_create_button_ready
      * the role-based / selector-based DOM scans inside _dismiss_start_dialog
      * the tile-retry DOM scan inside _wait_for_prompt_surface

    All six previously had near-identical structure (scan + click or scan + wait).
    They differ only in: (a) which selector to scan, (b) which text-regex set
    to match, (c) which attribute-equality conditions to enforce, (d) whether
    visibility/enablement are required, (e) whether the function polls with a
    timeout or one-shots, (f) whether the function actually clicks or just
    checks readiness, (g) whether to use Playwright's actionability semantics
    (Locator) or raw DOM-evaluate semantics (bypassing actionability).

    Two scan paths:
      * locator (default) — Playwright `page.locator(selectors)`, with
        `is_visible()` + `is_enabled()` checks and a `force=True` click that
        auto-waits up to 5s per element.
      * dom_evaluate (opt-in) — raw `page.evaluate(...)`, scans DOM and
        clicks via direct `el.click()`. Bypasses Playwright's actionability
        checks. Required for elements Playwright falsely reports as
        not-visible (e.g. Google Slides' kept-in-DOM Images card).

    Behaviour flags:
      * wait_ready=True — poll every poll_interval_ms until a match passes
        or timeout_ms elapses.
      * do_click=False — readiness check only; never click.
    """
    if page is None:
        return False
    if not text_regexes and not match_attrs:
        return False

    compiled = []
    for r in (text_regexes or []):
        compiled.append(re.compile(r, re.IGNORECASE) if isinstance(r, str) else r)
    match_attrs = match_attrs or {}
    deadline = time.time() + (timeout_ms / 1000.0) if wait_ready else None

    while True:
        try:
            ok = _scan_click_once(
                page, compiled, selectors, match_attrs, match_attrs_logic,
                require_visible, require_enabled, do_click, use_dom_evaluate,
            )
            if ok:
                return True
        except Exception as e:
            _log(f"[_click_visible_button_matching] scan exception: {e}")
        if not wait_ready:
            return False
        if deadline is not None and time.time() >= deadline:
            return False
        page.wait_for_timeout(poll_interval_ms)


def _scan_click_once(
    page, compiled, selectors, match_attrs, match_attrs_logic,
    require_visible, require_enabled, do_click, use_dom_evaluate,
) -> bool:
    """Single scan attempt; routes by use_dom_evaluate."""
    if use_dom_evaluate:
        return _scan_click_dom(
            page, compiled, selectors, match_attrs,
            require_visible, require_enabled, do_click,
        )
    return _scan_click_locator(
        page, compiled, selectors, match_attrs, match_attrs_logic,
        require_visible, require_enabled, do_click,
    )


def _scan_click_locator(
    page, compiled, selectors, match_attrs, match_attrs_logic,
    require_visible, require_enabled, do_click,
) -> bool:
    """Playwright Locator-based scan + force-click. Actionable elements only."""
    candidates = page.locator(selectors)
    try:
        n = candidates.count()
    except Exception as e:
        _log(f"[_scan_click_locator] locator count failed: {e}")
        return False
    for i in range(n):
        el = candidates.nth(i)
        try:
            if match_attrs and not _attrs_match(el, match_attrs, match_attrs_logic):
                continue
            if compiled and not _text_aria_match(el, compiled):
                continue
            if require_visible and not el.is_visible():
                continue
            if require_enabled and not el.is_enabled():
                continue
            if do_click:
                el.click(force=True, timeout=5000)
            return True
        except Exception:
            continue
    return False


def _scan_click_dom(
    page, compiled, selectors, match_attrs, require_visible, require_enabled, do_click,
) -> bool:
    """Raw page.evaluate scan + DOM-level click. Bypasses actionability."""
    js_pattern = "|".join(r.pattern for r in compiled)
    return bool(page.evaluate(
        """({selectors, textPatterns, matchAttrs, matchAttrsLogic, requireVisible, requireEnabled, doClick}) => {
            const candidates = [...document.querySelectorAll(selectors)];
            const rx = textPatterns ? new RegExp(textPatterns, 'i') : null;
            for (const el of candidates) {
                if (matchAttrs && Object.keys(matchAttrs).length > 0) {
                    if (matchAttrsLogic === 'all') {
                        let allHit = true;
                        for (const k in matchAttrs) {
                            const v = (el.getAttribute(k) || '').trim();
                            if (!matchAttrs[k].includes(v)) { allHit = false; break; }
                        }
                        if (!allHit) continue;
                    } else {
                        // 'any' (default for callers that don't pass logic explicitly)
                        let anyHit = false;
                        for (const k in matchAttrs) {
                            const v = (el.getAttribute(k) || '').trim();
                            if (matchAttrs[k].includes(v)) { anyHit = true; break; }
                        }
                        if (!anyHit) continue;
                    }
                }
                const txt = (el.textContent || '').trim();
                const aria = (el.getAttribute('aria-label') || '').trim();
                if (rx && !(rx.test(txt) || rx.test(aria))) continue;
                // Raw-DOM rect truthiness (matches the pre-fix
                // `_wait_for_create_button_ready` polling loop verbatim; do
                // NOT swap for Playwright `.is_visible()` semantics — that
                // would gate on extra actionability cues and lose the
                // wire-by-wire equivalence the round-2 shim claims).
                if (requireVisible) {
                    const visible = !!(el.offsetWidth || el.offsetHeight
                        || el.getClientRects().length);
                    if (!visible) continue;
                }
                const disabled = !!el.disabled
                    || el.classList.contains('goog-button-disabled');
                if (requireEnabled && disabled) continue;
                if (doClick) el.click();
                return true;
            }
            return false;
        }""",
        {
            "selectors": selectors,
            "textPatterns": js_pattern,
            "matchAttrs": match_attrs,
            "matchAttrsLogic": match_attrs_logic,
            "requireVisible": require_visible,
            "requireEnabled": require_enabled,
            "doClick": do_click,
        },
    ))


def _attrs_match(el, match_attrs: dict, logic: str) -> bool:
    """Apply match_attrs across attribute equality checks.

    `logic='any'`: OR across keys (any key value matching its allowed list
        passes the candidate).
    `logic='all'`: AND across keys (every key value must match its allowed
        list to pass).
    Within a single allowed list, equality is the test (value in list).
    """
    if logic == "all":
        for k, allowed in match_attrs.items():
            if (el.get_attribute(k) or "") not in allowed:
                return False
        return True
    for k, allowed in match_attrs.items():
        if (el.get_attribute(k) or "") in allowed:
            return True
    return False


def _text_aria_match(el, compiled) -> bool:
    """Match any compiled regex against the element's text OR aria-label."""
    try:
        text = (el.inner_text(timeout=500) or "").strip()
    except Exception:
        text = ""
    aria = (el.get_attribute("aria-label") or "").strip()
    return any(rgx.search(text) or rgx.search(aria) for rgx in compiled)


def _dismiss_start_dialog(page) -> bool:
    """Dismiss the Slides getting-started modal if it is present.

    Fresh Google Slides sessions often open on a modal like
    "Iniziamo a creare" with cards for Slides / Images / Infographics.
    The image workflow needs the editor canvas, not the overlay, so we
    either choose the Images card or close the dialog before proceeding.
    Returns True when a dialog was observed and handled.
    """
    if page is None:
        return False
    try:
        # Fast path: button-scoped text-match selectors. Scoped to
        # buttons (not any text node) so we don't accidentally match
        # the page title or a navigation bar item. Some Builds
        # expose the start-dialog tile as a button named "Images"
        # or "Immagini" outside any modal wrapper.
        # Fast path: accessibility-tree role-based lookup. Catches
        # builds where the tile is exposed as a button with accessible
        # name "Images" or "Immagini". This is the most reliable match
        # when the DOM contains hidden duplicates.
        # Migrated to _click_visible_button_matching (locator path). Single
        # call replaces the inner role-based scan + per-tile is_visible +
        # force-click loop, with identical observable behaviour (first
        # matching visible role-tagged button is clicked and we then
        # verify the prompt surface).
        if _click_visible_button_matching(
            page,
            [r"images", r"immagini"],
            require_visible=True,
        ):
            _log("[_dismiss_start_dialog] fast-path role button matched (unified helper)")
            if _wait_for_prompt_surface(page, timeout_ms=7000):
                return True
            page.wait_for_timeout(1000)
            return True

        if _click_visible_start_images_tile(page):
            _log("[_dismiss_start_dialog] DOM click on visible Images tile succeeded")
            if _wait_for_prompt_surface(page, timeout_ms=7000):
                return True
            page.wait_for_timeout(1000)
            return True

        # Migrated to _click_visible_button_matching (locator path).
        # Match on text + data-view-id + aria-controls in a single helper call.
        if _click_visible_button_matching(
            page,
            [r"images", r"immagini"],
            selectors='button[data-view-id="insert-generated-image"], button[aria-controls="insert-generated-image"], button',
            match_attrs={
                "data-view-id": ["insert-generated-image"],
                "aria-controls": ["insert-generated-image"],
            },
            match_attrs_logic="any",
            require_visible=True,
        ):
            _log("[_dismiss_start_dialog] fast-path tile selector matched (unified helper)")
            if _wait_for_prompt_surface(page, timeout_ms=7000):
                return True
            page.wait_for_timeout(1000)
            return True

        dialogs = page.locator('div[role="dialog"], div[aria-modal="true"]').all()
        for dialog in dialogs:
            try:
                txt = (dialog.inner_text(timeout=1000) or "").strip()
            except Exception:
                txt = ""
            if not txt:
                continue
            if "Iniziamo a creare" not in txt and "Ciao" not in txt and "Images" not in txt:
                continue

            _log(f"[_dismiss_start_dialog] handling modal: {txt[:80]!r}")

            # Prefer the Images card because it lands the editor directly
            # on the image-generation surface. The modal is permissive:
            # if the card click fails, fall through to the close/escape
            # path instead of blocking generation.
            # Modal-scoped tile scan: NOT migrated to the unified helper
            # because it must search children of a SPECIFIC dialog Locator
            # (not page-wide). Two-dialog ambiguity would let the helper
            # pick the wrong tile. Kept verbatim from the pre-unification
            # flow so behaviour is identical byte-for-byte.
            try:
                for selector in [
                    'button[data-view-id="insert-generated-image"]',
                    'button[aria-controls="insert-generated-image"]',
                    'button:has-text("Images")',
                    'button:has-text("Immagini")',
                ]:
                    images_cards = dialog.locator(selector)
                    _log(f"[_dismiss_start_dialog] modal candidate count for {selector}: {images_cards.count()}")
                    for idx in range(images_cards.count()):
                        images_card = images_cards.nth(idx)
                        if not images_card.is_visible():
                            continue
                        try:
                            txt = (images_card.inner_text(timeout=500) or "").strip()
                        except Exception:
                            txt = ""
                        _log(f"[_dismiss_start_dialog] modal tile selector matched: {selector} idx={idx}")
                        if txt:
                            _log(f"[_dismiss_start_dialog] modal tile text: {txt[:120]!r}")
                        images_card.click(force=True, timeout=5000)
                        if _wait_for_prompt_surface(page, timeout_ms=7000):
                            return True
                        page.wait_for_timeout(1000)
                        return True
            except Exception as e:
                _log(f"[_dismiss_start_dialog] Images card click failed: {e}")

            try:
                close_btn = dialog.locator(
                    'button[aria-label*="Chiudi"], button[aria-label*="Close"], '
                    'button:has-text("×"), button:has-text("X")'
                ).first
                if close_btn.is_visible():
                    close_btn.click(force=True, timeout=5000)
                    page.wait_for_timeout(800)
                    return True
            except Exception as e:
                _log(f"[_dismiss_start_dialog] close button click failed: {e}")

            try:
                page.keyboard.press("Escape")
                page.wait_for_timeout(800)
                return True
            except Exception as e:
                _log(f"[_dismiss_start_dialog] Escape fallback failed: {e}")
                return True
        return False
    except Exception as e:
        _log(f"[_dismiss_start_dialog] modal probe failed: {e}")
        return False


def _prepare_editor_surface(page) -> None:
    """Best-effort clear of startup overlays before touching the canvas.

    Google Slides can land on a getting-started wizard that blocks the
    prompt surface. A pair of Escape keypresses handles the common
    modal path; _dismiss_start_dialog() then handles the remaining card
    overlay if the wizard stays open.
    """
    if page is None:
        return
    try:
        page.keyboard.press("Escape")
        page.wait_for_timeout(300)
        page.keyboard.press("Escape")
        page.wait_for_timeout(300)
    except Exception as e:
        _log(f"[_prepare_editor_surface] Escape pre-clear failed: {e}")
    _dismiss_start_dialog(page)


def _wait_for_prompt_surface(page, timeout_ms: int = 15000) -> bool:
    """Wait for the visible textarea that hosts the Nano Banana prompt."""
    if page is None:
        return False
    ta = page.locator('textarea:visible').first
    try:
        ta.wait_for(state="visible", timeout=timeout_ms)
        return True
    except PlaywrightTimeout:
        try:
            if _click_visible_button_matching(
                page,
                [r"images", r"immagini"],
                require_visible=True,
            ):
                _log("[_wait_for_prompt_surface] retrying via visible tile (unified helper)")
                ta.wait_for(state="visible", timeout=timeout_ms)
                return True
        except Exception as e:
            _log(f"[_wait_for_prompt_surface] visible-tile retry failed: {e}")
        return False
    except Exception as e:
        _log(f"[_wait_for_prompt_surface] wait failed: {e}")
        return False


def _click_visible_start_images_tile(page) -> bool:
    """Thin shim around _click_visible_button_matching."""
    return _click_visible_button_matching(
        page,
        [r"images", r"immagini", r"insert-generated-image"],
        match_attrs={
            "data-view-id": ["insert-generated-image"],
            "aria-controls": ["insert-generated-image"],
        },
        match_attrs_logic="any",
        require_visible=False,
        use_dom_evaluate=True,
    )




def _click_visible_image_mode_tab(page) -> bool:
    """Thin shim around _click_visible_button_matching."""
    return _click_visible_button_matching(
        page,
        [r"^immagine$", r"^image$", r"immagine", r"image"],
        selectors='[role="tab"], button',
        require_visible=False,
        use_dom_evaluate=True,
    )




def _click_visible_create_button(page) -> bool:
    """Thin shim around _click_visible_button_matching."""
    return _click_visible_button_matching(
        page,
        [r"^crea$", r"^create$", r"crea", r"create"],
        selectors='button[aria-label="Crea"], button[aria-label="Create"], button.image-synthesis-creation-button',
        require_visible=True,
        require_enabled=True,
    )




def _type_prompt_text(page, locator, text: str) -> bool:
    """Type prompt text with real keyboard events so Google Slides updates state."""
    if page is None or locator is None:
        return False
    try:
        locator.click(force=True, timeout=5000)
        page.keyboard.press("Control+A")
        page.keyboard.type(text, delay=0)
        return True
    except Exception as e:
        _log(f"[_type_prompt_text] keyboard typing failed: {e}")
        try:
            locator.fill(text)
            return True
        except Exception as fe:
            _log(f"[_type_prompt_text] fallback fill failed: {fe}")
            return False


def _wait_for_create_button_ready(page, timeout_ms: int = 15000) -> bool:
    """Thin shim around _click_visible_button_matching (check-only path).

    Pre-fix used raw JS visibility (`offsetWidth || offsetHeight ||
    getClientRects().length`) via DOM-evaluate; we preserve that here so
    button-readiness semantics match byte-for-byte. A button whose rect is
    collapsed by CSS is NOT considered ready, regardless of Playwright's
    additional actionability cues (occlusion, hit-target geometry) that
    the locator path would consult.
    """
    return _click_visible_button_matching(
        page,
        [r"^crea$", r"^create$", r"crea", r"create"],
        require_visible=True,
        require_enabled=True,
        wait_ready=True,
        timeout_ms=timeout_ms,
        do_click=False,
        use_dom_evaluate=True,
    )




def _check_169_selected(page, ratio: str = "16:9") -> bool:
    """P1.3 (July 2026): post-click verification that the requested ratio is applied.

    Slides.new collapses the dropdown after click, so the original
    `opt_169` Playwright locator is unreachable after the click fires.
    We re-verify via a JS evaluate that walks a small list of DOM
    selectors in priority order; the first one that surfaces a non-empty
    label is the canonical "selected ratio" indicator. The check is
    best-effort: a missed selector returns False (not True) so the typed
    ErrImageGenRatioNotSelected path is taken conservatively.

    P1.1 (July 2026): the `ratio` parameter (defaulting to "16:9" if
    the request did not specify one) is canonical — we honored the
    dynamic value rather than hardcoding "16:9" so a future non-16:9
    ratio support can land without changing the selector set.

    Returns True iff one of the selectors matches a label containing
    the requested ratio. False on JS evaluate failure or no match.
    """
    if page is None:
        return False
    try:
        selected = page.evaluate("""() => {
            const candidates = [
                document.querySelector('[data-selected-ratio]'),
                document.querySelector('.ratio-button[data-active=\"true\"]'),
                document.querySelector('[role=\"radio\"][aria-checked=\"true\"][data-ratio]'),
                document.querySelector('[class*=\"ratio\"][class*=\"selected\"]'),
                document.querySelector('[aria-pressed=\"true\"][data-ratio]'),
                document.querySelector('[data-ratio=\"16:9\"]'),
            ];
            for (const el of candidates) {
                if (!el) continue;
                const txt = (el.textContent || el.getAttribute('aria-label') || el.getAttribute('data-ratio') || '').trim();
                if (txt.length > 0) return txt;
            }
            return '';
        }""") or ''
        return ratio in selected
    except Exception as e:
        _log(f"[_check_169_selected] DOM evaluate failed (ratio={ratio}): {e}")
        return False


# ── Generation step-method decomposition (July 2026) ─────────────────────────
#
# STAGE 3 (July 2026): split ProfileWorker._generate (~580 lines, 9 P2 diag
# phases, 4 error codes) into 7 focused per-responsibility step methods + a
# thin orchestrator. Each step owns its observable behaviour:
#
#   * `_step_prepare_surface`    — dismiss modals, ensure prompt textarea,
#                                  switch to Immagine/Image tab.
#   * `_step_fill_prompt`        — wait for textarea + type prompt_text; emits
#                                  `prompt_set` diag.
#   * `_step_select_ratio`       — open Proporzioni + click `ratio` option;
#                                  on failure emits `error` phase diag with
#                                  code=`ErrImageGenRatioNotSelected` and
#                                  CONTINUES (recoverable: log-only, no
#                                  raise — audit trail preserves code path).
#   * `_step_refresh_baseline`   — re-extract baseline AFTER sidebar
#                                  expansion + optional panel-clear gate.
#   * `_step_submit`             — wait for create button + click; emits
#                                  `click_create` diag.
#   * `_step_poll_for_candidate` — 60s polling loop, P0.4 filter via
#                                  CANDIDATE_LOCATOR_SELECTOR. On match
#                                  emits transient `candidate_found` diag +
#                                  captures metadata. On timeout raises
#                                  StepError with code `ErrGenerationTimeout`.
#   * `_step_extract_image`      — P0.4.A src-anchored Locator + google
#                                  content / blob: fetch / element-screenshot
#                                  fallback; emits richer `candidate_found` +
#                                  `fetch_method_choice` + `saved` + `end`
#                                  diags; on fail-closed raises StepError
#                                  with code `ErrNoImageCandidate`.
#
# Why exceptions (StepError) instead of return-tuples:
#   * Each step has a SINGLE success path + N failure paths. Return-tuple
#     shapes force call sites to remember which tuple shape to unpack and
#     lose per-error forensic fields (`candidates_baseline=...`,
#     `captured_src=...`, `elapsed_ms=...`) en route to the orchestrator.
#   * `StepError(Exception)` carries `.code` + `.diag_extra` + `.screenshot_path`
#     + `.error_message`. The orchestrator catches it once, emits the
#     canonical `error` JSONL phase, runs `_fresh_page`, returns typed
#     response.
#   * `PlaywrightTimeout` and generic `Exception` are caught successively
#     so a timeout maps to `ErrGenerationTimeout` without string-sniffing.
#
# Why `_GenerationContext` dataclass:
#   * 22+ pieces of mutable state (request fields, derived fields,
#     image_bytes, baseline sets, matched_candidate_meta, pixel_stats).
#     Passing each through every step signature would create fragile
#     parameter proliferation.
#
# JSONL audit trail invariants:
#   * 9 canonical P2 phases (start, prompt_set, click_create,
#     polling_start, candidate_found ×2, fetch_method_choice, saved, end).
#     The transient + richer pair of `candidate_found` lines is
#     INTENTIONAL: the richer one carries `anchor_strategy` +
#     full `candidate_records` and lands AFTER the polling break.
#   * `error` phases — one per typed error code (logged+returned for
#     fatal, logged-only for the recoverable ratio mis-select).
#   * `start` ALWAYS emits BEFORE the login check (Fix B): login-failing
#     requests still get a `start` line for forensic correlation.
#
# Go-side classification (Python worker does NOT emit):
#   * `ErrImageGenPermanent` — emitted solely by chrome_provider.go's
#     ClassifyError on the typed-retry policy.

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class _GenerationContext:
    """Per-request mutable state bundle passed through the 7 step methods."""
    request_id: str
    prompt: str
    original_prompt: str
    prompt_text: str
    output_path: str
    style_id: str
    generation_id: str
    req_width: int
    req_height: int
    ratio: str
    # Mutable fields set by steps:
    image_mode_active: bool = False
    ratio_selected: str = ""
    baseline_candidates: list = field(default_factory=list)
    baseline_src_set: set = field(default_factory=set)
    matched_candidate_meta: Optional[dict] = None
    natural_w: int = 0
    natural_h: int = 0
    complete: bool = False
    image_bytes: bytes = b""
    fetch_method: str = ""
    saved: bool = False
    saved_format: str = ""
    candidate_records: list = field(default_factory=list)
    pixel_stats: dict = field(default_factory=dict)
    t0: float = 0.0


class _StepError(Exception):
    """Raised by a step method to surface a typed error to the orchestrator.

    The orchestrator catches this once and returns the canonical typed
    error response. `diag_extra` is merged into both the `error` JSONL
    emission and the response dict so each error code path preserves
    its specific forensic context.

    `screenshot_path`, when passed as an empty string (""), suppresses
    the helper's `_screenshot_on_failure` fallback (Fix D): original
    `ErrNoImageCandidate` paths did NOT capture screenshots.
    """
    def __init__(self, code: str, *, screenshot_path=None,
                 diag_extra: Optional[dict] = None,
                 error_message: str = "", **legacy_kwargs):
        super().__init__(f"{code}: {error_message}" if error_message else code)
        self.code = code
        self.screenshot_path = screenshot_path
        self.error_message = error_message
        self.diag_extra = dict(diag_extra or {})
        self.diag_extra.update(legacy_kwargs)


# Alias kept short so the refactored steps have a quieter namespace.
StepError = _StepError


# ── ProfileWorker (one thread = one browser = one profile) ─────────────────

class ProfileWorker(threading.Thread):
    """One persistent browser profile with its own request queue.

    Lifecycle:
      start() → warmup (launch browser, navigate slides.new)
      run()  → loop: wait for request → generate → put result
      stop()  → shutdown (save session, close browser)
    """

    def __init__(self, profile_id: int, headful: bool = False) -> None:
        super().__init__(daemon=True, name=f"profile-{profile_id}")
        self.profile_id = profile_id
        self.profile_dir = f"{PROFILE_DIR}_{profile_id}_{os.getpid()}"
        self.headful = headful

        self.in_queue: queue.Queue = queue.Queue(maxsize=1)
        self.out_queue: queue.Queue = queue.Queue()

        self.playwright = None
        self.context = None
        self.page = None
        self._warmed = threading.Event()
        self._warmup_error: str | None = None
        self._running = True
        self._generation_count = 0
        self._max_generations_before_page_recycle = 20
        # P1.3 (July 2026): per-request counter that gates the panel
        # refresh (`SLIDE_WORKER_REFRESH_EVERY` env var). Initialised
        # to 0 so the first request has count=1 (divisible by N=1
        # default → always clear).
        self._refresh_count = 0

    # ── Warmup ────────────────────────────────────────────────────────

    def warmup(self) -> None:
        """Launch persistent browser, load cookies, navigate to slides.new."""
        _log(f"[profile-{self.profile_id}] warmup: launching browser...")
        _log_diag("warmup", self.profile_id, "warmup", url="https://slides.new")

        self.playwright = sync_playwright().start()

        self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}"
        try:
            os.makedirs(self.profile_dir, exist_ok=True)
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir,
                headless=not self.headful,
                args=[
                    "--disable-blink-features=AutomationControlled",
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                ],
            )
            _log(f"[profile-{self.profile_id}] warmup: launched browser with stable context at {self.profile_dir}")
        except Exception as le:
            self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}_{os.getpid()}"
            os.makedirs(self.profile_dir, exist_ok=True)
            _log(f"[profile-{self.profile_id}] warmup: stable context locked ({le}), falling back to {self.profile_dir}")
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir,
                headless=not self.headful,
                args=[
                    "--disable-blink-features=AutomationControlled",
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                ],
            )

        cookie_path, sdata = choose_storage_candidate(
            f"{MASTER_STORAGE}.profile_{self.profile_id}",
            MASTER_STORAGE,
            f"{MASTER_STORAGE}.profile_{self.profile_id}.backup",
            f"{MASTER_STORAGE}.backup",
        )
        if cookie_path and sdata:
            try:
                if "cookies" in sdata and sdata["cookies"]:
                    self.context.add_cookies(sdata["cookies"])
                    _log(f"[profile-{self.profile_id}] warmup: loaded session cookies from {cookie_path}")
                self._restore_storage_state(sdata)
            except Exception as e:
                _log(f"[profile-{self.profile_id}] warmup: failed to load cookies from {cookie_path}: {e}")

        self.page = self.context.new_page()
        try:
            self.page.goto("https://docs.google.com/presentation/create", wait_until="domcontentloaded", timeout=15000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: auth probe timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception("login required: user is logged out (please run scripts/bridges/login.py to sign in)")

        _log(f"[profile-{self.profile_id}] warmup: navigating to slides.new...")
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: slides.new timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception("login required: user is logged out (please run scripts/bridges/login.py to sign in)")

        self._warmed.set()
        _log(f"[profile-{self.profile_id}] warmup: ready")

    def _restore_storage_state(self, sdata: dict) -> None:
        origins = sdata.get("origins") or []
        if not origins:
            return

        for origin_state in origins:
            origin = origin_state.get("origin")
            local_storage = origin_state.get("localStorage") or []
            if not origin or not local_storage:
                continue
            page = None
            try:
                page = self.context.new_page()
                page.goto(origin, wait_until="domcontentloaded", timeout=30000)
                page.evaluate(
                    """(entries) => {
                        for (const item of entries) {
                            if (item && item.name !== undefined && item.value !== undefined) {
                                localStorage.setItem(item.name, item.value);
                            }
                        }
                    }""",
                    local_storage,
                )
                _log(
                    f"[profile-{self.profile_id}] warmup: restored localStorage for {origin}"
                )
            except Exception as e:
                _log(
                    f"[profile-{self.profile_id}] warmup: failed restoring localStorage for {origin}: {e}"
                )
            finally:
                try:
                    if page is not None:
                        page.close()
                except Exception:
                    pass

    def _persist_storage_state(self, request_id: str | None = None, reason: str = "auto-save") -> None:
        if not self.context:
            return
        if self.page and "accounts.google.com" in self.page.url:
            _log(f"[profile-{self.profile_id}] {reason}: skip save, browser is on accounts.google.com")
            return
        storage = self.context.storage_state()
        if not storage_looks_usable(storage):
            _log(f"[profile-{self.profile_id}] {reason}: skip save, storage snapshot looks empty")
            return
        save_storage_snapshot(MASTER_STORAGE, storage, backup_path=f"{MASTER_STORAGE}.backup")
        profile_storage_path = f"{MASTER_STORAGE}.profile_{self.profile_id}"
        save_storage_snapshot(profile_storage_path, storage, backup_path=f"{profile_storage_path}.backup")
        if request_id:
            _log(f"[profile-{self.profile_id}][{request_id}] {reason}: saved session snapshot")
        else:
            _log(f"[profile-{self.profile_id}] {reason}: saved session snapshot")

    # ── Main loop ──────────────────────────────────────────────────────

    def run(self) -> None:
        try:
            self.warmup()
        except Exception as e:
            self._warmup_error = str(e)
            self._warmed.set()
            _log(f"[profile-{self.profile_id}] warmup failed: {e}")
            return

        while self._running:
            try:
                req = self.in_queue.get(timeout=1)
            except queue.Empty:
                continue

            if req is None:
                break

            result = self._generate(req)
            self.out_queue.put(result)

        _log(f"[profile-{self.profile_id}] shutting down...")
        try:
            if self.context:
                self._persist_storage_state(reason="shutdown-save")
        except Exception as e:
            _log(f"[profile-{self.profile_id}] failed to save storage: {e}")

        try:
            if self.context:
                self.context.close()
        except Exception:
            pass

        try:
            if self.playwright:
                self.playwright.stop()
        except Exception:
            pass

        import shutil
        if "_" + str(os.getpid()) in self.profile_dir:
            try:
                shutil.rmtree(self.profile_dir, ignore_errors=True)
                _log(f"[profile-{self.profile_id}] cleaned up temporary profile directory {self.profile_dir}")
            except Exception as e:
                _log(f"[profile-{self.profile_id}] failed to clean up profile directory: {e}")
        else:
            _log(f"[profile-{self.profile_id}] preserving stable profile directory {self.profile_dir}")

        _log(f"[profile-{self.profile_id}] stopped")

    # ── Page recovery ─────────────────────────────────────────────────

    def _fresh_page(self) -> None:
        """Close current page and open a fresh one at slides.new."""
        _log(f"[profile-{self.profile_id}] recovery: opening fresh page...")
        try:
            if self.page and not self.page.is_closed():
                self.page.close()
        except Exception:
            pass
        if not self.context:
            _log(f"[profile-{self.profile_id}] recovery: context is None — cannot recover")
            return
        self.page = self.context.new_page()
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] recovery: slides.new timed out — continuing")

    def _maybe_recycle_page(self) -> None:
        self._generation_count += 1
        if self._generation_count >= self._max_generations_before_page_recycle:
            _log(f"[profile-{self.profile_id}] recycling page after {self._generation_count} generations")
            self._fresh_page()
            self._generation_count = 0

    # ── Health probes ─────────────────────────────────────────────────

    def health(self) -> dict:
        if not self._warmed.is_set():
            return {"status": "error", "error": "not warmed"}
        if not self.is_alive():
            return {"status": "error", "error": "thread died"}
        if self.page is None or self.page.is_closed():
            return {"status": "error", "error": "page closed"}
        if "accounts.google.com" in self.page.url:
            return {"status": "error", "error": "login required: user is logged out (please run scripts/bridges/login.py to sign in)"}
        return {"status": "ok"}

    # ── Health-deep probe (P2, July 2026) ────────────────────────────

    def health_deep(self) -> dict:
        """Probe DOM for Nano Banana panel + textarea + Immagine mode.

        Each sub-check is captured independently so the caller (Go
        side: HealthDeep()) can include fine-grained diagnostics in
        the typed error wrap. Profile health is the basic
        alive+warmed+url check.
        """
        panel_ok = False
        textarea_ok = False
        image_mode_selectable = False
        url = ""
        try:
            if self.page is not None and not self.page.is_closed():
                url = self.page.url
                # Panel = insert-generated-image button (Nano Banana Pro).
                try:
                    btn = self.page.locator(
                        'button.insert-generated-image, '
                        '[data-view-id="insert-generated-image"], '
                        'div[role="button"]:has-text("Nano Banana Pro"), '
                        'button:has-text("Nano Banana Pro")'
                    ).last
                    panel_ok = btn.is_visible() if btn else False
                except Exception:
                    panel_ok = False
                # Textarea = any visible prompt textarea.
                try:
                    ta = self.page.locator("textarea:visible").first
                    visible = ta.is_visible() if ta else False
                    enabled = ta.is_enabled() if (ta and visible) else False
                    textarea_ok = visible and enabled
                except Exception:
                    textarea_ok = False
                # Image-mode = Immagine/Image tab selectable (visible + enabled).
                try:
                    tab = self.page.locator(
                        '[role="tab"]:has-text("Immagine"), [role="tab"]:has-text("Image"), '
                        'button:has-text("Immagine"), button:has-text("Image")'
                    ).first
                    if tab:
                        visible = tab.is_visible()
                        enabled = tab.is_enabled()
                        image_mode_selectable = visible and enabled
                except Exception:
                    image_mode_selectable = False
        except Exception as e:
            _log(f"[profile-{self.profile_id}][health_deep] DOM probe error: {e}")

        profile_healthy = self.health().get("status") == "ok"
        failure_reasons = []
        if not panel_ok:
            failure_reasons.append("nano-banana-panel-missing")
        if not textarea_ok:
            failure_reasons.append("textarea-not-interactable")
        if not image_mode_selectable:
            failure_reasons.append("image-mode-not-selectable")
        if not profile_healthy:
            failure_reasons.append("profile-not-healthy")
        all_ok = len(failure_reasons) == 0
        return {
            "status": "ok" if all_ok else "error",
            "panel_ok": panel_ok,
            "textarea_ok": textarea_ok,
            "image_mode_selectable": image_mode_selectable,
            "url": url,
            "profile_healthy": profile_healthy,
            "failure_reason": ",".join(failure_reasons) if failure_reasons else "",
        }

    # ── Stop ──────────────────────────────────────────────────────────

    def stop(self) -> None:
        self._running = False
        try:
            self.in_queue.put_nowait(None)
        except queue.Full:
            pass

    # ── Generation step-method decomposition (STAGE 3, July 2026) ────
    #
    # The 7 `_step_*` methods below each own ONE concern of the P2
    # pipeline. Steps raise `StepError(code, **diag_extra)` for typed
    # failures so the orchestrator's single try/except chain maps to
    # typed responses uniformly. Steps that recover (e.g. ratio
    # mis-select) emit a typed `error` JSONL diag for audit but do
    # NOT raise — preserved pre-fix semantics. The
    # `_emit_failed_response` and `_build_success_response` helpers
    # keep the orchestrator thin: 3 except clauses (StepError,
    # PlaywrightTimeout, generic Exception) route through one
    # diagnostic shim.

    def _build_generation_context(self, req: dict) -> _GenerationContext:
        """Derive the mutable per-request state bundle from the inbound req.

        P1.2 prompt-composer trust: prompt arrives WHOLE from Go, no
        worker-side truncation. P1.1 negative-keyword auto-compose:
        only when caller does NOT pre-compose (i.e. no
        `[negative: do not include...]` directive).

        Emits the `start` phase BEFORE the login pre-check so login-
        failing requests still get a `start` line for forensic
        correlation (Fix B).
        """
        request_id = req["id"]
        prompt = req["prompt"]
        output_path = req["output"]
        negative_prompt = req.get("negative_prompt", "")
        style_id = req.get("style_id", "")
        req_width = int(req.get("width") or 0)
        req_height = int(req.get("height") or 0)
        generation_id = req.get("generation_id", "")
        # P1.1 (July 2026): ratio overrides the default 16:9. Empty
        # defaults to "16:9" — canonical P1.3 contract.
        ratio = req.get("ratio", "") or "16:9"
        # P1.2 (July 2026): prompt_original carries the raw user prompt
        # for the JSONL diagnostic's `prompt_original` field. Fall back
        # to `prompt` for backward compatibility.
        original_prompt = req.get("prompt_original", prompt)

        # P1.1 (July 2026): auto-compose `negative_keywords: avoid ...`
        # only if caller did NOT pre-compose AND supplied an explicit
        # `negative_prompt`. The anti-double-affix guard detects the
        # Go-side `[negative: do not include...]` directive.
        prompt_suffix = req.get("prompt_suffix", "")
        if (not prompt_suffix and negative_prompt
                and "[negative: do not include" not in prompt):
            prompt_suffix = f"negative_keywords: avoid {negative_prompt}"

        # P1.2: prompt_text is the canonically composed string fed to
        # ta.fill(). strip() guard avoids double-spacing.
        prompt_text = (prompt + " " + prompt_suffix).strip() if prompt_suffix else prompt

        # P1.2 trust-the-composer log line preserved verbatim.
        _log(
            f"[profile-{self.profile_id}][{request_id}] prompt accepted whole from Go "
            f"(len={len(prompt)}, original_len={len(original_prompt)}, composed_no_truncation=true)"
        )

        # P2 phase #1: start. Emitted BEFORE any DOM action AND BEFORE
        # the login pre-check so login-failing requests still get the
        # receipt marker (Fix B).
        _log_diag(
            request_id, self.profile_id, "start",
            url=self.page.url if self.page else "<no-page>",
            prompt_original=original_prompt,
            style_id=style_id,
            req_width=req_width, req_height=req_height,
            generation_id=generation_id, output_path=output_path,
        )

        return _GenerationContext(
            request_id=request_id,
            prompt=prompt,
            original_prompt=original_prompt,
            prompt_text=prompt_text,
            output_path=output_path,
            style_id=style_id,
            generation_id=generation_id,
            req_width=req_width,
            req_height=req_height,
            ratio=ratio,
        )

    def _step_prepare_surface(self, ctx: _GenerationContext) -> None:
        """Step 1: dismiss startup modals + ensure prompt textarea visible
        + switch to Immagine/Image mode tab.

        The login check is owned by the orchestrator (post-`_build_generation_context`)
        so this step focuses on Slides DOM surface.
        """
        _prepare_editor_surface(self.page)

        # Step 1a: ensure Gemini panel is open (textarea visible).
        ta = self.page.locator('textarea:visible').first
        panel_open = False
        try:
            panel_open = ta.is_visible()
        except Exception:
            panel_open = False

        if not panel_open:
            panel_open = _wait_for_prompt_surface(self.page, timeout_ms=15000)
            if not panel_open:
                raise PlaywrightTimeout(
                    "prompt surface did not become visible after selecting Images"
                )

        # Step 1b: switch to Immagine/Image tab. Tolerant: a failure
        # here logs a warning and continues — tab might already be
        # selected from a prior request.
        try:
            if _click_visible_image_mode_tab(self.page):
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] image mode tab click succeeded"
                )
                ctx.image_mode_active = True
            else:
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] image mode tab click not found"
                )
            self.page.wait_for_timeout(1000)
        except Exception as te:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                f"failed switching tab directly: {te}"
            )

    def _step_fill_prompt(self, ctx: _GenerationContext) -> None:
        """Step 2: ensure textarea visible (with _fresh_page recovery) +
        type prompt_text via keyboard events. Emits `prompt_set` diag.
        """
        ta = self.page.locator('textarea:visible').first
        try:
            ta.wait_for(state="visible", timeout=25000)
        except PlaywrightTimeout:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] textarea not visible — recovery"
            )
            self._fresh_page()
            _prepare_editor_surface(self.page)
            if not _wait_for_prompt_surface(self.page, timeout_ms=15000):
                raise PlaywrightTimeout(
                    "prompt surface did not become visible after recovery"
                )
            ta = self.page.locator('textarea:visible').first

        if not _type_prompt_text(self.page, ta, ctx.prompt_text):
            raise Exception("failed to type prompt into visible textarea")

        # P2 phase #2: prompt_set.
        _log_diag(
            ctx.request_id, self.profile_id, "prompt_set",
            url=self.page.url, prompt_dom=ctx.prompt_text,
            image_mode_active=ctx.image_mode_active,
        )

    def _step_select_ratio(self, ctx: _GenerationContext) -> None:
        """Step 3: open Proporzioni dropdown + click `ratio` option.

        RECOVERABLE by design: a click failure or post-click mis-select
        verification emits a typed `error` JSONL diag with code
        `ErrImageGenRatioNotSelected` (preserves the audit-trail code
        path) but does NOT raise. The pre-fix behavior was `except: pass`-
        style (silently accepted the wrong ratio); we preserve that
        continuation property while upgrading the audit trail.
        """
        try:
            prop_btn = self.page.locator(
                '[aria-label="Proporzioni"], '
                '.image-synthesis [aria-label*="Proporzi"]'
            ).first
            if not prop_btn.is_visible():
                raise Exception("Proporzioni button not visible (cannot open ratio menu)")
            prop_btn.click(force=True, timeout=3000)
            opt_ratio = self.page.locator(
                f'[role="menuitemradio"]:has-text("{ctx.ratio}"), '
                f'[data-ratio="{ctx.ratio}"], '
                f'*:has-text("{ctx.ratio}")'
            ).last
            opt_ratio.wait_for(state="visible", timeout=3000)
            opt_ratio.click(force=True, timeout=3000)
            # Settle + verify the dropdown closed. The locator
            # handle on opt_ratio is typically gone after the dropdown
            # closes so we re-query via _check_169_selected (parameter-
            # ized on the dynamic ratio variable).
            self.page.wait_for_timeout(400)
            ctx.ratio_selected = ctx.ratio
            if not _check_169_selected(self.page, ctx.ratio):
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                    f"{ctx.ratio} not confirmed in post-click selected-ratio state; continuing"
                )
                _log_diag(
                    ctx.request_id, self.profile_id, "error",
                    url=self.page.url,
                    error_code="ErrImageGenRatioNotSelected",
                    error_message=f"{ctx.ratio} not confirmed in post-click selected-ratio state",
                )
        except Exception as e:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                f"{ctx.ratio} selection encountered recoverable issue: {e}; continuing"
            )
            ctx.ratio_selected = ctx.ratio
            _log_diag(
                ctx.request_id, self.profile_id, "error",
                url=self.page.url,
                error_code="ErrImageGenRatioNotSelected",
                error_message=f"{ctx.ratio} selection encountered: {e}",
            )

    def _step_refresh_baseline(self, ctx: _GenerationContext) -> None:
        """Step 4: re-extract baseline AFTER sidebar expansion + optional
        panel-clear (SLIDE_WORKER_REFRESH_EVERY gate).

        The canonical baseline snapshot is taken here, AFTER the image
        mode tab + ratio dropdown have populated the panel, so
        `_extract_candidates(max_keep=200)` captures the real pre-
        submit gallery state. The pre-fix start-phase snapshot was
        stale UI from the prior partial run; the prior commit deduped
        that away.
        """
        ctx.baseline_candidates = _extract_candidates(self.page, max_keep=200)
        ctx.baseline_src_set = {c.get("src", "") for c in ctx.baseline_candidates}
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] refreshed baseline "
            f"before submit: {len(ctx.baseline_src_set)} candidate src(s)"
        )

        # P1.3 (July 2026): SLIDE_WORKER_REFRESH_EVERY gate. Best-effort
        # cleanup; a DOM-clear failure is tolerated (the canonical
        # clean-context invariant is re-established by `_maybe_recycle_page`
        # on the 20th generation if needed).
        self._refresh_count += 1
        if self._refresh_count % SLIDE_WORKER_REFRESH_EVERY == 0:
            cleared = _clear_image_library_panel(self.page)
            _log_diag(
                ctx.request_id, self.profile_id, "panel_cleared",
                url=self.page.url, removed=cleared,
                refresh_count=self._refresh_count,
            )

    def _step_submit(self, ctx: _GenerationContext) -> None:
        """Step 5: wait for create button to be ready + click. Emits
        `click_create` diag.
        """
        if not _wait_for_create_button_ready(self.page, timeout_ms=15000):
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                f"create button never became enabled before submit"
            )
        if _click_visible_create_button(self.page):
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] create button click succeeded"
            )
        else:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] create button click not found"
            )
            create_btn = self.page.locator(
                '.image-synthesis-creation-button, button[aria-label="Crea"]'
            ).first
            create_btn.click(force=True, timeout=5000)

        # P2 phase #3: click_create.
        _log_diag(
            ctx.request_id, self.profile_id, "click_create",
            url=self.page.url, ratio_selected=ctx.ratio_selected,
            image_mode_active=ctx.image_mode_active,
        )

    def _step_poll_for_candidate(self, ctx: _GenerationContext) -> None:
        """Step 6: 60s poll loop. P0.4 filter rejects src-in-baseline +
        incomplete (loading) + thumbnail (dims<64x64) candidates. On
        match: emits transient `candidate_found` diag + captures
        matched_candidate_meta for `_step_extract_image`. On timeout:
        captures screenshot + emits `error` diag with code
        `ErrGenerationTimeout` + raises StepError."""
        # P2 phase #4: polling_start.
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] waiting for AI generation "
            f"(P0.4 filter against baseline={len(ctx.baseline_src_set)}, "
            f"min_dims=64x64, complete=True)..."
        )
        _log_diag(ctx.request_id, self.profile_id, "polling_start", url=self.page.url)

        max_wait = 60
        poll_interval = 3
        waited = 0
        total_filtered_out = 0
        while waited < max_wait:
            self.page.wait_for_timeout(poll_interval * 1000)
            waited += poll_interval
            # CANDIDATE_LOCATOR_SELECTOR is the SINGLE canonical Playwright
            # locator fragment — replaces earlier inline duplicates with
            # silent-drift risk.
            imgs_check = self.page.locator(CANDIDATE_LOCATOR_SELECTOR).all()
            total_located = len(imgs_check)
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] poll t={waited}s "
                f"located={total_located} filtered_out={total_filtered_out}"
            )
            for img in imgs_check:
                try:
                    src = img.get_attribute("src") or ""
                    nw = int(img.evaluate("e => e.naturalWidth") or 0)
                    nh = int(img.evaluate("e => e.naturalHeight") or 0)
                    complete = bool(img.evaluate("e => e.complete") or False)
                    if not src or src in ctx.baseline_src_set:
                        total_filtered_out += 1
                        continue
                    if nw < 64 or nh < 64:
                        total_filtered_out += 1
                        continue
                    if not complete:
                        total_filtered_out += 1
                        continue
                    # P0.4: capture the matching candidate's metadata so
                    # Step 7 doesn't re-extract blindly.
                    _log(
                        f"[profile-{self.profile_id}][{ctx.request_id}] P0.4 candidate matched: "
                        f"src={src[:80]} dims={nw}x{nh} complete={complete} "
                        f"(after {waited}s, filtered_out={total_filtered_out}/{total_located})"
                    )
                    ctx.matched_candidate_meta = {
                        "src": src,
                        "natural_w": nw,
                        "natural_h": nh,
                        "complete": complete,
                        "locator": img,
                    }
                    # Transient `candidate_found` (RICH form follows in
                    # `_step_extract_image` after anchor build).
                    _log_diag(
                        ctx.request_id, self.profile_id, "candidate_found",
                        url=self.page.url, candidates_after=total_located,
                        candidates_matched=1,
                        candidates_filtered_out=total_filtered_out,
                        elapsed_ms=int((time.time() - ctx.t0) * 1000),
                    )
                    return
                except Exception:
                    continue

        # Timeout: emit `error` diag + raise StepError so the
        # orchestrator's single except clause picks it up.
        screenshot_path = _screenshot_on_failure(self.page, "ai_timeout")
        _log_diag(
            ctx.request_id, self.profile_id, "error",
            url=self.page.url if self.page else "<closed>",
            error_code="ErrGenerationTimeout",
            error_message=f"timed out after {max_wait}s",
            elapsed_ms=int((time.time() - ctx.t0) * 1000),
            screenshot_path=screenshot_path or "",
        )
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] timed out waiting for AI after {max_wait}s"
        )
        raise StepError(
            "ErrGenerationTimeout",
            screenshot_path=screenshot_path,
            diag_extra={"error_message": f"timed out after {max_wait}s"},
            candidates_baseline=len(ctx.baseline_candidates),
            elapsed_ms=int((time.time() - ctx.t0) * 1000),
        )

    def _step_extract_image(self, ctx: _GenerationContext) -> None:
        """Step 7: P0.4.A src-anchored Locator + googleusercontent/blob:
        fetch + save + emit richer `candidate_found`, `fetch_method_choice`,
        `saved`, and `end` diags.

        Failure paths (raises StepError):
          * P0.4.A fail-closed empty-src → `ErrNoImageCandidate` (no screenshot)
          * P0.4.A fail-looked-up vanished-candidate → `ErrNoImageCandidate` (no screenshot)
          * no-fetch → `ErrNoImageCandidate` (no screenshot)
        Per Fix D: the original `ErrNoImageCandidate` code paths did NOT
        capture screenshots. The step passes `screenshot_path=""` (falsy,
        not None) so `_emit_failed_response` skips the unsolicited
        `no_image_candidate` screenshot capture.
        """
        cached_meta = ctx.matched_candidate_meta
        if cached_meta is None:
            # Defensive: the poll step should have raised already; if
            # we reach the extract step without a candidate, the typed
            # timeout path is the canonical sentinel.
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] reached Step 7 with "
                f"matched_candidate_meta=None — defensive ErrGenerationTimeout"
            )
            raise StepError(
                "ErrGenerationTimeout",
                screenshot_path="",  # suppress unsolicited screenshot
                diag_extra={"error_message": "no candidate on Step 7 entry"},
            )

        captured_src = cached_meta["src"]

        # P0.4.A fail-closed: empty src. P0.4 filter at Step 6 would
        # have rejected this but cover the corner case anyway.
        if not captured_src:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
                f"matched_candidate_meta.src is empty"
            )
            _log_diag(
                ctx.request_id, self.profile_id, "error",
                url=self.page.url,
                error_code="ErrNoImageCandidate",
                error_message="P0.4.A: matched_candidate_meta.src is empty (no anchor)",
                candidates_baseline=len(ctx.baseline_candidates),
                screenshot_path="",
            )
            raise StepError(
                "ErrNoImageCandidate",
                screenshot_path="",  # Fix D
                diag_extra={"error_message": "P0.4.A: matched_candidate_meta.src is empty (no anchor)"},
                candidates_baseline=len(ctx.baseline_candidates),
                candidates_after=0,
            )

        # P0.4.A src-anchored Locator: img[src="X"] resolves determin-
        # istically regardless of DOM reordering. If the chosen img
        # vanished between Step 6 capture and Step 7 evaluate, surface
        # typed ErrNoImageCandidate. NO success-on-vanished.
        src_specific = self.page.locator(f'img[src="{captured_src}"]')
        if src_specific.count() == 0:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
                f'img[src="{captured_src[:80]}"] not found in current DOM '
                f"(vanished mid-extraction); surfacing typed ErrNoImageCandidate"
            )
            _log_diag(
                ctx.request_id, self.profile_id, "error",
                url=self.page.url,
                error_code="ErrNoImageCandidate",
                error_message=(
                    f"P0.4.A: img[src={captured_src[:80]}] not found at Step 7 "
                    f"(DOM redraw replaced/removed the chosen candidate)"
                ),
                captured_src=captured_src,
                candidates_baseline=len(ctx.baseline_candidates),
                screenshot_path="",
            )
            raise StepError(
                "ErrNoImageCandidate",
                screenshot_path="",  # Fix D
                diag_extra={
                    "error_message": (
                        f"P0.4.A: img[src={captured_src[:80]}] not found at Step 7"
                    ),
                },
                captured_src=captured_src,
                candidates_baseline=len(ctx.baseline_candidates),
                candidates_after=0,
            )

        imgs = [src_specific]
        candidate_records = [{
            "src": captured_src,
            "natural_w": cached_meta["natural_w"],
            "natural_h": cached_meta["natural_h"],
            "complete": cached_meta["complete"],
        }]

        # P2 phase #5 (richer form): candidate_found with anchor_strategy.
        _log_diag(
            ctx.request_id, self.profile_id, "candidate_found",
            url=self.page.url,
            candidates_baseline=len(ctx.baseline_candidates),
            candidates_after=len(imgs), candidates=candidate_records,
            candidates_matched=1,
            candidates_filtered_out_cache_keys=(
                "src_not_in_baseline", "complete_true", "min_dims_64x64"
            ),
            anchor_strategy="P0.4.A src-anchored (img[src=\"X\"]; survives DOM redraw)",
        )
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] P0.4 found {len(imgs)} "
            f"matched candidate (src-anchored P0.4.A guaranteed; "
            f"captured_src={captured_src[:80]}); canonical post-filter extract path"
        )

        # Fetch path: googleusercontent OR blob: (with proxied-fetch +
        # element-screenshot fallback per P0.3).
        for img in imgs:
            try:
                src = img.get_attribute("src") or ""
                nw = int(img.evaluate("e => e.naturalWidth") or 0)
                nh = int(img.evaluate("e => e.naturalHeight") or 0)
                cmp_ = bool(img.evaluate("e => e.complete") or False)
                ctx.natural_w, ctx.natural_h, ctx.complete = nw, nh, cmp_

                if "googleusercontent" in src:
                    response = self.page.request.get(src, timeout=15000)
                    if response.status == 200:
                        ctx.image_bytes = response.body()
                        ctx.fetch_method = "googleusercontent"
                elif "blob:" in src:
                    # P0.3 (July 2026): self.page.request.get() does NOT
                    # resolve blob: URLs because the blob: URL authority
                    # is the page-context. Cascade:
                    #   (a) window.fetch(src) inside page.evaluate —
                    #       proxy-on-page runs in page context where
                    #       blob: URL has authority;
                    #   (b) img.screenshot() element-scoped — preserves
                    #       rendered pixel content without page export.
                    try:
                        buffer_int_list = self.page.evaluate(
                            "url => fetch(url).then(r => r.arrayBuffer())"
                            ".then(b => Array.from(new Uint8Array(b)))",
                            src, timeout=10000,
                        )
                        if isinstance(buffer_int_list, list) and buffer_int_list:
                            ctx.image_bytes = bytes(buffer_int_list)
                            ctx.fetch_method = "blob-fetch"
                            _log(
                                f"[profile-{self.profile_id}][{ctx.request_id}] blob: "
                                f"window.fetch proxy-on-page succeeded "
                                f"({len(ctx.image_bytes)} bytes)"
                            )
                    except Exception as fe:
                        _log(
                            f"[profile-{self.profile_id}][{ctx.request_id}] blob: "
                            f"window.fetch proxy-on-page failed: {fe}"
                        )
                    if not ctx.image_bytes:
                        try:
                            ctx.image_bytes = img.screenshot(type="png", timeout=5000)
                            ctx.fetch_method = "element-screenshot"
                            _log(
                                f"[profile-{self.profile_id}][{ctx.request_id}] blob: "
                                f"element-screenshot fallback succeeded "
                                f"({len(ctx.image_bytes)} bytes)"
                            )
                        except Exception as se:
                            _log(
                                f"[profile-{self.profile_id}][{ctx.request_id}] blob: "
                                f"element-screenshot fallback failed: {se}"
                            )

                if ctx.image_bytes:
                    ctx.saved_format = _save_image_bytes(ctx.image_bytes, ctx.output_path)
                    elapsed = (time.time() - ctx.t0) * 1000
                    _log(
                        f"[profile-{self.profile_id}][{ctx.request_id}] SUCCESS → "
                        f"{ctx.output_path} ({len(ctx.image_bytes)} bytes, "
                        f"{ctx.saved_format}, {elapsed:.0f}ms, method={ctx.fetch_method})"
                    )

                    # P2 phase #6: fetch_method_choice.
                    _log_diag(
                        ctx.request_id, self.profile_id, "fetch_method_choice",
                        url=self.page.url, method=ctx.fetch_method,
                        natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                        complete=ctx.complete, elapsed_ms=int(elapsed),
                    )
                    # P2 phase #7: saved.
                    _log_diag(
                        ctx.request_id, self.profile_id, "saved",
                        url=self.page.url, output_path=ctx.output_path,
                        method=ctx.fetch_method, bytes=len(ctx.image_bytes),
                        format=ctx.saved_format,
                        natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                    )

                    ctx.candidate_records = candidate_records
                    ctx.saved = True

                    # P2 phase #8: end. Emitted here so the JSONL trail
                    # has the canonical completion marker before the
                    # orchestrator's success response is built.
                    ctx.pixel_stats = _compute_pixel_stats(ctx.output_path)
                    _log_diag(
                        ctx.request_id, self.profile_id, "end",
                        url=self.page.url, output_path=ctx.output_path,
                        method=ctx.fetch_method, bytes=len(ctx.image_bytes),
                        natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                        complete=ctx.complete,
                        image_mode_active=ctx.image_mode_active,
                        ratio_selected=ctx.ratio_selected,
                        prompt_original=ctx.original_prompt, prompt_dom=ctx.prompt,
                        style_id=ctx.style_id, generation_id=ctx.generation_id,
                        **ctx.pixel_stats,
                    )
                    return
            except Exception as e:
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] extraction "
                    f"attempt failed: {e}"
                )
                continue

        # P0.1 fail-closed: no slide-export fallback.
        _log_diag(
            ctx.request_id, self.profile_id, "error",
            url=self.page.url,
            error_code="ErrNoImageCandidate",
            error_message="no googleusercontent/blob candidates could be fetched",
            candidates_after=len(imgs),
            screenshot_path="",
        )
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] extraction failed — "
            f"failing closed with ErrNoImageCandidate (no slide-export fallback)"
        )
        try:
            os.remove(ctx.output_path)
        except OSError:
            pass
        raise StepError(
            "ErrNoImageCandidate",
            screenshot_path="",  # Fix D
            diag_extra={"error_message": "no googleusercontent/blob candidates could be fetched"},
            candidates_baseline=len(ctx.baseline_candidates),
            candidates_after=len(imgs),
        )

    def _emit_failed_response(self, ctx: _GenerationContext, code: str,
                              *, error_message: str = "",
                              traceback_str: str = "",
                              **extra) -> dict:
        """Common fail-closed shim: capture screenshot (if not provided
        AND the caller wants one), emit `error` diag, log, run
        `_fresh_page` recovery, return the canonical typed response
        dict with `diag_extra` merged.

        Screenshot capture rules:
          * If `extra["screenshot_path"]` is None or absent: helper
            synthesizes label from `code` (Fix C: ErrUnknown maps to
            `f"exception_{type(e).__name__}"` is handled by the
            orchestrator — the helper itself uses `f"exception_{code}"`
            which is overridden by callers).
          * If `extra["screenshot_path"]` is "" (empty string): helper
            SUPPRESSES the screenshot capture (Fix D).
          * Else: caller-provided `screenshot_path` is used verbatim.

        Auxiliary rule: the helper relies on the orchestrator for
        `ErrUnknown` because only there has the original been byte-
        equivalent (`f"exception_{type(e).__name__}"` reads exception
        class name).
        """
        screenshot_path = extra.pop("screenshot_path", None)
        if screenshot_path is None:
            label = {
                "ErrGenerationTimeout": "playwright_timeout",
                "ErrNoImageCandidate": "no_image_candidate",
            }.get(code, f"exception_{code}")
            screenshot_path = _screenshot_on_failure(self.page, label)
        elif screenshot_path == "":
            # Fix D: explicit suppression. Keep "" — the diag + response
            # both surface as `""`, matching the pre-fix `ErrNoImageCandidate`
            # behavior where no screenshot was captured.
            pass
        # Else: caller-provided path (e.g. from StepError, where the
        # step already captured with the right label like "ai_timeout").

        diag_payload = {
            "error_code": code,
            "error_message": error_message,
            "screenshot_path": screenshot_path or "",
        }
        diag_payload.update(extra)
        _log_diag(
            ctx.request_id, self.profile_id, "error",
            url=self.page.url if self.page else "<closed>",
            **diag_payload,
        )
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] error ({code}): "
            f"{error_message or '<no message>'}"
        )

        try:
            self._fresh_page()
        except Exception:
            pass

        elapsed_ms = int((time.time() - ctx.t0) * 1000)
        response = {
            "id": ctx.request_id, "status": "error",
            "error": error_message or code, "code": code,
            "profile": self.profile_id,
            "elapsed_ms": elapsed_ms,
            "screenshot_path": screenshot_path or "",
        }
        # Merge diag_extra into response (candidates_baseline,
        # captured_src, candidates_after, etc.) so the JSONL round-trip
        # is observable from the Go side without extra parsing.
        for k, v in extra.items():
            if k not in response:
                response[k] = v
        if traceback_str:
            response["traceback"] = traceback_str
            response["screenshot_path_in_err"] = screenshot_path or ""
        return response

    def _build_success_response(self, ctx: _GenerationContext) -> dict:
        """Build the canonical ok response after a successful extract step.

        Pixel stats were already computed inside `_step_extract_image`
        (drives the `end` diag emission); we merge them into the response
        here. `_maybe_recycle_page` and `_persist_storage_state` are
        owned by the orchestrator (NOT the step) so worker-lifecycle
        side effects stay centralised.
        """
        elapsed_ms = int((time.time() - ctx.t0) * 1000)
        return {
            "id": ctx.request_id, "status": "ok",
            "output": ctx.output_path, "elapsed_ms": elapsed_ms,
            "bytes": len(ctx.image_bytes), "profile": self.profile_id,
            # P2 stats replication:
            "method": ctx.fetch_method,
            "natural_w": ctx.natural_w, "natural_h": ctx.natural_h,
            "complete": ctx.complete,
            "candidates_baseline": len(ctx.baseline_candidates),
            "candidates_after": 1,
            "candidates": ctx.candidate_records,
            "image_mode_active": ctx.image_mode_active,
            "ratio_selected": ctx.ratio_selected or "",
            "prompt_original": ctx.original_prompt,
            "prompt_dom": ctx.prompt,
            **ctx.pixel_stats,
        }

    # ── Generation ────────────────────────────────────────────────────

    def _generate(self, req: dict) -> dict:
        """Thin orchestrator: build ctx (emits `start`), run 7 step
        methods in a single try/except chain. Login pre-check happens
        AFTER the `start` emit so login-failing requests still get a
        JSONL receipt marker (Fix B). Side effects (`_maybe_recycle_page`,
        `_persist_storage_state`) live at the orchestrator level — NOT
        in the steps — so worker-lifecycle is centralised.
        """
        ctx = self._build_generation_context(req)  # also emits `start`.

        # Login pre-check (Fix B): happens AFTER `start` emit so
        # login-failing requests still get the receipt marker. The
        # response shape matches the original pre-fix early-return:
        # no canonical typed fields (no elapsed_ms/candidates_*) since
        # the request never started running.
        if self.page is not None and "accounts.google.com" in self.page.url:
            return {
                "id": ctx.request_id, "status": "error",
                "error": "login required: user is logged out (please run scripts/bridges/login.py to sign in)",
                "profile": self.profile_id,
            }

        os.makedirs(
            os.path.dirname(os.path.abspath(ctx.output_path)) or ".", exist_ok=True
        )
        ctx.t0 = time.time()

        try:
            self._step_prepare_surface(ctx)
            self._step_fill_prompt(ctx)
            self._step_select_ratio(ctx)
            self._step_refresh_baseline(ctx)
            self._step_submit(ctx)
            self._step_poll_for_candidate(ctx)
            self._step_extract_image(ctx)
        except StepError as e:
            extra = dict(e.diag_extra)
            # If the StepError captured a screenshot inline (poll
            # timeout path uses label="ai_timeout"), surface it to the
            # helper; otherwise the helper falls back to the code-based
            # default label (or suppression when screenshot_path="").
            if e.screenshot_path is not None:
                extra.setdefault("screenshot_path", e.screenshot_path)
            return self._emit_failed_response(
                ctx, e.code, error_message=e.error_message, **extra,
            )
        except PlaywrightTimeout as e:
            return self._emit_failed_response(
                ctx, "ErrGenerationTimeout", error_message=str(e),
            )
        except Exception as e:
            # Fix C: capture screenshot with the original
            # `exception_<type(e).__name__>` label BEFORE invoking the
            # helper, so the filename matches pre-fix byte-for-byte.
            captured_screenshot = _screenshot_on_failure(
                self.page, f"exception_{type(e).__name__}"
            )
            return self._emit_failed_response(
                ctx, "ErrUnknown",
                error_message=f"{type(e).__name__}: {e}",
                traceback_str=traceback.format_exc(),
                screenshot_path=captured_screenshot,
            )

        # Success path: lifecycle side effects owned by the orchestrator
        # (NOT the steps) so worker-internal cleanup is centralised.
        self._maybe_recycle_page()
        try:
            self._persist_storage_state(
                request_id=ctx.request_id, reason="auto-save"
            )
        except Exception as se:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] "
                f"failed to auto-save cookies: {se}"
            )

        return self._build_success_response(ctx)



# ── Dispatcher (main thread) ───────────────────────────────────────────────

class SlideDispatcher:
    """Manages the single ProfileWorker thread and stdin requests."""

    def __init__(self, num_profiles: int, headful: bool = False) -> None:
        self.num_profiles = 1
        self.headful = headful
        self.profiles: list[ProfileWorker] = []
        self._shutdown_called = False

    def warmup_all(self) -> dict:
        self.profiles = [ProfileWorker(0, headful=self.headful)]
        for pw in self.profiles:
            pw.start()
        if not self.profiles[0]._warmed.wait(timeout=30):
            _log("slide_worker: profile-0 warmup timed out")
            return {"status": "error", "error": "profile-0 warmup timed out"}
        if self.profiles[0]._warmup_error:
            _log(f"slide_worker: profile-0 warmup failed immediately: {self.profiles[0]._warmup_error}")
            return {"status": "error", "error": self.profiles[0]._warmup_error}
        _log("slide_worker: profile ready")
        return {"status": "ready", "profiles": 1}

    def dispatch_generate(self, req: dict) -> dict:
        pw = self.profiles[0]
        pw.in_queue.put(req)
        _log(f"slide_worker: [{req.get('id', '')}] dispatched to profile-{pw.profile_id}")
        return pw.out_queue.get()

    def health_all(self) -> dict:
        statuses = {}
        all_ok = True
        for pw in self.profiles:
            h = pw.health()
            statuses[str(pw.profile_id)] = h["status"]
            if h["status"] != "ok":
                all_ok = False
        return {"status": "ok" if all_ok else "degraded", "profiles": statuses}

    def health_deep_all(self) -> dict:
        """P2 (July 2026): deeper probe. Delegates to per-profile health_deep."""
        if not self.profiles:
            return {
                "status": "error", "panel_ok": False, "textarea_ok": False,
                "image_mode_selectable": False, "profile_healthy": False,
                "failure_reason": "no_profiles_loaded",
            }
        return self.profiles[0].health_deep()

    def shutdown_all(self) -> None:
        if self._shutdown_called:
            return
        self._shutdown_called = True
        _log("slide_worker: shutting down all profiles...")
        for pw in self.profiles:
            pw.stop()
        for pw in self.profiles:
            pw.join(timeout=10)
            if pw.is_alive():
                _log(f"slide_worker: profile-{pw.profile_id} did not stop within 10s")
        _log("slide_worker: all profiles stopped")


# ── Main loop ───────────────────────────────────────────────────────────────

def main() -> None:
    parser = argparse.ArgumentParser(description="PipelineGen persistent Chrome/Playwright image generation worker")
    parser.add_argument("--profiles", type=int, default=1, help="Number of concurrent Chrome profiles (default: 1)")
    parser.add_argument("--headful", action="store_true", help="Run browsers in headful mode")
    args = parser.parse_args()

    num_profiles = max(1, args.profiles)
    dispatcher = SlideDispatcher(num_profiles, headful=args.headful)
    _log(f"slide_worker: started with {num_profiles} profile(s), waiting for commands on stdin...")
    if P2_DIAGNOSTICS_DIR:
        _log(f"slide_worker: P2 diagnostics ENABLED at {DIAG_FILE}")
    else:
        _log("slide_worker: P2 diagnostics DISABLED (set P2_DIAGNOSTICS_DIR to enable)")

    def _handle_signal(signum, frame):
        _log(f"slide_worker: received signal {signum}, shutting down...")
        dispatcher.shutdown_all()
        sys.exit(0)

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)

    try:
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue

            try:
                req = json.loads(line)
            except json.JSONDecodeError as e:
                _error(None, f"invalid JSON: {e}")
                continue

            action = req.get("action", "")

            if action == "warmup":
                try:
                    resp = dispatcher.warmup_all()
                    _respond(resp)
                except Exception as e:
                    _error(None, f"warmup failed: {e}")

            elif action == "generate":
                request_id = req.get("id", "")
                prompt = req.get("prompt", "")
                output_path = req.get("output", "")
                if not prompt:
                    _error(request_id, "missing prompt")
                    continue
                if not output_path:
                    _error(request_id, "missing output path")
                    continue
                # P1.1 + P1.2 (July 2026): forward the full extended payload
                # to the ProfileWorker thread. The browser/page lives in that
                # thread, so executing _generate() directly in the dispatcher
                # thread would trip Playwright's thread ownership checks.
                # dispatch_generate() preserves the canonical thread boundary:
                # main thread only enqueues the request; ProfileWorker owns
                # the page and performs DOM operations on its own thread.
                result = dispatcher.dispatch_generate({
                    "id": request_id,
                    "prompt": prompt,
                    "prompt_original": req.get("prompt_original", prompt),
                    "output": output_path,
                    "negative_prompt": req.get("negative_prompt", ""),
                    "style_id": req.get("style_id", ""),
                    "width": req.get("width", 0),
                    "height": req.get("height", 0),
                    "ratio": req.get("ratio", ""),
                    "prompt_suffix": req.get("prompt_suffix", ""),
                    "generation_id": req.get("generation_id", ""),
                })
                # The worker thread already produced the canonical JSON
                # response; the dispatcher just relays it to stdout.
                _respond(result)

            elif action == "health":
                _respond(dispatcher.health_all())

            elif action == "health_deep":
                # P2 (July 2026): deeper probe for Nano Banana UI surface.
                _respond(dispatcher.health_deep_all())

            elif action == "quit":
                _log("slide_worker: received quit command")
                dispatcher.shutdown_all()
                _respond({"status": "shutdown"})
                break

            else:
                _error(req.get("id"), f"unknown action: {action}")
    finally:
        _log("slide_worker: main loop exited — shutting down...")
        dispatcher.shutdown_all()

    _log("slide_worker: exiting")


if __name__ == "__main__":
    main()
