"""Candidate selection against the Google Slides image-generation panel.

Migrates `_extract_candidates` and `_clear_image_library_panel` from
slide_worker.py verbatim. Introduces typed forward-compat helpers
(`snapshot_baseline`, `poll_for_new_candidate`, `anchor_candidate_by_src`)
that return the typed dataclasses `ImageCandidate` and
`CandidateBaseline`. Commit 4 (session + generation + dispatcher)
wires those NEW helpers into `GenerationRunner.run`, replacing the
inline polling/anchor logic that today lives inside
`ProfileWorker._step_poll_for_candidate` / `_step_extract_image`.

Per godlike/07 fail-closed (P0.4 + P0.4.A, July 2026):
  No success-on-vanished. A matched candidate whose src later
  disappears from the DOM raises `NoImageCandidateError` rather than
  silently returning the previous locator. The DOM may reorder
  between Step 6 (polling) and Step 7 (extract); the src-anchored
  locator `img[src="<captured-src>"]` is deterministic regardless
  of position — we never cache positional locators.

Per godlike/06 SSOT: `ImageCandidate` and `CandidateBaseline` are the
SINGLE canonical typed surface for "what is a generated-image
candidate in this Slides DOM?". Any future re-export of those
concepts goes through this module.

Per "no features" rule (AGENTS.md): the legacy `_extract_candidates`
keeps its dict-shaped return so the existing ProfileWorker code paths
(hundreds of `dict["src"]` lookups across `_build_generation_context`,
`_step_poll_for_candidate`, `_step_extract_image`) keep working
byte-identically until Commit 4 migrates them in their own commit.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import FrozenSet, List, Tuple

from playwright.sync_api import Page

from .config import CANDIDATE_LOCATOR_SELECTOR
from .diagnostics import _log


# ── URL sanitisation (godlike/07 fail-closed surface) ──────────────────────


def _safe_url(src: str, max_len: int = 80) -> str:
    """Truncate a src URL for safe embedding in diagnostic/error messages.

    godlike/07 fail-closed note (PR-P2-FIX-URLLEAK, July 2026):
    Google-signed URLs in src attrs can carry signed query parameters
    (`X-Goog-Algorithm`, `X-Goog-SignedHeaders`, `X-Goog-Signature`).
    Embedding `src[:80]` raw would leak the credential segment into
    JSONL / stderr logs visible to operators reading forensics.
    Strip the query string and truncate the remainder so the
    diagnostic surface shows "...googleusercontent.com/abc..." but
    never "...googleusercontent.com/abc?X-Goog-Signature=...".

    Re-exported via extraction.py as `from .candidates import _safe_url`
    so all error sites in the wave share one canonical stripper.
    """
    no_query = src.split("?", 1)[0]
    return no_query[:max_len]


# ── Typed candidate model ──────────────────────────────────────────────────


@dataclass(frozen=True)
class ImageCandidate:
    """Typed view of one sniffed image candidate.

    Field semantics match exactly the dict keys returned by the
    legacy `_extract_candidates`:
      - src:          the `src` attribute string; "" if absent.
                      Empty src is anchored to ErrNoImageCandidate on
                      extract (P0.4.A fail-closed).
      - natural_width:  natural (intrinsic) pixel width; 0 if not yet decoded.
      - natural_height: natural (intrinsic) pixel height; 0 if not yet decoded.
      - complete:     the HTMLImageElement `complete` flag; True only
                      after the image has finished loading.

    The polling loop (`poll_for_new_candidate`) accepts a candidate
    ONLY when `complete=True` AND natural dims are >=64x64 AND src is
    non-empty AND src is NOT in the baseline's seen set. All other
    candidates are filtered out and contribute to the iteration
    counter for forensic diagnostics.
    """
    src: str
    natural_width: int
    natural_height: int
    complete: bool


@dataclass(frozen=True)
class CandidateBaseline:
    """Snapshot of all gallery candidates taken BEFORE the submit step.

    Used by `poll_for_new_candidate` to filter out pre-existing images
    that the Slides UI shows in the panel from prior requests — only
    NEW candidates (src not in baseline.sources) qualify as the
    "AI-generated this turn" target. Without this filter, the worker
    would happily return the most recent stale gallery image.
    """
    candidates: Tuple[ImageCandidate, ...]
    sources: FrozenSet[str]


# ── Typed exception (godlike/07 fail-closed sentinel) ──────────────────────


class NoImageCandidateError(Exception):
    """Raised when the candidate flow has no viable target.

    The wire-side error code is conventionally `"ErrNoImageCandidate"`
    (string). The exception's `code` attribute matches so a JSONL
    emission can surface the canonical typed-sentinel string. The
    slide_worker.py orchestrator catches this exception and converts
    it to `_StepError("ErrNoImageCandidate", ...)` with
    `screenshot_path=""` (per Fix D: ErrNoImageCandidate does NOT
    capture screenshots).

    Three raising points:
      1. `poll_for_new_candidate` after timeout_seconds with no
         new+complete+large-enough candidate.
      2. `anchor_candidate_by_src` when the src-anchored locator
         resolves to zero elements (DOM redraw removed the chosen
         candidate between polling and extract).
      3. `extraction.extract_candidate` when no strategy yielded
         bytes (googleusercontent 4xx/5xx, blob fetch proxy-on-page
         failed, element screenshot fallback failed).
    """
    code: str = "ErrNoImageCandidate"


# ── Moved verbatim from slide_worker.py (Commit 3) ─────────────────────────


_MIN_CANDIDATE_WIDTH = 64
_MIN_CANDIDATE_HEIGHT = 64


def _extract_candidates(page: Page, max_keep: int = 8) -> List[dict]:
    """Snapshot the .docs-content-library-image-generation-item / blob imgs.

    Returns a bounded list (≤max_keep entries) of {src, natural_w,
    natural_h, complete} dicts — dict shape preserved byte-identical
    so existing legacy callers in slide_worker.py (ProfileWorker.)
    keep working. New code paths prefer
    `snapshot_baseline(page)` which returns the typed
    CandidateBaseline dataclass.

    Returns [] if the page is missing or the DOM has zero matching
    elements. Exception swallowing is intentional: a transient
    locator failure should not abort the request, just produce an
    empty snapshot (the polling loop will retry on the next tick).
    """
    if page is None:
        return []
    try:
        locators = page.locator(CANDIDATE_LOCATOR_SELECTOR).all()
        out: List[dict] = []
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


def _clear_image_library_panel(page: Page) -> int:
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
            const items = document.querySelectorAll(
                ".docs-content-library-image-generation-item"
            );
            let n = 0;
            for (const item of items) {
                try {
                    item.remove();
                    n++;
                } catch (e) { /* skip single-node failures */ }
            }
            return n;
        }""")
        page.wait_for_timeout(200)
        return int(removed) if removed is not None else 0
    except Exception as e:
        _log(f"[_clear_image_library_panel] {e}")
        return -1


