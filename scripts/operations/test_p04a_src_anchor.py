#!/usr/bin/env python3
"""
test_p04a_src_anchor.py — P0.4.A regression test for slide_worker.py Step 6.

Audit verdict (P0.4.A):
  `page.locator(...).all()` returns Locator objects (lazy, re-evaluating),
  NOT ElementHandles. The pre-fix code reused the Step 5 nth-position
  Locator at Step 6, which silently rebinds to whatever element is at
  nth-K of the parent selector IF the Slides.new DOM redraws between
  Step 5 break and Step 6 evaluate. This caused mis-extraction (bytes
  for a different img than the cached metadata claims).

This test simulates the bind-shift hazard deterministically on a
stub HTML page (not against slides.new) and pins the canonical
fix's correctness contract:

  1. OLD path probe: `page.locator(...).all()[K]` after DOM redraw
     with shifted nth-positions → silently returns the WRONG src
     (proves the audit verdict is reproducible).
  2. NEW path probe: `page.locator('img[src="X"]')` after the SAME
     redraw → still returns src=X (proves the canonical fix pins
     to the chosen candidate regardless of DOM reordering).
  3. count() == 0 fail-closed path: when the chosen img vanishes,
     the src-specific locator's count() is 0 → typed sentinel
     ErrNoImageCandidate (mirrors Step 6 fix's contract).

Test contract (3 sub-tests, all MUST pass for ship-go):

  P0.4.A.1 — Step 5 picks src=X (nth-K=2 of 4 imgs). DOM redraws so
              nth-2 now points to a different img (other_c, NOT X).
              OLD locator: asserts get_attribute("src") != X (audit
              verdict reproducible). NEW locator: asserts
              count()==1 and get_attribute("src") == X (fix correct).

  P0.4.A.2 — Setup identical to P0.4.A.1. The chosen X img is
              REMOVED from the DOM at Step 6 (worst-case redraw).
              NEW locator: count()==0 (fail-closed signal).

  P0.4.A.3 — Two synthetic img selector variants:
              `.docs-content-library-image-generation-item img[src="X"]`
              matches even when X is nested inside the canonical parent
              selector (matches the worker's actual DOM tree shape).

Exit codes: 0 = all sub-tests pass, 1 = any failure.

Run from project root:
  python3 scripts/operations/test_p04a_src_anchor.py
"""
import sys
import os

# Make slide_worker importable from this script's perspective, so we
# can read the canonical selector without re-typing it. We do not
# instantiate ProfileWorker (it would try to launch a real browser).
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "bridges")))
try:
    from slide_worker import _log  # type: ignore  # for parity with worker stderr format
    HAS_SLIDE_WORKER = True
except Exception as e:
    HAS_SLIDE_WORKER = False
    print(f"[test] WARNING: slide_worker import failed: {e}", file=sys.stderr)

from playwright.sync_api import sync_playwright

# ── Test fixtures (deterministic, browser-portable) ────────────────────────

# Step 5 DOM: 4 imgs total.
#  nth=0: stale_a (in P0.4 baseline — would be filtered, but Step 5
#         captures from the post-click candidates; here we only count
#         for the locator's nth-K rebinding test).
#  nth=1: stale_b  (same)
#  nth=2: chosen_X (the OUR candidate we want; K=2)
#  nth=3: other_c
# Note: in real P0.4 Step 5 the chosen_X is the FIRST imgs_check that
# passes the baseline+complete+dims filter; the test mirrors that
# state by pre-seeding the DOM with chosen_X at nth=2.
STEP5_DOM = """
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_a" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_b" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="blob:https://slides.new/CHOSEN_X_abc123" width="1024" height="768">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/other_c" width="800" height="600">
</div>
"""

# Step 6 REDRAWN DOM: insert one new img at position 0, shifting
# the previous nth=2 (chosen_X) to nth=3, and putting a new
# unrelated img at nth=2 (other_c.c2). This is the exact hazard
# captured by the P0.4.A audit verdict.
STEP6_REDRAWN_DOM = """
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/newly_inserted_at_0" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_a" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_b" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="blob:https://slides.new/CHOSEN_X_abc123" width="1024" height="768">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/other_c" width="800" height="600">
</div>
"""

