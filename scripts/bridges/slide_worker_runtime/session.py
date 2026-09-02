"""BrowserSession — owns the persistent Playwright context + page + storage state.

Wave Commit 4 (July 2026): the ProfileWorker class is split. The
THIN ProfileWorker (in runtime.dispatcher) owns threading + queues +
delegates ALL Playwright interactions to BrowserSession. All
session-scoped state — context, page, profile_dir, generation
counter, refresh counter — lives here.

Per godlike/06 SSOT:
  - BrowserSession is the SINGLE canonical owner of "the browser's
    state". Any code path that needs to call `page.context.something`
    goes through this class (so the threading contract + warmup +
    recycle policy are centralised).
  - BrowserSession is NOT a Thread. Thread ownership stays on
    ProfileWorker. (Playwright sync API's threading rules require
    that the Playwright handle is owned by a single thread; the
    thread is ProfileWorker, the handle state is here.)

Per godlike/07 fail-closed:
  - The login guard inside `start()` raises Exception("login
    required: ...") if the browser lands on accounts.google.com.
    ProfileWorker surfaces this as `_warmup_error`. The typed
    failure is preserved end-to-end.
"""

from __future__ import annotations

import os
import shutil
from typing import Optional

from playwright.sync_api import (
    BrowserContext,
    Page,
    TimeoutError as PlaywrightTimeout,
    sync_playwright,
)

from storage_utils import (
    choose_storage_candidate,
    save_storage_snapshot,
    storage_looks_usable,
)

from .config import MASTER_STORAGE, PROFILE_DIR
from .diagnostics import _log, _log_diag


def _browser_launch_options(headless: bool) -> dict:
    """Return stable launch options with an explicit system-browser fallback.

    Playwright's managed Chromium is not guaranteed to be present on a
    production host. When it is absent, use the operator-provided browser
    or a distro-installed Chrome so the worker reports the real next
    readiness error (for example authentication) instead of an opaque
    executable-missing failure.
    """
    options = {
        "headless": headless,
        "args": [
            "--disable-blink-features=AutomationControlled",
            "--no-sandbox",
            "--disable-setuid-sandbox",
        ],
    }
    executable = os.getenv("PIPELINEGEN_CHROME_EXECUTABLE", "").strip()
    if not executable:
        executable = shutil.which("google-chrome") or shutil.which("chromium") or ""
    if executable:
        options["executable_path"] = executable
    return options


