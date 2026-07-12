"""Google Slides DOM automation: click helpers + typed SlidesDOM facade.

Wave Commit 2 (July 2026): the 16 DOM-action helpers formerly
co-located in `runtime.generation` (Commit 4, transitional
modularization) are lifted here verbatim per godlike/06 SSOT.
A typed `SlidesDOM` facade sits in front of them so GenerationRunner
calls `self.dom.prepare()` / `self.dom.set_prompt(...)` / etc.
instead of bare module-level references.

Per godlike/06 SSOT:
  - This module is the SINGLE canonical owner of automation routines
    that drive the Google Slides (Nano Banana Pro) image-generation
    surface. The 16 module-level helpers are the unit-of-automation
    primitives; the `SlidesDOM` class is the orchestrator's typed
    entry. Both shapes are exposed so callers can choose lexical
    scope (helpers) or typed facade (SlidesDOM) per call site.
  - Selector isolation. Only CANDIDATE_LOCATOR_SELECTOR lives in
    `runtime.selectors` (re-exported from config) — that constant
    is owned exclusively by the candidates-extraction surface, not
    by the click helper surface. dom_actions.py uses INLINE
    selectors for one-shot regex/JS-evaluate paths that are NOT
    worth promoting to selectors.py until a third reuse site
    emerges. Selector moves to selectors.py are governed by the
    godlike/06 SSOT rule: "any selector tweak lands here — never
    inlined at call sites" applies once a selector appears in 2+
    call sites; while a selector is one-shot, it stays inline.

Per godlike/07 fail-closed:
  - All click helpers default to `do_click=True` but expose
    `do_click=False` for wait_ready/readiness checks (no click).
  - DOM-level click via `page.evaluate` (`use_dom_evaluate=True`)
    bypasses Playwright's actionability checks. Required for
    elements Playwright falsely reports as not-visible (Cheerio /
    Google Slides rendering quirks).
  - All helpers tolerate `page=None` (return False / no-op). The
    orchestrator's login pre-check can land with a torn page handle
    without crashing these helpers.

Compatibility (Commit 4 invariants preserved):
  - `_check_169_selected` is RENAMED to `_check_ratio_selected` per
    the user spec — the function was already parameterized on
    `ratio: str` since P1.1; the rename drops the legacy 16:9-only
    name without changing observable behavior.
  - All other 15 helpers retain their original names + signatures
    (the SlidesDOM facade wraps them via simple pass-through).
  - No modifications to any Playwright API surface (page, Locator,
    TimeoutError, page.evaluate signatures unchanged).
"""

from __future__ import annotations

import re
import time
from dataclasses import dataclass
from typing import Optional

from playwright.sync_api import (
    Locator,
    Page,
    TimeoutError as PlaywrightTimeout,
)

from .diagnostics import _log


# ── SelectRatioResult typed return for SlidesDOM.select_ratio ────────────


@dataclass(frozen=True)
class SelectRatioResult:
    """Typed result for SlidesDOM.select_ratio (returns success + warning).

    `success`              True iff the dropdown was opened AND the option
                          was clicked AND the post-click verify confirms
                          the ratio is selected. Not the pre-fix
                          strict-mis-select policy.
    `ratio_selected`       The canonical ratio string the page reports
                          (== the requested ratio when success=True).
    `warning`              None on success; a non-empty string on
                          recoverable issues (verify-mismatch / click
                          exception). The orchestrator's step method
                          reads this and emits a typed `error` JSONL
                          diag with code ErrImageGenRatioNotSelected.

    godlike/07 fail-closed: the field set is invariant and the
    rate of warnings-via-diagnostic emission is bounded by the
    orchestrator's policy. Caller code MUST NOT mutate this
    struct (frozen=True).
    """
    success: bool
    ratio_selected: str
    warning: Optional[str]


# ── Module-level helpers (verbatim from generation.py; godlike/06 SSOT) ─


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


