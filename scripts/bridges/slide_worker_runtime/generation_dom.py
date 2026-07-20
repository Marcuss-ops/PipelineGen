"""DOM layer — page-side interactions via SlidesDOM facade.

LAYER SPLIT (split-generation-by-layer, July 2026): extracted from the
4 DOM-side step methods of `GenerationRunner`. Each function takes
`(session, ctx, profile_id)` and uses `SlidesDOM(session.page)` to
dispatch Playwright Locator clicks without leaking the page handle
outside this layer.

Step mapping (`run()` call site → layer function):

  Run Step 1 → step_prepare_surface   — dismiss startup dialogs +
                                        ensure prompt textarea visible
                                        + switch to Immagine/Image mode
                                        tab.
  Run Step 2 → step_fill_prompt       — type `prompt_text` into the
                                        textarea (with `fresh_page`
                                        recovery on textarea invisibility).
  Run Step 3 → step_select_ratio      — open Proporzioni dropdown +
                                        click `ratio` option
                                        (RECOVERABLE: emits a typed
                                        `error` JSONL diag on mismatch
                                        but does NOT raise).
  Run Step 5 → step_submit            — wait_ready + click create
                                        button via `SlidesDOM.submit()`.

godlike/06 SSOT: each step preserves the EXACT error semantics of
the pre-split method:

  - Step 1 + Step 2 → `PlaywrightTimeout` raise on prompt-surface
    invisibility. Step 1b's tab-click is tolerant (logs warning,
    continues if the tab was already selected from a prior request).
  - Step 3 → warns on ratio mis-select, does NOT raise. Pre-fix
    continuation policy preserved.
  - Step 5 → best-effort; consumes `SlidesDOM.submit()` with
    `timeout_ms=15000`.

Free-function signature shape (uniform across all 4 functions):

    def step_X(session: BrowserSession, ctx: GenerationContext,
               profile_id: int) -> None

The orchestrator passes `self.profile_id` from `GenerationRunner`.
The `profile_id` arg is required because the helpers
(`_log`, `_log_diag`) take it positionally and the per-request
logging prefixes `[profile-{profile_id}][{request_id}] ...`
cannot be derived from the `GenerationContext` (which is a typed
state bundle, NOT a runner state bundle).
"""

from __future__ import annotations

from playwright.sync_api import TimeoutError as PlaywrightTimeout

from .diagnostics import _log, _log_diag
from .dom_actions import SlidesDOM
from .generation import GenerationContext
from .session import BrowserSession


def step_prepare_surface(session: BrowserSession, ctx: GenerationContext,
                         profile_id: int) -> None:
    """Step 1: dismiss startup modals + ensure prompt textarea visible
    + switch to Immagine/Image mode tab.

    Commit 2 (dom_actions extract): delegates to
    SlidesDOM.prepare() + get_prompt_surface() + activate_image_mode().
    The orchestrator still owns the PlaywrightTimeout raise on
    prompt-surface invisibility; the image-mode-tab click is
    tolerant (a miss logs a warning and continues).
    """
    page = session.page
    dom = SlidesDOM(page)
    dom.prepare()

    # Step 1a: ensure prompt textarea visible.
    ta = page.locator('textarea:visible').first
    panel_open = False
    try:
        panel_open = ta.is_visible()
    except Exception:
        panel_open = False

    if not panel_open:
        panel_open = dom.get_prompt_surface(timeout_ms=15000)
        if not panel_open:
            raise PlaywrightTimeout(
                "prompt surface did not become visible after selecting Images"
            )

    # Step 1b: switch to Immagine/Image tab. Tolerant: a failure
    # here logs a warning and continues — tab might already be
    # selected from a prior request.
    try:
        if dom.activate_image_mode():
            _log(
                f"[profile-{profile_id}][{ctx.request_id}] image mode tab click succeeded"
            )
            ctx.image_mode_active = True
        else:
            _log(
                f"[profile-{profile_id}][{ctx.request_id}] image mode tab click not found"
            )
        page.wait_for_timeout(1000)
    except Exception as te:
        _log(
            f"[profile-{profile_id}][{ctx.request_id}] warning: "
            f"failed switching tab directly: {te}"
        )


def step_fill_prompt(session: BrowserSession, ctx: GenerationContext,
                     profile_id: int) -> None:
    """Step 2: ensure textarea visible (with fresh_page recovery) +
    type prompt_text via keyboard events. Emits `prompt_set` diag.

    Commit 2 (dom_actions extract): typing delegations to
    SlidesDOM.set_prompt(); recovery stays here (orchestrator
    concerns: session.fresh_page() is session-lifecycle, NOT
    DOM-lifecycle). The SlidesDOM lazy property follows the
    post-fresh_page page swap automatically.
    """
    page = session.page
    dom = SlidesDOM(page)
    ta = page.locator('textarea:visible').first
    try:
        ta.wait_for(state="visible", timeout=25000)
    except PlaywrightTimeout:
        _log(
            f"[profile-{profile_id}][{ctx.request_id}] textarea not visible — recovery"
        )
        session.fresh_page()
        dom.prepare()
        if not dom.get_prompt_surface(timeout_ms=15000):
            raise PlaywrightTimeout(
                "prompt surface did not become visible after recovery"
            )
        ta = page.locator('textarea:visible').first

    if not dom.set_prompt(ctx.prompt_text):
        raise Exception("failed to type prompt into visible textarea")

    # P2 phase #2: prompt_set.
    _log_diag(
        ctx.request_id, profile_id, "prompt_set",
        url=page.url, prompt_dom=ctx.prompt_text,
        image_mode_active=ctx.image_mode_active,
    )


def step_select_ratio(session: BrowserSession, ctx: GenerationContext,
                      profile_id: int) -> None:
    """Step 3: open Proporzioni dropdown + click `ratio` option.

    RECOVERABLE by design: a click failure or post-click mis-select
    emits a typed `error` JSONL diag with code
    `ErrImageGenRatioNotSelected` (preserves the audit-trail code
    path) but does NOT raise.

    Commit 2 (dom_actions extract): the click + verify dance
    lives in SlidesDOM.select_ratio() which returns a typed
    SelectRatioResult. The step function reads `warning` and emits
    the diag; ctx.ratio_selected is set to the requested ratio
    regardless of warning (preserves pre-fix continuation policy).
    """
    dom = SlidesDOM(session.page)
    result = dom.select_ratio(ctx.ratio)
    ctx.ratio_selected = ctx.ratio
    if result.warning:
        _log(
            f"[profile-{profile_id}][{ctx.request_id}] warning: "
            f"{ctx.ratio} selection encountered: {result.warning}; continuing"
        )
        _log_diag(
            ctx.request_id, profile_id, "error",
            url=session.page.url,
            error_code="ErrImageGenRatioNotSelected",
            error_message=result.warning,
        )


def step_submit(session: BrowserSession, ctx: GenerationContext,
                profile_id: int) -> None:
    """Step 5: wait for create button to be ready + click. Emits
    `click_create` diag.

    Commit 2 (dom_actions extract): the wait_ready + click +
    fallback locator sequence consolidates into
    SlidesDOM.submit().
    """
    page = session.page
    dom = SlidesDOM(page)
    dom.submit(timeout_ms=15000)

    # P2 phase #3: click_create.
    _log_diag(
        ctx.request_id, profile_id, "click_create",
        url=page.url, ratio_selected=ctx.ratio_selected,
        image_mode_active=ctx.image_mode_active,
    )
