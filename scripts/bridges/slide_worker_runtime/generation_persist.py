"""Persistence layer — candidates extraction + polling + image save.

LAYER SPLIT (split-generation-by-layer, July 2026): extracted from the
3 persist-side step methods of `GenerationRunner`. Each function
takes `(session, ctx, profile_id)` and operates on
`session.page` (Playwright Locator) and `ctx` (mutable state bundle).

Step mapping (`run()` call site → layer function):

  Run Step 4 → step_refresh_baseline  — re-extract baseline after
                                        sidebar expansion + optional
                                        panel-clear (gated by
                                        `SLIDE_WORKER_REFRESH_EVERY`).
  Run Step 6 → step_poll_for_candidate — 60s poll loop with the P0.4
                                        filter (src-not-in-baseline +
                                        complete=True + dims >= 64x64).
                                        On timeout → raises
                                        `StepError("ErrGenerationTimeout")`.
  Run Step 7 → step_extract_image      — P0.4.A src-anchored Locator +
                                        googleusercontent / blob: fetch
                                        path + disk save +
                                        `_compute_pixel_stats` +
                                        richer diag emissions.

godlike/06 SSOT: this file is the SINGLE canonical owner of:

  - The P0.4 filter invariants (src-not-in-baseline +
    complete=True + dims >= 64x64) at the poll loop.
  - The P0.4.A src-anchored Locator strategy (`img[src="X"]` looks
    up the chosen candidate via its captured src to survive DOM
    redraws between poll and extract).
  - The image-fetch fallback chain:
      googleusercontent.request.get → blob: window.fetch proxy →
      blob: img.screenshot → raise typed `ErrNoImageCandidate`.
  - The disk-save + pixel-quality-metric emission chain
    (`_save_image_bytes` + `_compute_pixel_stats`).

godlike/07 fail-closed: all `ErrNoImageCandidate` paths pass
`screenshot_path=""` (Fix D), so the orchestrator's
`_emit_failed_response` skips the unsolicited screenshot capture
that would otherwise pollute the operators' filesystem with
`exception_ErrNoImageCandidate_<timestamp>` captures.

Free-function signature shape (uniform across the layer):

    def step_X(session: BrowserSession, ctx: GenerationContext,
               profile_id: int) -> None

The orchestrator passes `self.profile_id` from `GenerationRunner`.
"""

from __future__ import annotations

import os
import time

from .candidates import _extract_candidates as _runtime_extract_candidates
from .config import CANDIDATE_LOCATOR_SELECTOR, SLIDE_WORKER_REFRESH_EVERY
from .diagnostics import _log, _log_diag, _screenshot_on_failure
from .generation import GenerationContext, StepError
from .image_quality import _compute_pixel_stats, _save_image_bytes
from .session import BrowserSession


def step_refresh_baseline(session: BrowserSession, ctx: GenerationContext,
                          profile_id: int) -> None:
    """Step 4: re-extract baseline AFTER sidebar expansion + optional
    panel-clear (SLIDE_WORKER_REFRESH_EVERY gate).
    """
    page = session.page
    # Use the runtime.candidates re-export of `_extract_candidates`
    # — same function (Commit 3 moved the original) but the path
    # through runtime.candidates is the single canonical owner.
    ctx.baseline_candidates = _runtime_extract_candidates(page, max_keep=200)
    ctx.baseline_src_set = {c.get("src", "") for c in ctx.baseline_candidates}
    _log(
        f"[profile-{profile_id}][{ctx.request_id}] refreshed baseline "
        f"before submit: {len(ctx.baseline_src_set)} candidate src(s)"
    )

    # P1.3 (July 2026): SLIDE_WORKER_REFRESH_EVERY gate.
    from .candidates import _clear_image_library_panel as _runtime_clear_panel
    session._refresh_count += 1
    if session._refresh_count % SLIDE_WORKER_REFRESH_EVERY == 0:
        cleared = _runtime_clear_panel(page)
        _log_diag(
            ctx.request_id, profile_id, "panel_cleared",
            url=page.url, removed=cleared,
            refresh_count=session._refresh_count,
        )


def step_poll_for_candidate(session: BrowserSession, ctx: GenerationContext,
                            profile_id: int) -> None:
    """Step 6: 60s poll loop. P0.4 filter rejects src-in-baseline +
    incomplete (loading) + thumbnail (dims<64x64) candidates.

    On timeout raises StepError("ErrGenerationTimeout").
    """
    page = session.page
    # P2 phase #4: polling_start.
    _log(
        f"[profile-{profile_id}][{ctx.request_id}] waiting for AI generation "
        f"(P0.4 filter against baseline={len(ctx.baseline_src_set)}, "
        f"min_dims=64x64, complete=True)..."
    )
    _log_diag(ctx.request_id, profile_id, "polling_start", url=page.url)

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
            f"[profile-{profile_id}][{ctx.request_id}] poll t={waited}s "
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
                    f"[profile-{profile_id}][{ctx.request_id}] P0.4 candidate matched: "
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
                    ctx.request_id, profile_id, "candidate_found",
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
        ctx.request_id, profile_id, "error",
        url=page.url if page else "<closed>",
        error_code="ErrGenerationTimeout",
        error_message=f"timed out after {max_wait}s",
        elapsed_ms=int((time.time() - ctx.t0) * 1000),
        screenshot_path=screenshot_path or "",
    )
    _log(
        f"[profile-{profile_id}][{ctx.request_id}] timed out waiting for AI after {max_wait}s"
    )
    raise StepError(
        "ErrGenerationTimeout",
        screenshot_path=screenshot_path,
        diag_extra={"error_message": f"timed out after {max_wait}s"},
        candidates_baseline=len(ctx.baseline_candidates),
        elapsed_ms=int((time.time() - ctx.t0) * 1000),
    )


