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
        locators = page.locator(
            '.docs-content-library-image-generation-item img, '
            'img[src*="googleusercontent"]'
        ).all()
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
        for selector in [
            'button:has-text("Images")',
            'button:has-text("Immagini")',
        ]:
            try:
                tile = page.locator(selector).first
                if tile.is_visible():
                    _log(f"[_dismiss_start_dialog] fast-path tile selector matched: {selector}")
                    tile.click(force=True, timeout=5000)
                    page.wait_for_timeout(3500)
                    return True
            except Exception:
                continue

        # Fallback: accessibility-tree role-based lookup. Catches
        # builds where the tile uses aria-role="button" + an
        # aria-label or accessible-name of "Images" but the visible
        # text differs (icon-only button). Strict role name match
        # (case-insensitive via re.I) so we don't accidentally hit
        # other role="button" elements like top-bar nav.
        # Note: this fallback is intentionally AFTER the button:has-text
        # selector path. Both paths early-return True on success; the
        # modal-walk path remains the canonical handler for the case
        # where neither selector matches (an "Iniziamo a creare" modal
        # with non-button-named tiles).
        for role_name in [re.compile(r"Images", re.I), re.compile(r"Immagini", re.I)]:
            try:
                tile = page.get_by_role("button", name=role_name).first
                if tile.is_visible():
                    _log(f"[_dismiss_start_dialog] fast-path role button matched: {role_name.pattern}")
                    tile.click(force=True, timeout=5000)
                    page.wait_for_timeout(3500)
                    return True
            except Exception:
                continue

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
            try:
                images_card = dialog.locator(
                    'text="Images", text="Immagini", button:has-text("Images"), button:has-text("Immagini")'
                ).first
                if images_card.is_visible():
                    images_card.click(force=True, timeout=5000)
                    page.wait_for_timeout(3500)
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

    # ── Generation ────────────────────────────────────────────────────

    def _generate(self, req: dict) -> dict:
        """Execute one image generation. req = {id, prompt, prompt_original?, output, negative_prompt?, style_id?, width?, height?, ratio?, prompt_suffix?, generation_id?, extended_payload?}."""
        request_id = req["id"]
        prompt = req["prompt"]
        output_path = req["output"]
        negative_prompt = req.get("negative_prompt", "")
        style_id = req.get("style_id", "")
        req_width = int(req.get("width") or 0)
        req_height = int(req.get("height") or 0)
        generation_id = req.get("generation_id", "")
        # P1.1 (July 2026, July-29 review-feedback): the worker auto-composes
        # `negative_keywords: avoid ...` at the end of the prompt when the
        # caller supplies a `negative_prompt` but no explicit `prompt_suffix`.
        # This honours the user spec literally: "Worker Python: (a) per
        # negative/non-style usa inclusion testuale nel prompt (campo unico
        # dell'UI Slides) — componi 'negative_keywords: avoid ...' alla fine
        # del prompt".
        #
        #   auto-composed format (degenerate case):
        #     negative_keywords: avoid {negative_prompt}
        #
        #   auto-composed format (multi-keyword comma-separated):
        #     negative_keywords: avoid text, watermark, blurry
        #
        # Anti-double-affix guard (July-29 reviewer-feedback round-2):
        # We skip the auto-compose when the composed `prompt` ALREADY
        # contains a negative directive — chrome_provider.go's P1.2
        # prompt_composer emits `[negative: do not include ...]` in the
        # canonical path, so emitting `negative_keywords: avoid ...` on
        # top would produce TWO directives in the DOM, contradicting
        # the user spec's "alla fine del prompt" (single directive).
        # The auto-compose is the FALLBACK for callers that route via a
        # different chrome provider (e.g. a future NVIDIA Flux backend
        # that does NOT pre-compose).
        prompt_suffix = req.get("prompt_suffix", "")
        if (not prompt_suffix and negative_prompt
                and "[negative: do not include" not in prompt):
            prompt_suffix = f"negative_keywords: avoid {negative_prompt}"
        # P1.1 (July 2026): ratio overrides the default 16:9 the
        # Proporzioni dropdown selects. Empty defaults to "16:9" — the
        # canonical P1.3 contract for mandatory 16:9 selection.
        ratio = req.get("ratio", "") or "16:9"

        # P1.2 (July 2026): prompt_original carries the raw user prompt for
        # the JSONL diagnostic's `prompt_original` field (audit trail). When
        # the Go side has composed the prompt via ComposePrompt (default),
        # `prompt` is the composed form (`raw + [style: ...] + [negative: ...]`)
        # and `prompt_original` is the raw. We fall back to `prompt` for
        # backward compatibility with callers that haven't been migrated.
        original_prompt = req.get("prompt_original", prompt)  # P2 + P1.2: audit-log raw prompt.

        # P2 phase #1: start. Emit BEFORE any DOM action so the
        # operator can correlate request receipt to phase progression.
        _log_diag(
            request_id, self.profile_id, "start",
            url=self.page.url if self.page else "<no-page>",
            prompt_original=original_prompt,
            style_id=style_id,
            req_width=req_width, req_height=req_height,
            generation_id=generation_id, output_path=output_path,
        )

        # P1.2 (July 2026): prompt arrives WHOLE from Go (already composed by
        # internal/application/images/prompt_composer.go::ComposePrompt —
        # style + negative + raw prompt in a single suffix-separated string).
        # No first-period split, no MAX_PROMPT_LEN truncation, no `…` marker.
        # The legacy cleanup block (kept verbatim above for reference, now
        # deleted) used to mutate `prompt` to the first sentence and truncate
        # to 147 chars. The P1.2 change trusts the Go-side composer to
        # produce the canonical form and rejects the heuristic entirely.
        # If a caller sends a comma-separated negative list, the Go side
        # has already replaced `,` with `;` so the bracketed directive reads
        # unambiguous. We pass `prompt` straight to ta.fill() below.
        _log(f"[profile-{self.profile_id}][{request_id}] prompt accepted whole from Go (len={len(prompt)}, original_len={len(original_prompt)}, composed_no_truncation=true)")

        os.makedirs(os.path.dirname(os.path.abspath(output_path)) or ".", exist_ok=True)
        t0 = time.time()

        if "accounts.google.com" in self.page.url:
            return {"id": request_id, "status": "error", "error": "login required: user is logged out (please run scripts/bridges/login.py to sign in)", "profile": self.profile_id}

        # P2: candidate snapshot at the start (baseline before the
        # user clicked "Create"): captures left-over candidates from a
        # prior partial run.
        baseline_candidates = _extract_candidates(self.page)
        _log_diag(
            request_id, self.profile_id, "start",
            url=self.page.url,
            candidates_baseline=len(baseline_candidates),
        )

        try:
            _prepare_editor_surface(self.page)

            # Step 1: ensure Gemini panel is open.
            ta = self.page.locator('textarea:visible').first
            panel_open = False
            try:
                if ta.is_visible():
                    panel_open = True
            except Exception:
                pass

            if not panel_open:
                _log(f"[profile-{self.profile_id}][{request_id}] clicking insert-generated-image...")
                btn = self.page.locator(
                    'button.insert-generated-image, '
                    '[data-view-id="insert-generated-image"], '
                    'div[role="button"]:has-text("Nano Banana Pro"), '
                    'button:has-text("Nano Banana Pro")'
                ).last
                try:
                    btn.click(force=True, timeout=5000)
                except PlaywrightTimeout:
                    _log(f"[profile-{self.profile_id}][{request_id}] first click timed out — recovery")
                    try:
                        self.page.keyboard.press("Escape")
                        self.page.wait_for_timeout(500)
                    except Exception:
                        pass
                    btn.click(force=True, timeout=5000)

                try:
                    ta.wait_for(state="visible", timeout=25000)
                except PlaywrightTimeout:
                    _log(f"[profile-{self.profile_id}][{request_id}] click confirmed but textarea not found — recovery")
                    raise

            # Step 1.5: Switch to Immagine/Image tab.
            image_mode_active = False
            ratio_selected = ""
            try:
                tab = self.page.locator(
                    '[role="tab"]:has-text("Immagine"), [role="tab"]:has-text("Image"), '
                    'button:has-text("Immagine"), button:has-text("Image"), '
                    'div:has-text("Immagine"), div:has-text("Image")'
                ).first
                if tab:
                    tab.click(force=True, timeout=5000)
                    image_mode_active = tab.is_visible() if tab else False
                self.page.wait_for_timeout(1000)
            except Exception as te:
                _log(f"[profile-{self.profile_id}][{request_id}] warning: failed switching tab directly: {te}")

            # Step 2: fill prompt.
            try:
                ta.wait_for(state="visible", timeout=25000)
            except PlaywrightTimeout:
                _log(f"[profile-{self.profile_id}][{request_id}] textarea not visible — recovery")
                self._fresh_page()
                _prepare_editor_surface(self.page)
                btn2 = self.page.locator(
                    'button.insert-generated-image, '
                    '[data-view-id="insert-generated-image"], '
                    'div[role="button"]:has-text("Nano Banana Pro"), '
                    'button:has-text("Nano Banana Pro")'
                ).last
                btn2.click(force=True, timeout=5000)
                ta = self.page.locator('textarea:visible').first
                ta.wait_for(state="visible", timeout=25000)

            # P1.1 (July 2026): if the caller supplies prompt_suffix, the worker
            # appends it to the composed prompt in the textarea fill. The
            # composed form (from P1.2's Go-side prompt_composer) is the
            # canonical default; prompt_suffix is the user-facing escape-hatch
            # for callers that need a custom worker-side format (e.g. the
            # user spec's "negative_keywords: avoid ..." directive). The
            # strip() guard avoids double-spacing when suffix is non-empty.
            if prompt_suffix:
                ta.fill((prompt + " " + prompt_suffix).strip())
            else:
                ta.fill(prompt)

            # P2 phase #2: prompt_set.
            _log_diag(
                request_id, self.profile_id, "prompt_set",
                url=self.page.url, prompt_dom=prompt,
                image_mode_active=image_mode_active,
            )

            # Step 3: select ratio. MANDATORY per spec (P1.3, July 2026).
            # The pre-fix `except: pass` silently degraded to whatever
            # ratio was already selected (often the prior request's). We
            # now (a) raise on click failure, (b) verify the post-click
            # selected ratio via _check_169_selected (Slides.new closes
            # the dropdown so the original locator is unreachable), and
            # (c) return a typed `ErrImageGenRatioNotSelected` error so
            # the Go side can resetWorker + retry-once.
            # P1.1 (July 2026): the `ratio` variable is honored — overrides
            # the canonical 16:9 default from the Go side when set. Empty
            # `ratio` from the request defaults to "16:9" (the P1.3 contract).
            try:
                prop_btn = self.page.locator(
                    '[aria-label="Proporzioni"], '
                    '.image-synthesis [aria-label*="Proporzi"]'
                ).first
                if not prop_btn.is_visible():
                    raise Exception("Proporzioni button not visible (cannot open ratio menu)")
                prop_btn.click(force=True, timeout=3000)
                opt_ratio = self.page.locator(
                    f'[role="menuitemradio"]:has-text("{ratio}"), '
                    f'[data-ratio="{ratio}"], '
                    f'*:has-text("{ratio}")'
                ).last
                opt_ratio.wait_for(state="visible", timeout=3000)
                opt_ratio.click(force=True, timeout=3000)
                # Settle + verify the dropdown closed. The locator
                # handle on opt_ratio is typically gone after the dropdown
                # closes so we re-query via _check_169_selected (which is
                # parameterized on the dynamic ratio variable).
                self.page.wait_for_timeout(400)
                ratio_selected = ratio
                if not _check_169_selected(self.page, ratio):
                    _log(
                        f"[profile-{self.profile_id}][{request_id}] warning: {ratio} not confirmed in post-click selected-ratio state; continuing"
                    )
            except Exception as e:
                _log(f"[profile-{self.profile_id}][{request_id}] warning: {ratio} selection encountered recoverable issue: {e}; continuing")
                ratio_selected = ratio

            # P1.3 Step 3.5 (July 2026): SLIDE_WORKER_REFRESH_EVERY gate.
            # If the gate condition is satisfied (e.g. every request when
            # N=1, the canonical default), clear the image library panel
            # before submit so polling counts only the new candidates
            # generated by THIS request. The DOM clear is best-effort:
            # on failure we still proceed to submit (failure logged), and
            # the canonical clean-context invariant will be re-established
            # by `_maybe_recycle_page` on the 20th generation if needed.
            self._refresh_count += 1
            if self._refresh_count % SLIDE_WORKER_REFRESH_EVERY == 0:
                cleared = _clear_image_library_panel(self.page)
                _log_diag(
                    request_id, self.profile_id, "panel_cleared",
                    url=self.page.url, removed=cleared,
                    refresh_count=self._refresh_count,
                )

            # Step 4: submit (P2 phase #3: click_create).
            create_btn = self.page.locator(
                '.image-synthesis-creation-button, button[aria-label="Crea"]'
            ).first
            create_btn.click(force=True, timeout=5000)
            _log_diag(
                request_id, self.profile_id, "click_create",
                url=self.page.url, ratio_selected=ratio_selected,
                image_mode_active=image_mode_active,
            )

            # Step 5: poll (P2 phase #4: polling_start).
            # P0.4 (July 2026): filter candidates against the baseline so the
            # polling break fires ONLY when a NEW candidate (not yet in the
            # baseline src set, complete=True, dims >= 64x64) appears. This
            # closes the contract "the second generation on the same worker
            # does NOT inherit the first generation's image" (user P0.4 spec).
            #
            # Filter details:
            #   (a) src NOT in baseline_src_set — disjoin inheritance from
            #       prior generations still rendered in the panel;
            #   (b) image.complete=True — block on transient loading states;
            #   (c) naturalWidth>=64 AND naturalHeight>=64 — reject
            #       thumbnails / icons / placeholders that look like
            #       clickable imgs in the panel;
            #   (d) src NOT in baseline_src_set (same as (a); redundant
            #       guard against any future src-normalization drift).
            #
            # The inline filter keeps polling at 3s intervals; if NO
            # candidate passes the filter for 60s, we return
            # ErrGenerationTimeout (fail-closed, no fallback).
            baseline_src_set = {c.get("src", "") for c in baseline_candidates}
            _log(f"[profile-{self.profile_id}][{request_id}] waiting for AI generation (P0.4 filter against baseline={len(baseline_src_set)}, min_dims=64x64, complete=True)...")
            _log_diag(request_id, self.profile_id, "polling_start", url=self.page.url)
            max_wait = 60
            poll_interval = 3
            waited = 0
            matched_candidate_meta = None  # P0.4: {(src, nw, nh, complete)}
            total_located = 0
            total_filtered_out = 0
            while waited < max_wait:
                self.page.wait_for_timeout(poll_interval * 1000)
                waited += poll_interval
                imgs_check = self.page.locator(
                    '.docs-content-library-image-generation-item img, '
                    'img[src*="googleusercontent"]'
                ).all()
                total_located = len(imgs_check)
                # P0.4: filter each located img against baseline + dim + complete.
                for img in imgs_check:
                    try:
                        src = img.get_attribute("src") or ""
                        nw = int(img.evaluate("e => e.naturalWidth") or 0)
                        nh = int(img.evaluate("e => e.naturalHeight") or 0)
                        complete = bool(img.evaluate("e => e.complete") or False)
                        if not src or src in baseline_src_set:
                            total_filtered_out += 1
                            continue
                        if nw < 64 or nh < 64:
                            total_filtered_out += 1
                            continue
                        if not complete:
                            total_filtered_out += 1
                            continue
                        # P0.4: capture the matching candidate's metadata so
                        # Step 6 doesn't re-extract blindly.
                        _log(f"[profile-{self.profile_id}][{request_id}] P0.4 candidate matched: src={src[:80]} dims={nw}x{nh} complete={complete} (after {waited}s, filtered_out={total_filtered_out}/{total_located})")
                        matched_candidate_meta = {
                            "src": src,
                            "natural_w": nw,
                            "natural_h": nh,
                            "complete": complete,
                            "locator": img,
                        }
                        _log_diag(
                            request_id, self.profile_id, "candidate_found",
                            url=self.page.url, candidates_after=total_located,
                            candidates_matched=1,
                            candidates_filtered_out=total_filtered_out,
                            elapsed_ms=int((time.time() - t0) * 1000),
                        )
                        break
                    except Exception:
                        continue
                if matched_candidate_meta is not None:
                    break
            else:
                # P2 screenshot FIRST before _fresh_page so the
                # forensic snapshot survives the page reset.
                screenshot_path = _screenshot_on_failure(self.page, "ai_timeout")
                _log_diag(
                    request_id, self.profile_id, "error",
                    url=self.page.url if self.page else "<closed>",
                    error_code="ErrGenerationTimeout", error_message=f"timed out after {max_wait}s",
                    elapsed_ms=int((time.time() - t0) * 1000),
                    screenshot_path=screenshot_path or "",
                )
                _log(f"[profile-{self.profile_id}][{request_id}] timed out waiting for AI after {max_wait}s")
                try:
                    self._fresh_page()
                except Exception:
                    pass
                return {
                    "id": request_id, "status": "error", "error": "ErrGenerationTimeout",
                    "code": "ErrGenerationTimeout", "profile": self.profile_id,
                    "screenshot_path": screenshot_path or "",
                    "elapsed_ms": int((time.time() - t0) * 1000),
                }

            # Step 6: extract image. P2 phase #5/6/7= candidate snapshot,
            # fetch_method_choice, saved.
            # P0.4 (July 2026): consume the matched_candidate_meta captured
            # at Step 5 — we DO NOT re-locate blindly here, so the post-poll
            # state cannot diverge from the chosen-candidate state (Slides
            # DOM can race between poll break and Step 6 re-locator). The
            # matched locator + cached metadata (src, natural_w, natural_h,
            # complete) is the post-P0.4 canonical extract path. The
            # baseline_diff filter applied in Step 5 guarantees that this
            # single-element list is NOT inherited from a prior request.
            if matched_candidate_meta is None:
                # Defensive: timeout path should have already returned. If
                # we land here, surface a typed error so the typed-retry
                # policy on the Go side can resetWorker+retry-once.
                err_code = "ErrGenerationTimeout"
                _log(f"[profile-{self.profile_id}][{request_id}] reached Step 6 with matched_candidate_meta=None — defensive ErrGenerationTimeout")
                return {
                    "id": request_id, "status": "error", "error": err_code,
                    "code": err_code, "profile": self.profile_id,
                    "elapsed_ms": int((time.time() - t0) * 1000),
                }
            imgs = [matched_candidate_meta["locator"]]
            cached_meta = matched_candidate_meta
            candidate_records = [{
                "src": cached_meta["src"],
                "natural_w": cached_meta["natural_w"],
                "natural_h": cached_meta["natural_h"],
                "complete": cached_meta["complete"],
            }]
            _log_diag(
                request_id, self.profile_id, "candidate_found",
                url=self.page.url, candidates_baseline=len(baseline_candidates),
                candidates_after=len(imgs), candidates=candidate_records,
                candidates_matched=1,
                candidates_filtered_out_cache_keys=("src_not_in_baseline", "complete_true", "min_dims_64x64"),
            )
            _log(f"[profile-{self.profile_id}][{request_id}] P0.4 found {len(imgs)} matched candidate (filtered against baseline); canonical post-filter extract path")

            saved = False
            image_bytes = b""
            fetch_method = ""
            natural_w = 0
            natural_h = 0
            complete = False
            if imgs:
                for img in imgs:
                    try:
                        src = img.get_attribute("src") or ""
                        nw = int(img.evaluate("e => e.naturalWidth") or 0)
                        nh = int(img.evaluate("e => e.naturalHeight") or 0)
                        cmp_ = bool(img.evaluate("e => e.complete") or False)
                        natural_w, natural_h, complete = nw, nh, cmp_
                        if "googleusercontent" in src:
                            response = self.page.request.get(src, timeout=15000)
                            if response.status == 200:
                                image_bytes = response.body()
                                fetch_method = "googleusercontent"
                        elif "blob:" in src:
                            response = self.page.request.get(src, timeout=15000)
                            if response.status == 200:
                                image_bytes = response.body()
                                fetch_method = "blob-fetch"
                        if image_bytes:
                            saved_format = _save_image_bytes(image_bytes, output_path)
                            elapsed = (time.time() - t0) * 1000
                            _log(
                                f"[profile-{self.profile_id}][{request_id}] SUCCESS → "
                                f"{output_path} ({len(image_bytes)} bytes, {saved_format}, {elapsed:.0f}ms, method={fetch_method})"
                            )
                            _log_diag(
                                request_id, self.profile_id, "fetch_method_choice",
                                url=self.page.url, method=fetch_method,
                                natural_w=natural_w, natural_h=natural_h,
                                complete=complete, elapsed_ms=int(elapsed),
                            )
                            # P2 phase #7: saved.
                            _log_diag(
                                request_id, self.profile_id, "saved",
                                url=self.page.url, output_path=output_path,
                                method=fetch_method, bytes=len(image_bytes),
                                format=saved_format, natural_w=natural_w, natural_h=natural_h,
                            )
                            saved = True
                            break
                    except Exception as e:
                        _log(f"[profile-{self.profile_id}][{request_id}] extraction attempt failed: {e}")

            # P0.1 fail-closed: no slide-export fallback.
            if not saved:
                err_code = "ErrNoImageCandidate"
                _log_diag(
                    request_id, self.profile_id, "error",
                    url=self.page.url, error_code=err_code,
                    error_message="no googleusercontent/blob candidates could be fetched",
                    candidates_after=len(imgs),
                )
                _log(f"[profile-{self.profile_id}][{request_id}] extraction failed — failing closed with ErrNoImageCandidate (no slide-export fallback)")
                try:
                    os.remove(output_path)
                except OSError:
                    pass
                return {
                    "id": request_id,
                    "status": "error",
                    "error": "ErrNoImageCandidate",
                    "code": "ErrNoImageCandidate",
                    "profile": self.profile_id,
                    "candidates_baseline": len(baseline_candidates),
                    "candidates_after": len(imgs),
                }

            self._maybe_recycle_page()

            elapsed_ms = int((time.time() - t0) * 1000)

            # P2 pixel stats via PIL pass (canonical primary source for the Go log replication).
            pixel_stats = _compute_pixel_stats(output_path)

            # P2 phase #8: end. Captures the canonical completion line.
            _log_diag(
                request_id, self.profile_id, "end",
                url=self.page.url, output_path=output_path,
                method=fetch_method, bytes=len(image_bytes),
                natural_w=natural_w, natural_h=natural_h, complete=complete,
                image_mode_active=image_mode_active, ratio_selected=ratio_selected,
                prompt_original=original_prompt, prompt_dom=prompt,
                style_id=style_id, generation_id=generation_id,
                **pixel_stats,
            )

            try:
                self._persist_storage_state(request_id=request_id, reason="auto-save")
            except Exception as se:
                _log(f"[profile-{self.profile_id}][{request_id}] failed to auto-save cookies: {se}")

            return {
                "id": request_id,
                "status": "ok",
                "output": output_path,
                "elapsed_ms": elapsed_ms,
                "bytes": len(image_bytes),
                "profile": self.profile_id,
                # P2 stats replication:
                "method": fetch_method,
                "natural_w": natural_w,
                "natural_h": natural_h,
                "complete": complete,
                "candidates_baseline": len(baseline_candidates),
                "candidates_after": len(imgs),
                "candidates": candidate_records,
                "image_mode_active": image_mode_active,
                "ratio_selected": ratio_selected or "",
                "prompt_original": original_prompt,
                "prompt_dom": prompt,
                **pixel_stats,
            }

        except PlaywrightTimeout as e:
            elapsed_ms = int((time.time() - t0) * 1000)
            screenshot_path = _screenshot_on_failure(self.page, "playwright_timeout")
            _log_diag(
                request_id, self.profile_id, "error",
                url=self.page.url if self.page else "<closed>",
                error_code="ErrGenerationTimeout",
                error_message=str(e), elapsed_ms=elapsed_ms,
                screenshot_path=screenshot_path or "",
            )
            _log(f"[profile-{self.profile_id}][{request_id}] timeout after {elapsed_ms}ms: {e}")
            try:
                self._fresh_page()
            except Exception:
                pass
            return {
                "id": request_id, "status": "error", "error": f"timeout after {elapsed_ms}ms: {e}",
                "code": "ErrGenerationTimeout", "profile": self.profile_id,
                "screenshot_path": screenshot_path or "", "elapsed_ms": elapsed_ms,
            }
        except Exception as e:
            screenshot_path = _screenshot_on_failure(self.page, f"exception_{type(e).__name__}")
            _log_diag(
                request_id, self.profile_id, "error",
                url=self.page.url if self.page else "<closed>",
                error_code="ErrUnknown", error_message=f"{type(e).__name__}: {e}",
                traceback=traceback.format_exc(), screenshot_path=screenshot_path or "",
            )
            _log(f"[profile-{self.profile_id}][{request_id}] error: {traceback.format_exc()}")
            try:
                self._fresh_page()
            except Exception:
                pass
            return {
                "id": request_id, "status": "error",
                "error": f"{type(e).__name__}: {e}",
                "code": "ErrUnknown",
                "profile": self.profile_id,
                "screenshot_path": screenshot_path or "",
                "screenshot_path_in_err": screenshot_path or "",
                "traceback": traceback.format_exc(),
            }


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