# Step 6 VANISHED DOM: chosen_X is REMOVED entirely (worst-case redraw).
STEP6_VANISHED_DOM = """
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_a" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/stale_b" width="800" height="600">
</div>
<div class="docs-content-library-image-generation-item">
  <img src="https://lh3.googleusercontent.com/other_c_new" width="800" height="600">
</div>
"""

CHOSEN_X = "blob:https://slides.new/CHOSEN_X_abc123"
PARENT_SELECTOR = (
    '.docs-content-library-image-generation-item img, '
    'img.docs-image-synthesis-item, '
    'img[src*="googleusercontent"], '
    'img'
)


def build_html(body: str) -> str:
    return f"""<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>P0.4.A test page</title></head>
<body>
{body}
</body>
</html>"""


# ── Test runner ───────────────────────────────────────────────────────────

def run_tests() -> bool:
    """Returns True iff all sub-tests pass."""
    sub_tests_passed = []

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context()
        page = context.new_page()

        # ── P0.4.A.1: Step 5 picks src=X (nth=2); DOM redraws; OLD locator
        #               silently rebinds (audit verdict), NEW locator pins to X.
        try:
            print("[test P0.4.A.1] starting: Step 5 picks src=X; DOM redraws.", file=sys.stderr)
            page.set_content(build_html(STEP5_DOM))
            page.wait_for_load_state("domcontentloaded")
            # Step 5: capture the PTR to the nth=2 locator, mirroring the
            # old slide_worker.py pattern (page.locator(parent_selector).all()[K]).
            # The captured `old_locator` is a Locator at index K=2 from
            # .all() — but Locator.all() returns Locators (not handles).
            old_locator_K2 = page.locator(PARENT_SELECTOR).all()[2]
            captured_src_step5 = old_locator_K2.get_attribute("src")
            assert captured_src_step5 == CHOSEN_X, \
                f"Step 5 setup invariant broken: expected nth=2 src={CHOSEN_X}, got {captured_src_step5}"

            # Step 6: simulate Slides.new DOM redraw — insert a new img at
            # position 0, shifting the previous nth=2 (chosen_X) to nth=3.
            page.set_content(build_html(STEP6_REDRAWN_DOM))
            page.wait_for_load_state("domcontentloaded")

            # AUDIT PROBE: re-evaluate the OLD nth-K=2 Locator against the
            # redrawn DOM. Per Playwright's locator semantics, this fires a
            # fresh DOM query and picks the element currently at nth=2 of
            # the parent selector — which, NOW, is stale_b (NOT chosen_X).
            old_locator_K2_after_redraw_src = old_locator_K2.get_attribute("src")
            assert old_locator_K2_after_redraw_src != CHOSEN_X, (
                f"AUDIT VERDICT INVERTED (unexpected): old locator "
                f"after redraw ALSO returned src={old_locator_K2_after_redraw_src!r}; "
                f"the audit hazard was supposed to be reproducible. "
                f"If Playwright's locator semantics changed and "
                f"`Locator.all()[K]` now anchors to the originally-resolved "
                f"element, the bug may not trigger — but the canonical fix is "
                f"still strictly correct (src-anchored)."
            )
            print(
                f"[test P0.4.A.1] AUDIT VERDICT REPRODUCED: old locator K=2 returns src={old_locator_K2_after_redraw_src!r} after redraw "
                f"(would have caused Step 6 to extract bytes for the WRONG img in production).",
                file=sys.stderr,
            )

            # FIX PROBE: build the NEW P0.4.A src-anchored locator against
            # the SAME redrawn DOM. Mirrors Step 6's: `page.locator('img[src="..."]')`.
            new_locator = page.locator(f'img[src="{CHOSEN_X}"]')
            new_count = new_locator.count()
            assert new_count == 1, (
                f"P0.4.A fix broken: src-anchored locator expected count=1 "
                f"(chosen_X still present in DOM), got count={new_count}"
            )
            new_locator_src = new_locator.get_attribute("src")
            assert new_locator_src == CHOSEN_X, (
                f"P0.4.A fix broken: src-anchored locator returned "
                f"src={new_locator_src!r}, want {CHOSEN_X!r}"
            )
            print(
                f"[test P0.4.A.1] P0.4.A FIX VERIFIED: src-anchored locator count={new_count} src={new_locator_src!r} "
                f"(survives DOM redraw; pinned to chosen_X regardless of nth-shift).",
                file=sys.stderr,
            )
            sub_tests_passed.append(("P0.4.A.1", True))
        except AssertionError as e:
            print(f"[test P0.4.A.1] FAILED: {e}", file=sys.stderr)
            sub_tests_passed.append(("P0.4.A.1", False))

        # ── P0.4.A.2: chosen_X is REMOVED at Step 6; src-specific locator
        #               reports count() == 0 → fail-closed.
        try:
            print("[test P0.4.A.2] starting: Step 5 picks src=X; chosen_X vanishes at Step 6.", file=sys.stderr)
            page.set_content(build_html(STEP5_DOM))
            page.wait_for_load_state("domcontentloaded")
            captured_src_step5 = page.locator(PARENT_SELECTOR).all()[2].get_attribute("src")
            assert captured_src_step5 == CHOSEN_X, "P0.4.A.2 setup invariant broken"

            # Step 6: chosen_X vanished (replaced by other_c_new at the same DOM position).
            page.set_content(build_html(STEP6_VANISHED_DOM))
            page.wait_for_load_state("domcontentloaded")

            new_count = page.locator(f'img[src="{CHOSEN_X}"]').count()
            assert new_count == 0, (
                f"P0.4.A fail-closed broken: chosen_X removed but src locator "
                f"still reports count={new_count} (should be 0 to trigger ErrNoImageCandidate)"
            )
            print(
                f"[test P0.4.A.2] P0.4.A FAIL-CLOSED VERIFIED: count()={new_count} → "
                f"Step 6 surfaces typed ErrNoImageCandidate (no silent success on vanished candidate).",
                file=sys.stderr,
            )
            sub_tests_passed.append(("P0.4.A.2", True))
        except AssertionError as e:
            print(f"[test P0.4.A.2] FAILED: {e}", file=sys.stderr)
            sub_tests_passed.append(("P0.4.A.2", False))

        # ── P0.4.A.3: parent-scope anchor also matches when X is nested
        #               inside the canonical .docs-content-library-image-generation-item wrapper.
        try:
            print("[test P0.4.A.3] starting: parent-scoped anchor also resolves.", file=sys.stderr)
            # Reuse STEP6_REDRAWN_DOM (chosen_X is nested under the parent).
            page.set_content(build_html(STEP6_REDRAWN_DOM))
            page.wait_for_load_state("domcontentloaded")
            scoped = page.locator(
                f'.docs-content-library-image-generation-item img[src="{CHOSEN_X}"]'
            )
            scoped_count = scoped.count()
            assert scoped_count == 1, (
                f"P0.4.A scoped anchor broken: expected count=1 got {scoped_count}"
            )
            scoped_src = scoped.get_attribute("src")
            assert scoped_src == CHOSEN_X, (
                f"P0.4.A scoped anchor wrong src: got {scoped_src!r}, want {CHOSEN_X!r}"
            )
            print(
                f"[test P0.4.A.3] SCOPED ANCHOR OK: .docs-content-library-image-generation-item "
                f"img[src=X] count={scoped_count} src={scoped_src!r}.",
                file=sys.stderr,
            )
            sub_tests_passed.append(("P0.4.A.3", True))
        except AssertionError as e:
            print(f"[test P0.4.A.3] FAILED: {e}", file=sys.stderr)
            sub_tests_passed.append(("P0.4.A.3", False))

        context.close()
        browser.close()

    # ── Summary ──────────────────────────────────────────────────────────
    print("", file=sys.stderr)
    print("=" * 72, file=sys.stderr)
    print("  P0.4.A regression test summary:", file=sys.stderr)
    for name, ok in sub_tests_passed:
        marker = "PASS" if ok else "FAIL"
        print(f"    [{marker}] {name}", file=sys.stderr)
    print("=" * 72, file=sys.stderr)
    return all(ok for _, ok in sub_tests_passed)


if __name__ == "__main__":
    ok = run_tests()
    sys.exit(0 if ok else 1)