def step_extract_image(session: BrowserSession, ctx: GenerationContext,
                       profile_id: int) -> None:
    """Step 7: P0.4.A src-anchored Locator + googleusercontent/blob:
    fetch + save + emit richer `candidate_found`, `fetch_method_choice`,
    `saved`, and `end` diags.

    Raises StepError on any of three typed failure paths
    (empty src / vanished candidate / no-fetch). Per Fix D, all
    `ErrNoImageCandidate` paths pass `screenshot_path=""` so
    `_emit_failed_response` skips the unsolicited screenshot.
    """
    page = session.page
    cached_meta = ctx.matched_candidate_meta
    if cached_meta is None:
        _log(
            f"[profile-{profile_id}][{ctx.request_id}] reached Step 7 with "
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
            f"[profile-{profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
            f"matched_candidate_meta.src is empty"
        )
        _log_diag(
            ctx.request_id, profile_id, "error",
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
            f"[profile-{profile_id}][{ctx.request_id}] P0.4.A fail-closed: "
            f'img[src="{captured_src[:80]}"] not found in current DOM '
            f"(vanished mid-extraction); surfacing typed ErrNoImageCandidate"
        )
        _log_diag(
            ctx.request_id, profile_id, "error",
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
        ctx.request_id, profile_id, "candidate_found",
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
        f"[profile-{profile_id}][{ctx.request_id}] P0.4 found {len(imgs)} "
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
                            f"[profile-{profile_id}][{ctx.request_id}] blob: "
                            f"window.fetch proxy-on-page succeeded "
                            f"({len(ctx.image_bytes)} bytes)"
                        )
                except Exception as fe:
                    _log(
                        f"[profile-{profile_id}][{ctx.request_id}] blob: "
                        f"window.fetch proxy-on-page failed: {fe}"
                    )
                if not ctx.image_bytes:
                    try:
                        ctx.image_bytes = img.screenshot(type="png", timeout=5000)
                        ctx.fetch_method = "element-screenshot"
                        _log(
                            f"[profile-{profile_id}][{ctx.request_id}] blob: "
                            f"element-screenshot fallback succeeded "
                            f"({len(ctx.image_bytes)} bytes)"
                        )
                    except Exception as se:
                        _log(
                            f"[profile-{profile_id}][{ctx.request_id}] blob: "
                            f"element-screenshot fallback failed: {se}"
                        )

            if ctx.image_bytes:
                ctx.saved_format = _save_image_bytes(ctx.image_bytes, ctx.output_path)
                elapsed = (time.time() - ctx.t0) * 1000
                _log(
                    f"[profile-{profile_id}][{ctx.request_id}] SUCCESS → "
                    f"{ctx.output_path} ({len(ctx.image_bytes)} bytes, "
                    f"{ctx.saved_format}, {elapsed:.0f}ms, method={ctx.fetch_method})"
                )

                _log_diag(
                    ctx.request_id, profile_id, "fetch_method_choice",
                    url=page.url, method=ctx.fetch_method,
                    natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                    complete=ctx.complete, elapsed_ms=int(elapsed),
                )
                _log_diag(
                    ctx.request_id, profile_id, "saved",
                    url=page.url, output_path=ctx.output_path,
                    method=ctx.fetch_method, bytes=len(ctx.image_bytes),
                    format=ctx.saved_format,
                    natural_w=ctx.natural_w, natural_h=ctx.natural_h,
                )

                ctx.candidate_records = candidate_records
                ctx.saved = True

                ctx.pixel_stats = _compute_pixel_stats(ctx.output_path)
                _log_diag(
                    ctx.request_id, profile_id, "end",
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
                f"[profile-{profile_id}][{ctx.request_id}] extraction "
                f"attempt failed: {e}"
            )
            continue

    # P0.1 fail-closed: no slide-export fallback.
    _log_diag(
        ctx.request_id, profile_id, "error",
        url=page.url,
        error_code="ErrNoImageCandidate",
        error_message="no googleusercontent/blob candidates could be fetched",
        candidates_after=len(imgs),
        screenshot_path="",
    )
    _log(
        f"[profile-{profile_id}][{ctx.request_id}] extraction failed — "
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
