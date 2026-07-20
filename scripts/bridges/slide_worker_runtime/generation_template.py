"""Template layer — request composition + start-diag emission.

LAYER SPLIT (split-generation-by-layer, July 2026): extracted from
`GenerationRunner._build_context` in
`scripts/bridges/slide_worker_runtime/generation.py`. The
template-layer responsibility is purely the per-request state-bundle
derivation + the `start` JSONL phase emission (Fix B invariant).

free-function style: `build_context(req, session, profile_id) ->
GenerationContext` is the layer's single typed output. The
orchestrator's `run()` invokes it and treats the returned value as
the immutable-then-mutable state bundle passed to the subsequent
step delegates.

godlike/06 SSOT: this file is the SINGLE canonical owner of:

  - prompt composition logic (P1.1 negative-keyword auto-compose,
    P1.2 prompt-composer trust, anti-double-affix guard for the
    `[negative: do not include...]` Go-side directive).
  - phase #1 `start` diag emission timing (BEFORE the login pre-check
    in the orchestrator's `run()`, BEFORE any DOM mutation so
    login-failing requests still get a `start` line — Fix B).

godlike/07 fail-closed: the auto-compose guard for `[negative: do
not include...]` preserves the P1.1 wire-protocol invariant —
callers MUST NOT pre-compose the negative keywords AND rely on the
worker to auto-compose. The guard detects the pre-composed form
and skips the worker-side composition to avoid double-affix.
"""

from __future__ import annotations

from .diagnostics import _log, _log_diag
from .generation import GenerationContext
from .session import BrowserSession


def build_context(req: dict, session: BrowserSession, profile_id: int) -> GenerationContext:
    """Derive the mutable per-request state bundle from the inbound req.

    P1.2 prompt-composer trust: prompt arrives WHOLE from Go, no
    worker-side truncation. P1.1 negative-keyword auto-compose
    only when caller does NOT pre-compose (no
    `[negative: do not include...]` directive).

    Emits the `start` phase BEFORE the login pre-check so login-
    failing requests still get a `start` line for forensic
    correlation (Fix B).
    """
    request_id = req.get("id", "")
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
        f"[profile-{profile_id}][{request_id}] prompt accepted whole from Go "
        f"(len={len(prompt)}, original_len={len(original_prompt)}, composed_no_truncation=true)"
    )

    # P2 phase #1: start. Emitted BEFORE any DOM action AND BEFORE
    # the login pre-check so login-failing requests still get the
    # receipt marker (Fix B).
    page = session.page
    _log_diag(
        request_id, profile_id, "start",
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