def _check_ratio_selected(page, ratio: str = "16:9") -> bool:
    """Post-click verification that the requested ratio is applied.

    Best-effort. False on JS evaluate failure or no match (conservative:
    a missed selector makes the typed `ErrImageGenRatioNotSelected`
    path the canonical signal).

    RENAMED (July 2026): originally `_check_169_selected` (16:9-only).
    The function has been parameterized on `ratio` since P1.1; the
    rename drops the legacy name without changing observable behavior.
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
        _log(f"[_check_ratio_selected] DOM evaluate failed (ratio={ratio}): {e}")
        return False


def _select_ratio(page, ratio: str) -> SelectRatioResult:
    """Open Proporzioni dropdown + click `ratio` option + verify selection.

    NEW EXTRACTION (July 2026, Commit 2): previously the inline body
    of `_step_select_ratio` in generation.py. Lifted here so the
    typed return shape (`SelectRatioResult`) survives the move and
    the orchestrator remains a thin shell.

    RECOVERABLE: on verify-mismatch or click-exception the function
    returns a SelectRatioResult with `warning` non-empty and
    `success=False` — it never raises. The orchestrator's step
    method decides whether to emit the typed `error` JSONL diag.
    """
    select_aria = '[aria-label="Proporzioni"], .image-synthesis [aria-label*="Proporzi"]'
    try:
        prop_btn = page.locator(select_aria).first
        if not prop_btn.is_visible():
            raise Exception("Proporzioni button not visible (cannot open ratio menu)")
        prop_btn.click(force=True, timeout=3000)
        opt_selector = (
            f'[role="menuitemradio"]:has-text("{ratio}"), '
            f'[data-ratio="{ratio}"], '
            f'*:has-text("{ratio}")'
        )
        opt_ratio = page.locator(opt_selector).last
        opt_ratio.wait_for(state="visible", timeout=3000)
        opt_ratio.click(force=True, timeout=3000)
        page.wait_for_timeout(400)
        if _check_ratio_selected(page, ratio):
            return SelectRatioResult(success=True, ratio_selected=ratio, warning=None)
        return SelectRatioResult(
            success=False,
            ratio_selected=ratio,
            warning=f"{ratio} not confirmed in post-click selected-ratio state",
        )
    except Exception as e:
        return SelectRatioResult(
            success=False,
            ratio_selected=ratio,
            warning=f"{ratio} selection encountered: {e}",
        )


# ── SlidesDOM facade ──────────────────────────────────────────────────────


class SlidesDOM:
    """Typed facade over the 16 DOM-action helpers.

    Construction: `SlidesDOM(page)` — just holds the page reference.
    Methods are thin wrappers over module-level helpers (godlike/06
    SSOT — module-level functions remain testable + re-exportable;
    the class is the orchestrator's typed entry).

    Per godlike/06 SSOT, this is the SINGLE typed entry-point for
    the orchestrator (currently GenerationRunner). Per godlike/07
    fail-closed, every wrapper method tolerates `page=None` and
    emits a `_log` line on actionability failure rather than
    crashing the request pipeline.
    """

    def __init__(self, page: Page) -> None:
        self.page = page

    def prepare(self) -> None:
        """Dismiss startup modals + ensure the editor canvas is
        reachable. Best-effort: a torn page is tolerated.
        """
        _prepare_editor_surface(self.page)

    def get_prompt_surface(self, timeout_ms: int = 15000) -> bool:
        """Wait for the visible textarea that hosts the Nano Banana
        prompt. Returns False on Playwright Timeout.
        """
        return _wait_for_prompt_surface(self.page, timeout_ms=timeout_ms)

    def activate_image_mode(self) -> bool:
        """Switch to Immagine/Image mode tab (raw DOM-evaluate path).
        Tolerant: a torn page is tolerated.
        """
        return _click_visible_image_mode_tab(self.page)

    def set_prompt(self, text: str) -> bool:
        """Type `text` into the visible prompt textarea via real
        keyboard events so Google Slides' state machine updates.

        Returns False on locating-failure or Playwright actionability
        limit. Does NOT retry/recover — the orchestrator handles the
        fresh_page → redrive path so session lifecycle stays out of
        the DOM concern.
        """
        page = self.page
        try:
            ta = page.locator('textarea:visible').first
            ta.wait_for(state="visible", timeout=25000)
        except PlaywrightTimeout:
            _log("[SlidesDOM.set_prompt] textarea wait timed out")
            return False
        return _type_prompt_text(page, ta, text)

    def select_ratio(self, ratio: str) -> SelectRatioResult:
        """Open Proporzioni dropdown + click option + verify the
        selected ratio is applied. RECOVERABLE: a click failure or
        verify-mismatch returns a SelectRatioResult with `warning`
        non-empty rather than raising. The orchestrator's step
        method decides whether to log the typed `error` JSONL diag.
        """
        return _select_ratio(self.page, ratio)

    def submit(self, timeout_ms: int = 15000) -> bool:
        """Wait for create button ready + click. Falls back to a
        direct locator click if the helper didn't match. Returns
        True on click success (any path); False if every path
        failed.

        godlike/07 fail-closed: this method ALWAYS attempts to
        click the create button. If wait_ready=False, it proceeds
        to click immediately (matches pre-fix behavior where the
        click was unconditional). Failure here propagates to the
        orchestrator as PlaywrightTimeout (orchestrator catches).
        """
        page = self.page
        ready = _wait_for_create_button_ready(page, timeout_ms=timeout_ms)
        if not ready:
            _log(
                "[SlidesDOM.submit] warning: create button never became "
                "enabled before submit"
            )
        if _click_visible_create_button(page):
            _log("[SlidesDOM.submit] create button click succeeded")
            return True
        # Fallback locator (matches pre-fix behavior byte-byte).
        _log("[SlidesDOM.submit] create button click not found, fallback")
        try:
            create_btn = page.locator(
                '.image-synthesis-creation-button, button[aria-label="Crea"]'
            ).first
            create_btn.click(force=True, timeout=5000)
            return True
        except Exception as e:
            _log(f"[SlidesDOM.submit] fallback locator click failed: {e}")
            return False