class BrowserSession:
    """One persistent browser session: profile_dir + context + page.

    Lifecycle:
      start()             → warmup: launch + restore cookies + navigate
                            to slides.new. Raises if accounts.google.com.
      fresh_page()        → close current page, open a fresh one at
                            slides.new. Recovery path for typed errors
                            AND for `_maybe_recycle_page`.
      recycle_if_needed() → fresh_page every N generations (default 20).
      persist()           → save cookies/storage_state to MASTER_STORAGE.
      health()            → basic alive + page + url check.
      health_deep()       → DOM-level probe for Nano Banana surface.
      close()             → persist + close context + stop Playwright.

    NOT-OWNED:
      - threading state, queues, request handling — ProfileWorker
      - the JSONL protocol / wire format — runtime.protocol
      - prompt composition, image extraction — runtime.generation
        (GenerationRunner + step methods)
    """

    def __init__(
        self,
        profile_id: int,
        headful: bool = False,
        max_generations_before_page_recycle: int = 20,
    ) -> None:
        self.profile_id = profile_id
        self.headful = headful
        self.profile_dir = f"{PROFILE_DIR}_{profile_id}_{os.getpid()}"
        self.playwright = None
        self.context: Optional[BrowserContext] = None
        self.page: Optional[Page] = None
        self._generation_count = 0
        self._max_generations_before_page_recycle = max_generations_before_page_recycle
        # P1.3 (July 2026): per-request counter that gates the panel
        # refresh (`SLIDE_WORKER_REFRESH_EVERY` env var). Initialised
        # to 0 so the first request has count=1 (divisible by N=1
        # default → always clear).
        self._refresh_count = 0

    # ── Warmup ────────────────────────────────────────────────────────

    def start(self) -> None:
        """Launch persistent browser, load cookies, navigate to slides.new.

        Raises Exception("login required: ...") on accounts.google.com
        fallback. ProfileWorker catches this into `_warmup_error` and
        surfaces it through the warmup response shape byte-byte.
        """
        _log(f"[profile-{self.profile_id}] warmup: launching browser...")
        _log_diag("warmup", self.profile_id, "warmup", url="https://slides.new")

        self.playwright = sync_playwright().start()

        self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}"
        try:
            os.makedirs(self.profile_dir, exist_ok=True)
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir, **_browser_launch_options(not self.headful)
            )
            _log(
                f"[profile-{self.profile_id}] warmup: launched browser "
                f"with stable context at {self.profile_dir}"
            )
        except Exception as le:
            self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}_{os.getpid()}"
            os.makedirs(self.profile_dir, exist_ok=True)
            _log(
                f"[profile-{self.profile_id}] warmup: stable context "
                f"locked ({le}), falling back to {self.profile_dir}"
            )
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir, **_browser_launch_options(not self.headful)
            )

        # The master snapshot is the login helper's canonical output and is
        # shared by every pool worker. Prefer it over per-profile snapshots,
        # which may be stale after the operator refreshes authentication.
        cookie_path, sdata = choose_storage_candidate(
            MASTER_STORAGE,
            f"{MASTER_STORAGE}.profile_{self.profile_id}",
            f"{MASTER_STORAGE}.profile_{self.profile_id}.backup",
            f"{MASTER_STORAGE}.backup",
        )
        if cookie_path and sdata:
            try:
                if "cookies" in sdata and sdata["cookies"]:
                    self.context.add_cookies(sdata["cookies"])
                    _log(
                        f"[profile-{self.profile_id}] warmup: loaded session cookies from {cookie_path}"
                    )
                self._restore_storage_state(sdata)
            except Exception as e:
                _log(
                    f"[profile-{self.profile_id}] warmup: failed to load cookies from {cookie_path}: {e}"
                )

        self.page = self.context.new_page()
        try:
            self.page.goto(
                "https://docs.google.com/presentation/create",
                wait_until="domcontentloaded",
                timeout=15000,
            )
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: auth probe timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception(
                "login required: user is logged out "
                "(please run scripts/bridges/login.py to sign in)"
            )

        _log(f"[profile-{self.profile_id}] warmup: navigating to slides.new...")
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: slides.new timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception(
                "login required: user is logged out "
                "(please run scripts/bridges/login.py to sign in)"
            )

    # ── Storage state ─────────────────────────────────────────────────

    def _restore_storage_state(self, sdata: dict) -> None:
        """Restore per-origin localStorage from a previously-saved snapshot.

        Iterates the `origins` list in `sdata` (Playwright storage_state
        shape) and re-applies each origin's `localStorage` entries via
        a transient page navigation. Tolerates per-origin failures so
        one broken origin doesn't abort the whole warmup.
        """
        origins = sdata.get("origins") or []
        if not origins:
            return

        for origin_state in origins:
            origin = origin_state.get("origin")
            local_storage = origin_state.get("localStorage") or []
            if not origin or not local_storage:
                continue
            page = None
            try:
                page = self.context.new_page()
                page.goto(origin, wait_until="domcontentloaded", timeout=30000)
                page.evaluate(
                    """(entries) => {
                        for (const item of entries) {
                            if (item && item.name !== undefined && item.value !== undefined) {
                                localStorage.setItem(item.name, item.value);
                            }
                        }
                    }""",
                    local_storage,
                )
                _log(
                    f"[profile-{self.profile_id}] warmup: restored localStorage for {origin}"
                )
            except Exception as e:
                _log(
                    f"[profile-{self.profile_id}] warmup: failed restoring localStorage "
                    f"for {origin}: {e}"
                )
            finally:
                try:
                    if page is not None:
                        page.close()
                except Exception:
                    pass

    def persist(self, request_id: Optional[str] = None, reason: str = "auto-save") -> None:
        """Save storage state (cookies + localStorage) to disk.

        Skips if:
          - context is missing (warmup crashed before this point)
          - page is on accounts.google.com (would persist stale login
            state — refuse-to-pollute semantics)
          - storage_looks_usable() returns False (the snapshot is
            empty / anonymous)

        Writes to BOTH a "global" path AND a per-profile path so
        multi-profile restarts can pick the right snapshot.
        """
        if not self.context:
            return
        if self.page and "accounts.google.com" in self.page.url:
            _log(
                f"[profile-{self.profile_id}] {reason}: skip save, "
                f"browser is on accounts.google.com"
            )
            return
        storage = self.context.storage_state()
        if not storage_looks_usable(storage):
            _log(
                f"[profile-{self.profile_id}] {reason}: skip save, "
                f"storage snapshot looks empty"
            )
            return
        save_storage_snapshot(
            MASTER_STORAGE, storage, backup_path=f"{MASTER_STORAGE}.backup"
        )
        profile_storage_path = f"{MASTER_STORAGE}.profile_{self.profile_id}"
        save_storage_snapshot(
            profile_storage_path, storage, backup_path=f"{profile_storage_path}.backup"
        )
        if request_id:
            _log(
                f"[profile-{self.profile_id}][{request_id}] {reason}: saved session snapshot"
            )
        else:
            _log(f"[profile-{self.profile_id}] {reason}: saved session snapshot")

    # ── Page recovery ─────────────────────────────────────────────────

    def fresh_page(self) -> None:
        """Close current page + open a fresh one at slides.new.

        Recovery path for typed errors. The pending page state
        (uncommitted DOM mutations, half-filled prompts) is discarded.
        """
        _log(f"[profile-{self.profile_id}] recovery: opening fresh page...")
        try:
            if self.page and not self.page.is_closed():
                self.page.close()
        except Exception:
            pass
        if not self.context:
            _log(
                f"[profile-{self.profile_id}] recovery: context is None — cannot recover"
            )
            return
        self.page = self.context.new_page()
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(
                f"[profile-{self.profile_id}] recovery: slides.new timed out — continuing"
            )

    def recycle_if_needed(self) -> None:
        """Recycle page after N successful generations (default 20).

        Bumps `_generation_count` per call; if it hits the threshold
        (`_max_generations_before_page_recycle`), calls `fresh_page()`
        and resets the counter. The recycle is a slide-side cleanup
        defence (Slides' document tree accumulates stale event listeners
        over many generations; recycling prevents the page from
        becoming unreasonably heavy).
        """
        self._generation_count += 1
        if self._generation_count >= self._max_generations_before_page_recycle:
            _log(
                f"[profile-{self.profile_id}] recycling page after "
                f"{self._generation_count} generations"
            )
            self.fresh_page()
            self._generation_count = 0

    # ── Health probes ─────────────────────────────────────────────────

    def health(self) -> dict:
        """Minimal alive + page + url check.

        Returns `{"status": "error", "error": "..."}` on any condition
        that should reject a `health` request from the Go side.
        """
        if self.page is None or self.page.is_closed():
            return {"status": "error", "error": "page closed"}
        if "accounts.google.com" in self.page.url:
            return {
                "status": "error",
                "error": "login required: user is logged out "
                "(please run scripts/bridges/login.py to sign in)",
            }
        return {"status": "ok"}

    def health_deep(self) -> dict:
        """DOM-level probe for Nano Banana panel + textarea + Image tab.

        Each sub-check is captured independently so the caller (Go side
        `HealthDeep()`) can include fine-grained diagnostics in the typed
        error wrap. `profile_healthy` aggregates the basic alive+page check.
        """
        panel_ok = False
        textarea_ok = False
        image_mode_selectable = False
        url = ""
        try:
            if self.page is not None and not self.page.is_closed():
                url = self.page.url
                try:
                    btn = self.page.locator(
                        "button.insert-generated-image, "
                        "[data-view-id=\"insert-generated-image\"], "
                        "div[role=\"button\"]:has-text(\"Nano Banana Pro\"), "
                        "button:has-text(\"Nano Banana Pro\")"
                    ).last
                    panel_ok = btn.is_visible() if btn else False
                except Exception:
                    panel_ok = False
                try:
                    ta = self.page.locator("textarea:visible").first
                    visible = ta.is_visible() if ta else False
                    enabled = ta.is_enabled() if (ta and visible) else False
                    textarea_ok = visible and enabled
                except Exception:
                    textarea_ok = False
                try:
                    tab = self.page.locator(
                        "[role=\"tab\"]:has-text(\"Immagine\"), "
                        "[role=\"tab\"]:has-text(\"Image\"), "
                        "button:has-text(\"Immagine\"), button:has-text(\"Image\")"
                    ).first
                    if tab:
                        visible = tab.is_visible()
                        enabled = tab.is_enabled()
                        image_mode_selectable = visible and enabled
                except Exception:
                    image_mode_selectable = False
        except Exception as e:
            _log(f"[profile-{self.profile_id}][health_deep] DOM probe error: {e}")

        profile_healthy = self.health().get("status") == "ok"
        failure_reasons = []
        if not panel_ok:
            failure_reasons.append("nano-banana-panel-missing")
        if not textarea_ok:
            failure_reasons.append("textarea-not-interactable")
        if not image_mode_selectable:
            failure_reasons.append("image-mode-not-selectable")
        if not profile_healthy:
            failure_reasons.append("profile-not-healthy")
        all_ok = len(failure_reasons) == 0
        return {
            "status": "ok" if all_ok else "error",
            "panel_ok": panel_ok,
            "textarea_ok": textarea_ok,
            "image_mode_selectable": image_mode_selectable,
            "url": url,
            "profile_healthy": profile_healthy,
            "failure_reason": ",".join(failure_reasons) if failure_reasons else "",
        }

    # ── Lifecycle ─────────────────────────────────────────────────────

    def is_warm(self) -> bool:
        """True if `start()` succeeded: context + page exist + not on login.

        ProfileWorker consults this in its warmup wait path.
        """
        return (
            self.context is not None
            and self.page is not None
            and not self.page.is_closed()
        )

    def close(self) -> None:
        """Persist + close context + stop Playwright + cleanup temp dirs.

        Order:
          1. persist(reason="shutdown-save") (best-effort, swallows errors)
          2. context.close() (best-effort)
          3. playwright.stop() (best-effort)
          4. rmtree if profile_dir contains the per-pid tag (temp
             profile cleanup vs. stable profile preserve).
        """
        try:
            if self.context:
                self.persist(reason="shutdown-save")
        except Exception as e:
            _log(f"[profile-{self.profile_id}] failed to save storage: {e}")

        try:
            if self.context:
                self.context.close()
        except Exception:
            pass

        try:
            if self.playwright:
                self.playwright.stop()
        except Exception:
            pass

        if "_" + str(os.getpid()) in self.profile_dir:
            try:
                shutil.rmtree(self.profile_dir, ignore_errors=True)
                _log(
                    f"[profile-{self.profile_id}] cleaned up temporary profile directory "
                    f"{self.profile_dir}"
                )
            except Exception as e:
                _log(
                    f"[profile-{self.profile_id}] failed to clean up profile directory: {e}"
                )
        else:
            _log(f"[profile-{self.profile_id}] preserving stable profile directory {self.profile_dir}")