# ── NEW typed helpers (forward-compat for Commit 4) ────────────────────────


def snapshot_baseline(page: Page) -> CandidateBaseline:
    """Snapshot the current gallery candidates as a typed baseline.

    Wraps `_extract_candidates` and converts the legacy dict list into
    a frozen `CandidateBaseline` containing the typed `ImageCandidate`
    list and a frozenset of src strings for O(1) "is this candidate
    new?" membership tests in `poll_for_new_candidate`.

    Per godlike/06 SSOT: this is the SINGLE canonical entry point for
    "establish the pre-submit baseline". Commit 4's
    `GenerationRunner.run` will call this immediately BEFORE
    `dom.submit(page)` so the poll loop can correctly identify the
    AI-generated-this-turn candidate.
    """
    raw = _extract_candidates(page)
    candidates: List[ImageCandidate] = []
    sources: set = set()
    for item in raw:
        src = item.get("src", "") or ""
        cand = ImageCandidate(
            src=src,
            natural_width=int(item.get("natural_w", 0) or 0),
            natural_height=int(item.get("natural_h", 0) or 0),
            complete=bool(item.get("complete", False)),
        )
        candidates.append(cand)
        if src:
            sources.add(src)
    return CandidateBaseline(
        candidates=tuple(candidates),
        sources=frozenset(sources),
    )


