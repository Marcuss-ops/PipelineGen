"""Generation pipeline: GenerationContext + StepError + GenerationRunner.

Wave Commit 4 (July 2026): captures the 7 step-method decomposition
from ProfileWorker (`refactor(slide_worker): split _generate into 7
step methods and thin orchestrator`, July 2026) plus the typed
GenerationContext + StepError. Behaviour preservation is the
contract — see `_step_*` docstrings.

DOM scaffolding (`_prepare_editor_surface`, `_click_visible_image_mode_tab`,
`_dismiss_start_dialog`, etc.) is co-located in this module as
module-level helper functions so the step methods can reference
them via lexical scope WITHOUT introducing circular imports.
Commit 2 will fold these helpers into `runtime.dom_actions.SlidesDOM`
and add a typed facade. Today they stay here as transitional
verbatim relocations from slide_worker.py.

Per godlike/06 SSOT:
  - `GenerationContext` is the SINGLE typed surface for "the
    per-request mutable state bundle passed through the 7 step
    methods".
  - `StepError` is the SINGLE typed exception surface for
    orchestrator failures (Fix B/C/D land in `_emit_failed_response`).
  - `GenerationRunner.run()` is the thinnest possible orchestrator —
    all typed-failure logic centralised in one try/except chain.

Per godlike/07 fail-closed:
  - The runner's `_emit_failed_response` reproduces the pre-fix
    screenshot policy exactly (Fix D: empty-string `screenshot_path`
    suppresses the unsolicited screenshot capture; Fix C: the
    exception class name drives the screenshot label for
    `ErrUnknown` paths).
"""

from __future__ import annotations

import io
import os
import re
import time
import traceback
from dataclasses import dataclass, field
from typing import Optional

from playwright.sync_api import (
    Locator,
    Page,
    TimeoutError as PlaywrightTimeout,
)

from .config import (
    CANDIDATE_LOCATOR_SELECTOR,
    SLIDE_WORKER_REFRESH_EVERY,
)
from .diagnostics import _log_diag, _log, _screenshot_on_failure
from .image_quality import _compute_pixel_stats, _save_image_bytes
from .session import BrowserSession
from .candidates import _extract_candidates as _runtime_extract_candidates


# ── GenerationContext (typed per-request state bundle) ────────────────────


@dataclass
class GenerationContext:
    """Per-request mutable state bundle passed through the 7 step methods.

    Field semantics are preserved byte-byte from the prior
    `_GenerationContext` (private-leading-underscore) dataclass in
    slide_worker.py. The public name removes the underscore so the
    ProfileWorker slim thread + the wire-side consumers can reference
    it without import-time scoping surprises.
    """
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


# ── StepError (typed orchestrator failure surface) ─────────────────────────


class StepError(Exception):
    """Raised by a step method to surface a typed error to the orchestrator.

    The orchestrator catches it once and returns the canonical typed
    error response. `diag_extra` is merged into both the `error`
    JSONL emission and the response dict so each error code path
    preserves its specific forensic context.

    `screenshot_path`, when passed as an empty string (""), suppresses
    the helper's `_screenshot_on_failure` fallback (Fix D): original
    `ErrNoImageCandidate` paths did NOT capture screenshots.
    """
    def __init__(
        self,
        code: str,
        *,
        screenshot_path=None,
        diag_extra: Optional[dict] = None,
        error_message: str = "",
        **legacy_kwargs,
    ):
        super().__init__(f"{code}: {error_message}" if error_message else code)
        self.code = code
        self.screenshot_path = screenshot_path
        self.error_message = error_message
        self.diag_extra = dict(diag_extra or {})
        self.diag_extra.update(legacy_kwargs)


