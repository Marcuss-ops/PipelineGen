"""Configuration constants & environment-derived defaults for slide_worker.

Per godlike/06 SSOT, every selector/limit/timeout lives here — never
redeclare them inline at call sites. Operators override most limits
via environment variables (see individual constant docstrings).

Only the 6 constants that CURRENTLY EXIST in slide_worker.py are
moved here in this commit. Scaffolding placeholders for
DEFAULT_RATIO / GENERATION_TIMEOUT_SECONDS / POLL_INTERVAL_SECONDS /
MIN_CANDIDATE_WIDTH / MIN_CANDIDATE_HEIGHT / MAX_GENERATIONS_BEFORE_RECYCLE
are DELIBERATELY OMITTED — AGENTS.md forbids adding production
features without an explicit user request backed by an existing
reader. They will land in a future commit when an actual caller
materialises that needs to consult the canonical value.
"""

import os


# ── Storage paths ──────────────────────────────────────────────────────────
#
# MASTER_STORAGE: canonical single target file for the legacy
# `storage_utils` snapshot machinery. The session profile holds the
# actual auth state, this file is a tiny JSON mirror for debugging.
MASTER_STORAGE = "data/google_slides_storage.json"

# PROFILE_DIR: persistent Chrome user-data-dir used by Playwright so the
# Google Slides session cookies survive across worker restarts. Path is
# operator-overridable at compose time (see internal/app/build_bundles_*).
PROFILE_DIR = "data/google_slides_session_profile"


# ── P2 diagnostics (July 2026) ──────────────────────────────────────────────
#
# P2_DIAGNOSTICS_DIR: when set, the worker appends one JSONL line per
# phase-event per request to $P2_DIAGNOSTICS_DIR/requests.jsonl. When unset
# (the default), the diagnostics helper is a strict no-op so production
# performance is unaffected. godlike/07 no-fake-availability: the env
# var is opt-in; setting it should be a deliberate operator choice.
#
# Rationale for append mode: a single rolling log is cheap to maintain,
# survives worker restarts, and postmortem forensics flourish when all
# requests from the process are co-located. The file is created on first
# write; an empty P2_DIAGNOSTICS_DIR is silently ignored.
P2_DIAGNOSTICS_DIR = os.environ.get("P2_DIAGNOSTICS_DIR", "").strip()
DIAG_FILE = (
    os.path.join(P2_DIAGNOSTICS_DIR, "requests.jsonl")
    if P2_DIAGNOSTICS_DIR
    else None
)


# ── P1.3 panel-refresh (July 2026) ──────────────────────────────────────────
#
# SLIDE_WORKER_REFRESH_EVERY: every N requests the worker triggers a
# DOM-level clear of the images library panel before the next submit.
# Default=1 ⟹ every request starts from a clean panel. Higher N amortises
# the 200-300ms clear cost across N requests when operator chooses to
# tolerate some cross-request contamination.
#
# Strategy: DOM-level removeChildren of the
#   `.docs-content-library-image-generation-item`
# subtree + 200ms settle. Slides.new exposes no native one-click "delete
# all"; we use plain element removal via page.evaluate. If the DOM clear
# raises (highly unlikely under Chromium), the call returns -1 and the
# worker keeps the prior state — the next request will see stale images
# only until `_maybe_recycle_page` triggers (every 20 successful runs).
SLIDE_WORKER_REFRESH_EVERY = max(1, int(os.environ.get("SLIDE_WORKER_REFRESH_EVERY") or "1"))


# ── Candidate-locator canonical form (July 2026) ──────────────────────────
#
# CANDIDATE_LOCATOR_SELECTOR: the canonical Playwright locator fragment
# for sniffing AI-generated image candidates from the Google Slides
# Nano Banana gallery. Centralised here so selector tweaks land in ONE
# place — previously duplicated across _extract_candidates + the polling
# loop in _generate, with silent-drift risk as the Slides DOM tree
# evolves across builds.
#
#   .docs-content-library-image-generation-item img
#       — tree-level container for the Slides image-generation panel
#   img.docs-image-synthesis-item
#       — element-level class pinned in P0.4.A on AI-synthesised items
#   img[src*="googleusercontent"]
#       — host-bucket filter for any googleusercontent-served candidate
#   img
#       — broad-scope fallback so late-loaded gallery children are seen
#
# Bit-for-bit identical to the previous inline fragments; refactor only.
CANDIDATE_LOCATOR_SELECTOR = (
    '.docs-content-library-image-generation-item img, '
    'img.docs-image-synthesis-item, '
    'img[src*="googleusercontent"], '
    'img'
)
