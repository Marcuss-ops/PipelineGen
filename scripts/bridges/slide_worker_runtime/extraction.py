"""Image-byte extraction: googleusercontent fetch, blob fetch, element screenshot.

Commit 3 introduces the typed forward-compat extraction surface for
the slide_worker.py wave. The legacy inline extraction logic inside
`ProfileWorker._step_extract_image` is NOT yet migrated in this
commit — that wires in at Commit 4 (session.py + generation.py +
dispatcher.py). Commit 3 only ENSURES the helpers exist with the
correct ordering + typed return shapes so Commit 4's wiring is
mechanical (search-replace the inline block with a call to
`extract_candidate`).

PER WAVE PLAN §9 INVARIANTS (godlike/06 SSOT, godlike/07 fail-closed):
  * Strategy order is FIXED and non-overridable:
        1. googleusercontent → page.request.get(src)
        2. blob:             → page.evaluate(window.fetch) on the page
        3. blob: fallback    → locator.screenshot() on the img only
  * NEVER re-introduce `page.screenshot()` of the FULL slide.
  * NEVER re-introduce File→Download→PNG (i.e. filesystem-shelled
    download + PIL open + resave).
  * NEVER re-introduce slide export (the worker's pre-P0.1 fallback
    that exported the entire Slides page to PNG and reused the
    cropped impression as a "candidate image" — explicitly retired).

The above invariants are codified in `extract_candidate`'s strategy
branching; the helper is the ONLY path from a captured src to bytes
on disk. Test contract:

  * googleusercontent src + 200 → bytes via page.request.get
  * googleusercontent src + non-200 → fallback n/a (raises
    NoImageCandidateError wrapped via extract_candidate raising).
  * blob: src + window.fetch OK → bytes via the page-context proxy
  * blob: src + window.fetch raises → img.screenshot() fallback
  * blob: src + window.fetch AND img.screenshot() both fail →
    NoImageCandidateError

The byte_count, method, saved_format are recorded in the typed
`ExtractedImage` dataclass for the JSONL `saved` phase emission
and the typed success response shape.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Optional

from playwright.sync_api import Page, Locator, Response

from .candidates import ImageCandidate, NoImageCandidateError, _safe_url
from .diagnostics import _log
from .image_quality import _save_image_bytes


# ── Typed extraction model ────────────────────────────────────────────────


@dataclass(frozen=True)
class ExtractedImage:
    """Typed result of one successful image-byte extraction.

    Fields mirror the context fields populated by the legacy inline
    `_step_extract_image` and used verbatim by the orchestrator's
    success-response builder:
      - output_path:    absolute filesystem path written
      - method:         "googleusercontent" | "blob-fetch" |
                         "element-screenshot"
      - byte_count:     len(image_bytes), for the JSONL `saved`
                         diagnostic and the response `bytes`
      - natural_width:  from the captured candidate metadata
      - natural_height: from the captured candidate metadata
      - complete:       HTMLImageElement `complete` = True at extract
      - saved_format:   "png" | "jpeg" | "webp" — MIRRORS the format
                         actually written by `_save_image_bytes`
    """
    output_path: str
    method: str
    byte_count: int
    natural_width: int
    natural_height: int
    complete: bool
    saved_format: str


# ── Fetch helpers (Commit 3 NEW surface) ──────────────────────────────────


_GOOGLECONTENT_TIMEOUT_MS = 15_000
_BLOB_FETCH_TIMEOUT_MS = 10_000
_ELEMENT_SCREENSHOT_TIMEOUT_MS = 5_000


def fetch_googleusercontent(page: Page, src: str) -> bytes:
    """Fetch the image bytes via page-context HTTP GET.

    googleusercontent URLs are stable public buckets; page.request.get
    uses the BROWSER session context (cookies, headers), so any
    auth-required sub-bucket still resolves correctly. This is the
    PRIMARY and fastest path for the common generated-image case.

    Returns the bytes; raises on non-200 or fetch error so the caller
    can fall through to alternative strategies (current code has no
    alternative for googleusercontent — non-200 raises
    NoImageCandidateError via extract_candidate).
    """
    if "googleusercontent" not in src:
        raise NoImageCandidateError(
            f"fetch_googleusercontent called with non-googleusercontent src={_safe_url(src)}"
        )
    try:
        response: Response = page.request.get(src, timeout=_GOOGLECONTENT_TIMEOUT_MS)
        if response.status != 200:
            raise NoImageCandidateError(
                f"fetch_googleusercontent: HTTP {response.status} for src={_safe_url(src)}"
            )
        return response.body()
    except NoImageCandidateError:
        raise
    except Exception as e:
        raise NoImageCandidateError(
            f"fetch_googleusercontent: request error for src={_safe_url(src)}: {e}"
        ) from e


def fetch_blob_in_page(page: Page, src: str) -> bytes:
    """Fetch a blob: URL via proxy-on-page (window.fetch inside page.evaluate).

    godlike/07 fail-closed note: `page.request.get()` does NOT resolve
    blob: URLs because the blob: URL's authority is the page context.
    Cascade:
      (a) `window.fetch(src)` inside page.evaluate — the page-context
          proxy has authority for blob: URLs.
      (b) If (a) raises or returns empty, the caller is expected to
          fall back to `screenshot_image_element` (which is the
          ONLY-sanctioned visual fallback — NEVER page.screenshot()
          whole slide).

    Returns the bytes; raises `NoImageCandidateError` on JS exception
    or empty result so the caller can fall through to screenshot_image_element.
    """
    if not src.startswith("blob:"):
        raise NoImageCandidateError(
            f"fetch_blob_in_page called with non-blob src={_safe_url(src)}"
        )
    try:
        buffer_int_list = page.evaluate(
            "url => fetch(url).then(r => r.arrayBuffer())"
            ".then(b => Array.from(new Uint8Array(b)))",
            src,
            timeout=_BLOB_FETCH_TIMEOUT_MS,
        )
        if isinstance(buffer_int_list, list) and buffer_int_list:
            return bytes(buffer_int_list)
        raise NoImageCandidateError(
            f"fetch_blob_in_page: empty array for src={_safe_url(src)}"
        )
    except NoImageCandidateError:
        raise
    except Exception as e:
        raise NoImageCandidateError(
            f"fetch_blob_in_page: window.fetch failed for src={_safe_url(src)}: {e}"
        ) from e


def screenshot_image_element(locator: Locator) -> bytes:
    """Element-scoped screenshot of the candidate img only.

    This is the FINAL fallback — when googleusercontent GET failed
    AND blob: window.fetch failed AND we still need bytes to commit.
    Per P0.4 (godlike/07 fail-closed, July 2026): the worker screenshotted
    only the img element (NOT the whole page), preserving the rendered
    pixel content without exporting the slide.

    Returns the PNG bytes from the locator. Raises
    `NoImageCandidateError` on screenshot timeout or Playwright error.
    The slide_worker.py ORCHESTRATOR is responsible for the godlike/
    /07 fail-closed NO EXPORT invariant — this helper does NOT know
    or care whether the locator is a slide or an img. The CALLER
    must pass an img-scoped locator (NOT page.locator(...).first
    with a generic approach).
    """
    try:
        return locator.screenshot(type="png", timeout=_ELEMENT_SCREENSHOT_TIMEOUT_MS)
    except Exception as e:
        raise NoImageCandidateError(
            f"screenshot_image_element: locator screenshot failed: {e}"
        ) from e


# ── Orchestrator (Commit 3 NEW) ────────────────────────────────────────────


def extract_candidate(
    page: Page,
    candidate: ImageCandidate,
    output_path: str,
) -> ExtractedImage:
    """Extract bytes for ONE candidate and save to disk.

    Strategy dispatch matches the wave plan §9 invariants:
      * "googleusercontent" in src → fetch_googleusercontent (primary)
      * "blob:" in src → fetch_blob_in_page; on raise → screenshot_image_element
      * Anything else → screenshot_image_element (no slide export)

    Order is FIXED: each stage's failure routes through
    NoImageCandidateError to the next; the last stage's failure
    surfaces NoImageCandidateError to the caller.

    Returns `ExtractedImage` describing what was written. Raises
    `NoImageCandidateError` on the final-stage failure so the
    slide_worker.py orchestrator's single-except handler converts to
    `_StepError("ErrNoImageCandidate", screenshot_path="")` per Fix D.
    """
    src = candidate.src
    if page is None:
        raise NoImageCandidateError("extract_candidate: page is None")
    if not src:
        raise NoImageCandidateError("extract_candidate: candidate.src is empty")

    bytes_: Optional[bytes] = None
    method = ""

    if "googleusercontent" in src:
        bytes_ = fetch_googleusercontent(page, src)  # may raise
        method = "googleusercontent"
    elif src.startswith("blob:"):
        try:
            bytes_ = fetch_blob_in_page(page, src)
            method = "blob-fetch"
        except NoImageCandidateError as e:
            _log(f"[extract_candidate] blob-fetch failed; trying element screenshot: {e}")
            # The img-scoped locator: same src-anchored anchor the
            # caller used for `candidate`; we re-resolve so the
            # locator object is fresh and not subject to staleness.
            blob_locator = page.locator(f'img[src="{src}"]').first
            bytes_ = screenshot_image_element(blob_locator)  # may raise
            method = "element-screenshot"
    else:
        # Non-googleusercontent, non-blob src — element screenshot is
        # the ONLY sanitised path. Page-level screenshot is FORBIDDEN
        # per godlike/06 SSOT.
        _log(f"[extract_candidate] unknown src scheme ({_safe_url(src)}); element screenshot fallback")
        unknown_locator = page.locator(f'img[src="{src}"]').first
        bytes_ = screenshot_image_element(unknown_locator)  # may raise
        method = "element-screenshot"

    if not bytes_:
        # All strategies returned empty bytes — fail closed.
        try:
            os.remove(output_path)
        except OSError:
            pass
        raise NoImageCandidateError(
            f"extract_candidate: no bytes for src={_safe_url(src)} (method={method})"
        )

    saved_format = _save_image_bytes(bytes_, output_path)

    # Forensic cleanup of partial writes if the saved format round-trip
    # ended up zero-byte (e.g. extension mismatch produced no file).
    try:
        size = os.path.getsize(output_path)
    except OSError:
        size = 0
    if size == 0:
        try:
            os.remove(output_path)
        except OSError:
            pass
        raise NoImageCandidateError(
            f"extract_candidate: saved file is zero-byte for src={_safe_url(src)} "
            f"(format={saved_format})"
        )

    return ExtractedImage(
        output_path=output_path,
        method=method,
        byte_count=len(bytes_),
        natural_width=candidate.natural_width,
        natural_height=candidate.natural_height,
        complete=candidate.complete,
        saved_format=saved_format,
    )