# ── DOM-action helpers (transitional co-location; Commit 2 → runtime.dom_actions) ──
#
# These module-level helpers were inline in slide_worker.py before
# Commit 4. They are NOT yet typed as a `SlidesDOM` facade — that
# work is Commit 2. They remain module-level here so the step methods
# below can reference them via lexical scope (no import overhead,
# no circular-import risk between slide_worker.py and this module).
#
# Use these helpers directly in step methods ONLY. Do not introduce
# new callers beyond the step methods and legacy slide_worker.py
# surfaces. New code should route through `SlidesDOM` post-Commit 2.


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
    """Best-effort find-and-(optionally)-click on a visible Playwright
    Locator element matching text/aria regexes and any attribute
    equality conditions. Single-shared helper that replaced six
    near-identical pre-fix click variants.
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
            page, compiled, selectors, match_attrs, match_attrs_logic,
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
    """Playwright Locator-based scan + force-click (actionable path)."""
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
    page, compiled, selectors, match_attrs, match_attrs_logic,
    require_visible, require_enabled, do_click,
) -> bool:
    """Raw page.evaluate scan + DOM-level click (bypasses actionability)."""
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

    `logic='any'`: OR across keys. `logic='all'`: AND across keys.
    Within a single allowed list, equality is the test.
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

    Returns True when a dialog was observed and handled.
    """
    if page is None:
        return False
    try:
        if _click_visible_button_matching(
            page, [r"images", r"immagini"], require_visible=True,
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
    """Best-effort clear of startup overlays before touching the canvas."""
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
                page, [r"images", r"immagini"], require_visible=True,
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
    """Thin shim around _click_visible_button_matching for the
    start-images tile."""
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
    """Thin shim around _click_visible_button_matching for the
    Immagine/Image mode tab."""
    return _click_visible_button_matching(
        page,
        [r"^immagine$", r"^image$", r"immagine", r"image"],
        selectors='[role="tab"], button',
        require_visible=False,
        use_dom_evaluate=True,
    )


def _click_visible_create_button(page) -> bool:
    """Thin shim around _click_visible_button_matching for the
    Crea/Create button."""
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
    """Thin shim around _click_visible_button_matching (check-only).
    Uses raw DOM visibility (offsetWidth|offsetHeight|getClientRects)
    — preserved byte-byte from pre-fix byte-for-byte behavior."""
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
    """P1.3: post-click verification that the requested ratio is applied.

    Best-effort. False on JS evaluate failure or no match (conservative:
    a missed selector makes the typed `ErrImageGenRatioNotSelected`
    path the canonical signal).
    """
    if page is None:
        return False
    try:
        selected = page.evaluate("""() => {
            const candidates = [
                document.querySelector('[data-selected-ratio]'),
                document.querySelector('.ratio-button[data-active="true"]'),
                document.querySelector('[role="radio"][aria-checked="true"][data-ratio]'),
                document.querySelector('[class*="ratio"][class*="selected"]'),
                document.querySelector('[aria-pressed="true"][data-ratio]'),
                document.querySelector('[data-ratio="16:9"]'),
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


# ── GenerationRunner ───────────────────────────────────────────────────────


class GenerationRunner:
    """Thin orchestrator that owns ONE generation lifecycle.

    Composed per-ProfileWorker via `__init__(session, profile_id)`.
    The runner does NOT own BrowserSession (that's the per-thread
    Playwright handle); it BORROWS the page from the session for the
    duration of one request.

    Public surface:
      run(req) -> dict — the canonical entry. Returns the response
                      dict (success OR typed-error shape). Wire-shape
                      compatibility is preserved byte-byte from the
                      pre-fix ProfileWorker._generate() output.

    Private helpers (kept on this class for lexical-scope access to
    `self.session.page`, `self.profile_id`, etc.):
      _build_context, _step_prepare_surface, _step_fill_prompt,
      _step_select_ratio, _step_refresh_baseline, _step_submit,
      _step_poll_for_candidate, _step_extract_image,
      _emit_failed_response, _build_success_response.
    """

    def __init__(self, session: BrowserSession, profile_id: int) -> None:
        self.session = session
        self.profile_id = profile_id

    # ── Private builders ──────────────────────────────────────────────

    def _build_context(self, req: dict) -> GenerationContext:
        """Derive the mutable per-request state bundle from the inbound req.

        P1.2 prompt-composer trust: prompt arrives WHOLE from Go, no
        worker-side truncation. P1.1 negative-keyword auto-compose
        only when caller does NOT pre-compose (no
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
        # P1.1: ratio overrides the default 16:9. Empty defaults to "16:9".
        ratio = req.get("ratio", "") or "16:9"
        # P1.2: prompt_original carries the raw user prompt. Fall back
        # to `prompt` for backward compatibility.
        original_prompt = req.get("prompt_original", prompt)

        # P1.1: auto-compose `negative_keywords: avoid ...` only if
        # caller did NOT pre-compose AND supplied an explicit
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
        page = self.session.page
        _log_diag(
            request_id, self.profile_id, "start",
            url=page.url if page else "<no-page>",
            prompt_original=original_prompt,
            style_id=style_id,
            req_width=req_width, req_height=req_height,
            generation_id=generation_id, output_path=output_path,
        )

        return GenerationContext(
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

    # ── Step 1: ────────────────────────────────────────────────────────

    def _step_prepare_surface(self, ctx: GenerationContext) -> None:
        """Step 1: dismiss startup modals + ensure prompt textarea visible
        + switch to Immagine/Image mode tab.
        """
        page = self.session.page
        _prepare_editor_surface(page)

        # Step 1a: ensure Gemini panel is open (textarea visible).
        ta = page.locator('textarea:visible').first
        panel_open = False
        try:
            panel_open = ta.is_visible()
        except Exception:
            panel_open = False

        if not panel_open:
            panel_open = _wait_for_prompt_surface(page, timeout_ms=15000)
            if not panel_open:
                raise PlaywrightTimeout(
                    "prompt surface did not become visible after selecting Images"
                )

        # Step 1b: switch to Immagine/Image tab. Tolerant: a failure
        # here logs a warning and continues — tab might already be
        # selected from a prior request.
        try:
            if _click_visible_image_mode_tab(page):
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] image mode tab click succeeded"
                )
                ctx.image_mode_active = True
            else:
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] image mode tab click not found"
                )
            page.wait_for_timeout(1000)
        except Exception as te:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                f"failed switching tab directly: {te}"
            )

    # ── Step 2: ────────────────────────────────────────────────────────

    def _step_fill_prompt(self, ctx: GenerationContext) -> None:
        """Step 2: ensure textarea visible (with fresh_page recovery) +
        type prompt_text via keyboard events. Emits `prompt_set` diag.
        """
        page = self.session.page
        ta = page.locator('textarea:visible').first
        try:
            ta.wait_for(state="visible", timeout=25000)
        except PlaywrightTimeout:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] textarea not visible — recovery"
            )
            self.session.fresh_page()
            _prepare_editor_surface(page)
            if not _wait_for_prompt_surface(page, timeout_ms=15000):
                raise PlaywrightTimeout(
                    "prompt surface did not become visible after recovery"
                )
            ta = page.locator('textarea:visible').first

        if not _type_prompt_text(page, ta, ctx.prompt_text):
            raise Exception("failed to type prompt into visible textarea")

        # P2 phase #2: prompt_set.
        _log_diag(
            ctx.request_id, self.profile_id, "prompt_set",
            url=page.url, prompt_dom=ctx.prompt_text,
            image_mode_active=ctx.image_mode_active,
        )

    # ── Step 3: ────────────────────────────────────────────────────────

    def _step_select_ratio(self, ctx: GenerationContext) -> None:
        """Step 3: open Proporzioni dropdown + click `ratio` option.

        RECOVERABLE by design: a click failure or post-click mis-select
        emits a typed `error` JSONL diag with code
        `ErrImageGenRatioNotSelected` (preserves the audit-trail code
        path) but does NOT raise.
        """
        page = self.session.page
        try:
            prop_btn = page.locator(
                '[aria-label="Proporzioni"], '
                '.image-synthesis [aria-label*="Proporzi"]'
            ).first
            if not prop_btn.is_visible():
                raise Exception("Proporzioni button not visible (cannot open ratio menu)")
            prop_btn.click(force=True, timeout=3000)
            opt_ratio = page.locator(
                f'[role="menuitemradio"]:has-text("{ctx.ratio}"), '
                f'[data-ratio="{ctx.ratio}"], '
                f'*:has-text("{ctx.ratio}")'
            ).last
            opt_ratio.wait_for(state="visible", timeout=3000)
            opt_ratio.click(force=True, timeout=3000)
            page.wait_for_timeout(400)
            ctx.ratio_selected = ctx.ratio
            if not _check_169_selected(page, ctx.ratio):
                _log(
                    f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                    f"{ctx.ratio} not confirmed in post-click selected-ratio state; continuing"
                )
                _log_diag(
                    ctx.request_id, self.profile_id, "error",
                    url=page.url,
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
                url=page.url,
                error_code="ErrImageGenRatioNotSelected",
                error_message=f"{ctx.ratio} selection encountered: {e}",
            )

    # ── Step 4: ────────────────────────────────────────────────────────

    def _step_refresh_baseline(self, ctx: GenerationContext) -> None:
        """Step 4: re-extract baseline AFTER sidebar expansion + optional
        panel-clear (SLIDE_WORKER_REFRESH_EVERY gate).
        """
        page = self.session.page
        # Use the runtime.candidates re-export of `_extract_candidates`
        # — same function (Commit 3 moved the original) but the path
        # through runtime.candidates is the single canonical owner.
        ctx.baseline_candidates = _runtime_extract_candidates(page, max_keep=200)
        ctx.baseline_src_set = {c.get("src", "") for c in ctx.baseline_candidates}
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] refreshed baseline "
            f"before submit: {len(ctx.baseline_src_set)} candidate src(s)"
        )

        # P1.3 (July 2026): SLIDE_WORKER_REFRESH_EVERY gate.
        from .candidates import _clear_image_library_panel as _runtime_clear_panel
        self.session._refresh_count += 1
        if self.session._refresh_count % SLIDE_WORKER_REFRESH_EVERY == 0:
            cleared = _runtime_clear_panel(page)
            _log_diag(
                ctx.request_id, self.profile_id, "panel_cleared",
                url=page.url, removed=cleared,
                refresh_count=self.session._refresh_count,
            )

    # ── Step 5: ────────────────────────────────────────────────────────

    def _step_submit(self, ctx: GenerationContext) -> None:
        """Step 5: wait for create button to be ready + click. Emits
        `click_create` diag.
        """
        page = self.session.page
        if not _wait_for_create_button_ready(page, timeout_ms=15000):
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] warning: "
                f"create button never became enabled before submit"
            )
        if _click_visible_create_button(page):
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] create button click succeeded"
            )
        else:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] create button click not found"
            )
            create_btn = page.locator(
                '.image-synthesis-creation-button, button[aria-label="Crea"]'
            ).first
            create_btn.click(force=True, timeout=5000)

        # P2 phase #3: click_create.
        _log_diag(
            ctx.request_id, self.profile_id, "click_create",
            url=page.url, ratio_selected=ctx.ratio_selected,
            image_mode_active=ctx.image_mode_active,
        )

    # ── Step 6: ────────────────────────────────────────────────────────

    def _step_poll_for_candidate(self, ctx: GenerationContext) -> None:
        """Step 6: 60s poll loop. P0.4 filter rejects src-in-baseline +
        incomplete (loading) + thumbnail (dims<64x64) candidates.

        On timeout raises StepError("ErrGenerationTimeout").
        """
        page = self.session.page
        # P2 phase #4: polling_start.
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] waiting for AI generation "
            f"(P0.4 filter against baseline={len(ctx.baseline_src_set)}, "
            f"min_dims=64x64, complete=True)..."
        )
        _log_diag(ctx.request_id, self.profile_id, "polling_start", url=page.url)

        max_wait = 60
        poll_interval = 3
        waited = 0
        total_filtered_out = 0
        while waited < max_wait:
            page.wait_for_timeout(poll_interval * 1000)
            waited += poll_interval
            imgs_check = page.locator(CANDIDATE_LOCATOR_SELECTOR).all()
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
                    _log_diag(
                        ctx.request_id, self.profile_id, "candidate_found",
                        url=page.url, candidates_after=total_located,
                        candidates_matched=1,
                        candidates_filtered_out=total_filtered_out,
                        elapsed_ms=int((time.time() - ctx.t0) * 1000),
                    )
                    return
                except Exception:
                    continue

        # Timeout: emit `error` diag + raise StepError so the
        # orchestrator's single except clause picks it up.
        screenshot_path = _screenshot_on_failure(page, "ai_timeout")
        _log_diag(
            ctx.request_id, self.profile_id, "error",
            url=page.url if page else "<closed>",
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

    # ── Step 7: ────────────────────────────────────────────────────────

    def _step_extract_image(self, ctx: GenerationContext) -> None:
        """Step 7: P0.4.A src-anchored Locator + googleusercontent/blob:
        fetch + save + emit richer `candidate_found`, `fetch_method_choice`,
        `saved`, and `end` diags.

        Raises StepError on any of three typed failure paths
        (empty src / vanished candidate / no-fetch). Per Fix D, all
        `ErrNoImageCandidate` paths pass `screenshot_path=""` so
        `_emit_failed_response` skips the unsolicited screenshot.
        """
        page = self.session.page
        cached_meta = ctx.matched_candidate_meta
        if cached_meta is None:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] reached Step 7 with "
                f"matched_candidate_meta=None — defensive ErrGenerationTimeout"
            )
            raise StepError(
                "ErrGenerationTimeout",
                screenshot_path="",
                diag_extra={"error_message": "no candidate on Step 7 entry"},
            )

        captured_src = cached_meta["src"]

        # P0.4.A fail-closed: empty src.
        if not captured_src:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
                f"matched_candidate_meta.src is empty"
            )
            _log_diag(
                ctx.request_id, self.profile_id, "error",
                url=page.url,
                error_code="ErrNoImageCandidate",
                error_message="P0.4.A: matched_candidate_meta.src is empty (no anchor)",
                candidates_baseline=len(ctx.baseline_candidates),
                screenshot_path="",
            )
            raise StepError(
                "ErrNoImageCandidate",
                screenshot_path="",
                diag_extra={"error_message": "P0.4.A: matched_candidate_meta.src is empty (no anchor)"},
                candidates_baseline=len(ctx.baseline_candidates),
                candidates_after=0,
            )

        # P0.4.A src-anchored Locator: img[src="X"]. If the chosen img
        # vanished between Step 6 capture and Step 7 evaluate, surface
        # typed ErrNoImageCandidate. NO success-on-vanished.
        src_specific = page.locator(f'img[src="{captured_src}"]')
        if src_specific.count() == 0:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
                f'img[src="{captured_src[:80]}"] not found in current DOM '
                f"(vanished mid-extraction); surfacing typed ErrNoImageCandidate"
            )
            _log_diag(
                ctx.request_id, self.profile_id, "error",
                url=page.url,
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
                screenshot_path="",
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
            url=page.url,
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

        # Fetch path: googleusercontent OR blob: (proxied-fetch +
        # element-screenshot fallback per P0.3).
        for img in imgs:
            try:
                src = img.get_attribute("src") or ""
                nw = int(img.evaluate("e => e.naturalWidth") or 0)
                nh = int(img.evaluate("e => e.naturalHeight") or 0)
                cmp_ = bool(img.evaluate("e => e.complete") or False)
                ctx.natural_w, ctx.natural_h, ctx.complete = nw, nh, cmp_

                if "googleusercontent" in src:
                    response = page.request.get(src, timeout=15000)
                    if response.status == 200:
                        ctx.image_bytes = response.body()
                        ctx.fetch_method = "googleusercontent"
                elif "blob:" in src:
                    try:
                        buffer_int_list = page.evaluate(
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

                    _log_diag(
                        ctx.request_id, self.profile_id, "fetch_method_choice",
                        url=page.url, method=ctx.fetch_method,
                        natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                        complete=ctx.complete, elapsed_ms=int(elapsed),
                    )
                    _log_diag(
                        ctx.request_id, self.profile_id, "saved",
                        url=page.url, output_path=ctx.output_path,
                        method=ctx.fetch_method, bytes=len(ctx.image_bytes),
                        format=ctx.saved_format,
                        natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                    )

                    ctx.candidate_records = candidate_records
                    ctx.saved = True

                    ctx.pixel_stats = _compute_pixel_stats(ctx.output_path)
                    _log_diag(
                        ctx.request_id, self.profile_id, "end",
                        url=page.url, output_path=ctx.output_path,
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
            url=page.url,
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
            screenshot_path="",
            diag_extra={"error_message": "no googleusercontent/blob candidates could be fetched"},
            candidates_baseline=len(ctx.baseline_candidates),
            candidates_after=len(imgs),
        )

    # ── Orchestrator-level helpers ─────────────────────────────────────

    def _emit_failed_response(self, ctx: GenerationContext, code: str,
                              *, error_message: str = "",
                              traceback_str: str = "",
                              **extra) -> dict:
        """Common fail-closed shim (Fix B/C/D invariants)."""
        page = self.session.page
        screenshot_path = extra.pop("screenshot_path", None)
        if screenshot_path is None:
            label = {
                "ErrGenerationTimeout": "playwright_timeout",
                "ErrNoImageCandidate": "no_image_candidate",
            }.get(code, f"exception_{code}")
            screenshot_path = _screenshot_on_failure(page, label)
        elif screenshot_path == "":
            # Fix D: explicit suppression.
            pass

        diag_payload = {
            "error_code": code,
            "error_message": error_message,
            "screenshot_path": screenshot_path or "",
        }
        diag_payload.update(extra)
        _log_diag(
            ctx.request_id, self.profile_id, "error",
            url=page.url if page else "<closed>",
            **diag_payload,
        )
        _log(
            f"[profile-{self.profile_id}][{ctx.request_id}] error ({code}): "
            f"{error_message or '<no message>'}"
        )

        try:
            self.session.fresh_page()
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
        for k, v in extra.items():
            if k not in response:
                response[k] = v
        if traceback_str:
            response["traceback"] = traceback_str
            response["screenshot_path_in_err"] = screenshot_path or ""
        return response

    def _build_success_response(self, ctx: GenerationContext) -> dict:
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

    # ── Public entry: thin orchestrator ───────────────────────────────

    def run(self, req: dict) -> dict:
        """One generation lifecycle. Thin orchestrator — delegates step
        bodies to the 7 `_step_*` methods and centralises error
        capture in `_emit_failed_response`.

        Wire compatibility: returns the canonical response dict shape
        (success or typed-error), preserving byte-byte what the
        pre-fix `ProfileWorker._generate()` had.
        """
        ctx = self._build_context(req)  # also emits `start`.

        # Login pre-check (Fix B): AFTER `start` emit, BEFORE any DOM
        # mutation. Response shape preserves the original pre-fix
        # early-return: no canonical typed fields (no
        # elapsed_ms / candidates_*) since the request never started.
        page = self.session.page
        if page is not None and "accounts.google.com" in page.url:
            return {
                "id": ctx.request_id, "status": "error",
                "error": "login required: user is logged out "
                "(please run scripts/bridges/login.py to sign in)",
                "profile": self.profile_id,
            }

        os.makedirs(
            os.path.dirname(os.path.abspath(ctx.output_path)) or ".", exist_ok=True
        )
        ctx.t0 = time.time()

        # The 7 step methods are delegated in the canonical order.
        # Each method maps to one line of the wave-plan § "Generation
        # order" sequence (the user spec's high-level flow):
        #   ensure_authenticated   — pre-step login guard above
        #   dom.prepare            — _step_prepare_surface
        #   dom.activate_image_mode — _step_prepare_surface (1b)
        #   dom.set_prompt         — _step_fill_prompt
        #   dom.select_ratio       — _step_select_ratio
        #   snapshot_baseline      — _step_refresh_baseline (a)
        #   clear_panel_if_required — _step_refresh_baseline (b)
        #   dom.submit             — _step_submit
        #   poll_for_new_candidate — _step_poll_for_candidate
        #   extract_candidate      — _step_extract_image
        #   compute_pixel_stats    — _step_extract_image (inline)
        #   session.persist        — orchestrator-level end-of-success
        #   session.recycle_if_needed — orchestrator-level end-of-success
        #   build_success_response — _build_success_response
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
            # `exception_<type(e).__name__>` label BEFORE invoking
            # the helper, so the filename matches pre-fix byte-for-byte.
            captured_screenshot = _screenshot_on_failure(
                page, f"exception_{type(e).__name__}"
            )
            return self._emit_failed_response(
                ctx, "ErrUnknown",
                error_message=f"{type(e).__name__}: {e}",
                traceback_str=traceback.format_exc(),
                screenshot_path=captured_screenshot,
            )

        # Success path: lifecycle side effects owned by the
        # orchestrator (NOT the steps) so worker-internal cleanup is
        # centralised.
        self.session.recycle_if_needed()
        try:
            self.session.persist(request_id=ctx.request_id, reason="auto-save")
        except Exception as se:
            _log(
                f"[profile-{self.profile_id}][{ctx.request_id}] "
                f"failed to auto-save cookies: {se}"
            )

        return self._build_success_response(ctx)
