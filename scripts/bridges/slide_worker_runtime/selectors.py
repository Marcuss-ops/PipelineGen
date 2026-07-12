"""Canonical Playwright locator / regex fragments (data-only module).

Per godlike/06 SSOT: this module is the SINGLE canonical owner of
CSS-selector fragments and IT/EN text patterns used to drive the
Google Slides (Nano Banana Pro) image-generation surface. Any
selector tweak lands here — never inlined at call sites.

In Commit 1 the only Python-side selector already used as a constant
(`.docs-content-library-image-generation-item img, ...`) lives in
config.py::CANDIDATE_LOCATOR_SELECTOR for historical reasons (it was
declared next to other env-derived config). This module re-exports
it under its canonical selectors.identity so future selector moves
can land here without changing imports elsewhere.

What does NOT live here:
  - Playwright locator() calls (those belong to dom_actions.py in
    Commit 2).
  - Selector strings embedded in page.evaluate(...) JavaScript
    payloads (those remain in slide_worker.py until Commit 2
    migrates them to dom_actions.py).
  - Runtime regex compilation (re.compile call sites stay where
    they are in slide_worker.py — only their PATTERNS would move
    here in a future commit if a third reuse site emerges).
"""

from .config import CANDIDATE_LOCATOR_SELECTOR

__all__ = ["CANDIDATE_LOCATOR_SELECTOR"]
