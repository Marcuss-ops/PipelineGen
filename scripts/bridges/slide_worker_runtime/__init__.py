"""slide_worker_runtime — pure-Python helpers extracted from slide_worker.py.

This package is the canonical owner of:
  - constants & env-derived configuration (config.py)
  - JSONL wire protocol types + stdio I/O (protocol.py)
  - diagnostics, logging, screenshots (diagnostics.py)
  - image-quality metric computation (image_quality.py)
  - canonical Playwright selector fragments (selectors.py)

The remaining slide_worker.py owns the dispatch loop, browser session
lifecycle, generation orchestration, and DOM interactions (migrated
to runtime/dom_actions.py + candidates.py + extraction.py + session.py
+ generation.py in subsequent commits per the wave plan).

Per godlike/06 SSOT (single owner per fact): once a symbol lands
here, no other module may redefine it — edit here only.
"""