def poll_for_new_candidate(
    page: Page,
    baseline: CandidateBaseline,
    timeout_seconds: int = 60,
    poll_interval_seconds: int = 3,
) -> ImageCandidate:
    """Poll the gallery until a NEW valid candidate appears.

    P0.4 filter (must match ALL of):
      * `src` non-empty
      * `src` NOT in `baseline.sources` (i.e., AI-generated this turn)
      * `complete=True` (DOM-level loading complete)
      * `natural_width >= 64` AND `natural_height >= 64`
        (filter out thumbnails and 1x1/32x32 placeholders)

    Polling cadence: every `poll_interval_seconds` re-snapshot the
    gallery, scan all candidates, stop at the FIRST match. Returns
    the typed `ImageCandidate` on match.

    On timeout: raises `NoImageCandidateError` (P0.4 fail-closed).
    Callers catch and convert to `_StepError("ErrNoImageCandidate",
    screenshot_path="")` per Fix D.

    godlike/06 SSOT: this is the SINGLE canonical polling loop. The
    inline polling inside ProfileWorker._step_poll_for_candidate
    must NOT be re-introduced at any call site — `poll_for_new_candidate`
    is the only path.
    """
    import time

    elapsed = 0
    while elapsed < timeout_seconds:
        page.wait_for_timeout(poll_interval_seconds * 1000)
        elapsed += poll_interval_seconds
        try:
            locators = page.locator(CANDIDATE_LOCATOR_SELECTOR).all()
        except Exception as e:
            _log(f"[poll_for_new_candidate] locator scan failed: {e}")
            continue
        for img in locators:
            try:
                src = img.get_attribute("src") or ""
                if not src or src in baseline.sources:
                    continue
                nw = int(img.evaluate("e => e.naturalWidth") or 0)
                nh = int(img.evaluate("e => e.naturalHeight") or 0)
                if nw < _MIN_CANDIDATE_WIDTH or nh < _MIN_CANDIDATE_HEIGHT:
                    continue
                complete = bool(img.evaluate("e => e.complete") or False)
                if not complete:
                    continue
                return ImageCandidate(
                    src=src,
                    natural_width=nw,
                    natural_height=nh,
                    complete=True,
                )
            except Exception:
                continue
    raise NoImageCandidateError(
        f"polling timeout after {timeout_seconds}s with no NEW valid candidate "
        f"(baseline.size={len(baseline.sources)})"
    )


def anchor_candidate_by_src(page: Page, src: str) -> ImageCandidate:
    """Resolve the candidate whose `src` matches the captured string.

    P0.4.A fail-closed: if the src-anchored locator resolves to ZERO
    elements (the chosen img vanished between polling and extract due
    to Slides DOM redraw), raise `NoImageCandidateError` rather than
    silently returning the previous locator. NO success-on-vanished.

    Per the wave plan §8: the anchor MUST use `img[src='<captured-src>']`
    syntax (NOT positional). The DOM may reorder between poll and
    extract; positional locators drift toward stale image; src-anchored
    locators survive the redraw.

    Returns the typed ImageCandidate with up-to-date natural_width /
    natural_height / complete values (re-read at anchor time so the
    extract step doesn't see stale dimensions).
    """
    if not src:
        raise NoImageCandidateError("anchor_candidate_by_src: empty src")
    if page is None:
        raise NoImageCandidateError("anchor_candidate_by_src: page is None")
    locator = page.locator(f'img[src="{src}"]')
    if locator.count() == 0:
        raise NoImageCandidateError(
            f"anchor_candidate_by_src: img[src='{_safe_url(src)}'] not found in current DOM"
        )
    target = locator.first
    try:
        nw = int(target.evaluate("e => e.naturalWidth") or 0)
        nh = int(target.evaluate("e => e.naturalHeight") or 0)
        complete = bool(target.evaluate("e => e.complete") or False)
    except Exception as e:
        raise NoImageCandidateError(
            f"anchor_candidate_by_src: evaluate failed for src={_safe_url(src)}: {e}"
        ) from e
    return ImageCandidate(
        src=src,
        natural_width=nw,
        natural_height=nh,
        complete=complete,
    )
