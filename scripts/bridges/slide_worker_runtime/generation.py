"""Generation pipeline: GenerationContext + StepError + GenerationRunner.

Wave Commit 4 (July 2026): captures the 7 step-method decomposition
from ProfileWorker (`refactor(slide_worker): split _generate into 7
step methods and thin orchestrator`, July 2026) plus the typed
GenerationContext + StepError. Behaviour preservation is the
contract — see `_step_*` docstrings.

LAYER SPLIT (split-generation-by-layer, July 2026): the 7 step
methods + the `_build_context` request-template composition have
been extracted by layer into sibling modules:

  - generation_template.py  — `build_context(req, session, profile_id)`
                               prompt composition + phase #1 `start`
                               JSONL diag emission (Fix B invariant).
  - generation_dom.py       — DOM-side step bodies
                               (step_prepare_surface, step_fill_prompt,
                               step_select_ratio, step_submit).
                               Each function delegates to the
                               SlidesDOM typed facade (Commit 2).
  - generation_persist.py   — persistence-side step bodies
                               (step_refresh_baseline,
                               step_poll_for_candidate,
                               step_extract_image). Owns candidate
                               extraction, polling, image download,
                               disk save, and pixel-quality metric
                               emission.

This file retains the canonical PUBLIC bridge surface so the wire
contract remains bit-identical from the pre-split
`ProfileWorker._generate()` output:

  - `GenerationContext` — single typed per-request mutable-state bundle.
  - `StepError`         — single typed orchestrator failure surface.
  - `GenerationRunner` — thin orchestrator that owns ONE generation
                         lifecycle; `run(req) -> dict` is the canonical
                         entry. The class exposes:
                           - `__init__(session, profile_id)` (composed
                             per-ProfileWorker via `dispatcher.py`),
                           - `dom` lazy property,
                           - `_emit_failed_response` (Fix B/C/D shim),
                           - `_build_success_response` (success shape),
                           - `run(req)` (thin orchestrator).

godlike/06 SSOT: the public symbols defined here are owned by this
file. Layer modules expose FREE FUNCTIONS, not methods. The pattern
lets future agents target a single layer without dragging in the
runner-class ergonomic surface; the wire shape is unchanged because
the layer functions are invoked from `run()` only.

godlike/07 fail-closed: `_emit_failed_response` reproduces the
pre-fix screenshot policy exactly (Fix D: empty-string
`screenshot_path` suppresses the unsolicited screenshot capture;
Fix C: the exception class name drives the screenshot label for
`ErrUnknown` paths).
"""

from __future__ import annotations

import os
import time
import traceback
from typing import Optional

from playwright.sync_api import (
    Page,
    TimeoutError as PlaywrightTimeout,
)

from .diagnostics import _log, _log_diag, _screenshot_on_failure
from .dom_actions import SlidesDOM
from .session import BrowserSession

from .generation_dom import (
    step_fill_prompt,
    step_prepare_surface,
    step_select_ratio,
    step_submit,
)
from .generation_persist import (
    step_extract_image,
    step_poll_for_candidate,
    step_refresh_baseline,
)
from .generation_template import build_context
from .generation_types import GenerationContext, StepError


# ── GenerationRunner (thin orchestrator) ───────────────────────────────────


class GenerationRunner:
    """Thin orchestrator that owns ONE generation lifecycle.

    Composed per-ProfileWorker via `__init__(session, profile_id)`.
    The runner does NOT own BrowserSession (that's the per-thread
    Playwright handle); it BORROWS the page from the session for the
    duration of one request.

    Layer split (July 2026): the 7 step delegates + the
    `_build_context` request-template composition have moved to the
    layer modules (`generation_template`, `generation_dom`,
    `generation_persist`). The runner still drives the sequence from
    `run()` and centralises typed-error emission in
    `_emit_failed_response`; the layer modules do not depend on
    `GenerationRunner`. Free-function signature shape across the
    layer modules is `(session, ctx, profile_id) -> None` so the
    orchestrator's call sites stay short + uniform.

    Public surface (preserved from pre-split):
      run(req) -> dict   — the canonical entry. Returns the response
                           dict (success OR typed-error shape). Wire-
                           shape compatibility is preserved byte-byte
                           from the pre-fix `ProfileWorker._generate()`
                           output.
      dom (property)     — lazy SlidesDOM facade bound to the current
                           session.page. Each access is cheap (just
                           `SlidesDOM(page)` — no I/O); recomputed on
                           each access so the facade follows
                           `session.fresh_page()`'s page reset.
    """

    def __init__(self, session: BrowserSession, profile_id: int) -> None:
        self.session = session
        self.profile_id = profile_id

    @property
    def dom(self) -> SlidesDOM:
        """Lazily constructed SlidesDOM facade bound to the current
        session.page. Recomputed on each access so the facade
        follows session.fresh_page()'s page reset (thinker #3).

        godlike/06 SSOT: the facade is the SINGLE typed entry-point
        for the orchestrator's DOM-bound side effects. Each access
        is cheap (just SlidesDOM(page) — no I/O).
        """
        return SlidesDOM(self.session.page)

    # ── Orchestrator-level helpers ─────────────────────────────────────

    def _emit_failed_response(self, ctx: GenerationContext, code: str,
                              *, error_message: str = "",
                              traceback_str: str = "",
                              **extra) -> dict:
        """Common fail-closed shim (Fix B/C/D invariants)."""
        page: Optional[Page] = self.session.page
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
        bodies to the layer modules (`generation_template.build_context`
        + `generation_dom.step_*` + `generation_persist.step_*`) and
        centralises error capture in `_emit_failed_response`.

        Wire compatibility: returns the canonical response dict shape
        (success or typed-error), preserving byte-byte what the
        pre-fix `ProfileWorker._generate()` had.
        """
        ctx = build_context(req, self.session, self.profile_id)

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

        # The 7 step delegates are invoked via the layer modules. Each
        # call site maps to a single line of the wave-plan § "Generation
        # order" sequence (the user spec's high-level flow):
        #   ensure_authenticated   — login pre-check above
        #   dom.prepare            — step_prepare_surface  (Step 1)
        #   dom.activate_image_mode — step_prepare_surface (1b)
        #   dom.set_prompt         — step_fill_prompt   (Step 2)
        #   dom.select_ratio       — step_select_ratio  (Step 3)
        #   snapshot_baseline      — step_refresh_baseline (4a)
        #   clear_panel_if_required — step_refresh_baseline (4b)
        #   dom.submit             — step_submit        (Step 5)
        #   poll_for_new_candidate — step_poll_for_candidate (Step 6)
        #   extract_candidate      — step_extract_image  (Step 7)
        #   compute_pixel_stats    — step_extract_image (inline)
        #   session.persist        — orchestrator-level post-success
        #   session.recycle_if_needed — orchestrator-level post-success
        #   build_success_response — _build_success_response
        try:
            step_prepare_surface(self.session, ctx, self.profile_id)
            step_fill_prompt(self.session, ctx, self.profile_id)
            step_select_ratio(self.session, ctx, self.profile_id)
            step_refresh_baseline(self.session, ctx, self.profile_id)
            step_submit(self.session, ctx, self.profile_id)
            step_poll_for_candidate(self.session, ctx, self.profile_id)
            step_extract_image(self.session, ctx, self.profile_id)
        except StepError as e:
            extra = dict(e.diag_extra)
            # Step layers historically put the human-readable error in
            # diag_extra. Do not pass that key twice to the typed helper:
            # timeout/error paths must remain serializable even when the
            # layer supplied no separate StepError.error_message.
            step_error_message = e.error_message or str(extra.pop("error_message", ""))
            if e.screenshot_path is not None:
                extra.setdefault("screenshot_path", e.screenshot_path)
            return self._emit_failed_response(
                ctx, e.code, error_message=step_error_message, **extra,
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
