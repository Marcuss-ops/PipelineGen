#!/usr/bin/env python3
"""
Persistent Chrome/Playwright worker for AI image generation via Google Slides
Nano Banana Pro.

Protocol (stdin → stdout, one JSON object per line, newline-delimited):

  REQUEST (stdin):
    {"action": "warmup"}
    {"action": "generate", "id": "<request-id>", "prompt": "...", "output": "/path/to/output.png"}
    {"action": "health"}
    {"action": "quit"}

  RESPONSE (stdout):
    {"id": "<request-id>", "status": "ok", "output": "...", "elapsed_ms": 22000, "bytes": 123456, "profile": 2}
    {"id": "<request-id>", "status": "error", "error": "..."}
    {"status": "ready", "profiles": 3}         # warmup response
    {"status": "ok", "profiles": {"0":"ok","1":"ok","2":"ok"}}  # health

Single-profile model:
  - One ProfileWorker thread owns one persistent browser context.
  - Requests are processed serially through a single queue.
  - No legacy profile cloning, no round-robin, no multi-profile routing.
"""

import argparse
import io
import json
import os
import queue
import signal
import sys
import threading
import time
import traceback
from datetime import datetime, timezone

from playwright.sync_api import sync_playwright, TimeoutError as PlaywrightTimeout
from storage_utils import choose_storage_candidate, save_storage_snapshot, storage_looks_usable
from PIL import Image

MASTER_STORAGE = "data/google_slides_storage.json"
PROFILE_DIR = "data/google_slides_session_profile"

# ── Helpers ────────────────────────────────────────────────────────────────

def _log(msg: str) -> None:
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"
    print(f"[{ts}] {msg}", file=sys.stderr, flush=True)


def _respond(obj: dict) -> None:
    """Write one JSON object line to stdout and flush."""
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def _error(request_id: str | None, msg: str) -> None:
    payload = {"status": "error", "error": msg}
    if request_id:
        payload["id"] = request_id
    _respond(payload)


def _save_image_bytes(image_bytes: bytes, output_path: str) -> str:
    """Save image bytes using the format implied by the output file extension."""
    ext = os.path.splitext(output_path)[1].lower().lstrip(".")
    if not ext:
        ext = "png"

    with Image.open(io.BytesIO(image_bytes)) as img:
        if ext in {"jpg", "jpeg"}:
            img = img.convert("RGB")
            img.save(output_path, format="JPEG", quality=95)
            return "jpeg"

        if ext == "png":
            if img.mode not in {"RGB", "RGBA"}:
                img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
            img.save(output_path, format="PNG")
            return "png"

        if ext == "webp":
            if img.mode not in {"RGB", "RGBA"}:
                img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
            img.save(output_path, format="WEBP")
            return "webp"

        # Unknown extension: default to PNG to keep the output deterministic.
        if img.mode not in {"RGB", "RGBA"}:
            img = img.convert("RGBA" if "A" in img.getbands() else "RGB")
        img.save(output_path, format="PNG")
        return "png"


# ── ProfileWorker (one thread = one browser = one profile) ─────────────────

class ProfileWorker(threading.Thread):
    """One persistent browser profile with its own request queue.

    Lifecycle:
      start() → warmup (launch browser, navigate slides.new)
      run()  → loop: wait for request → generate → put result
      stop()  → shutdown (save session, close browser)
    """

    def __init__(self, profile_id: int, headful: bool = False) -> None:
        super().__init__(daemon=True, name=f"profile-{profile_id}")
        self.profile_id = profile_id
        # Append PID to prevent locking conflicts when running concurrent processes
        self.profile_dir = f"{PROFILE_DIR}_{profile_id}_{os.getpid()}"
        self.headful = headful

        # in_queue: max 1 pending request — enforces "1 job at a time per profile"
        self.in_queue: queue.Queue = queue.Queue(maxsize=1)
        # out_queue: result of the currently processing request
        self.out_queue: queue.Queue = queue.Queue()

        self.playwright = None
        self.context = None
        self.page = None
        self._warmed = threading.Event()
        self._warmup_error: str | None = None
        self._running = True
        self._generation_count = 0
        self._max_generations_before_page_recycle = 20

    # ── Warmup ────────────────────────────────────────────────────────

    def warmup(self) -> None:
        """Launch persistent browser, load cookies, navigate to slides.new."""
        _log(f"[profile-{self.profile_id}] warmup: launching browser...")

        self.playwright = sync_playwright().start()

        # Try stable profile directory first to reuse browser state, fallback to PID-based if locked
        self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}"
        try:
            os.makedirs(self.profile_dir, exist_ok=True)
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir,
                headless=not self.headful,
                args=[
                    "--disable-blink-features=AutomationControlled",
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                ],
            )
            _log(f"[profile-{self.profile_id}] warmup: launched browser with stable context at {self.profile_dir}")
        except Exception as le:
            self.profile_dir = f"{PROFILE_DIR}_{self.profile_id}_{os.getpid()}"
            os.makedirs(self.profile_dir, exist_ok=True)
            _log(f"[profile-{self.profile_id}] warmup: stable context locked ({le}), falling back to {self.profile_dir}")
            self.context = self.playwright.chromium.launch_persistent_context(
                self.profile_dir,
                headless=not self.headful,
                args=[
                    "--disable-blink-features=AutomationControlled",
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                ],
            )

        # Prefer the most recent usable storage snapshot, falling back to backups
        # if the primary file was overwritten by a logged-out session.
        cookie_path, sdata = choose_storage_candidate(
            f"{MASTER_STORAGE}.profile_{self.profile_id}",
            MASTER_STORAGE,
            f"{MASTER_STORAGE}.profile_{self.profile_id}.backup",
            f"{MASTER_STORAGE}.backup",
        )
        if cookie_path and sdata:
            try:
                if "cookies" in sdata and sdata["cookies"]:
                    self.context.add_cookies(sdata["cookies"])
                    _log(f"[profile-{self.profile_id}] warmup: loaded session cookies from {cookie_path}")
                self._restore_storage_state(sdata)
            except Exception as e:
                _log(f"[profile-{self.profile_id}] warmup: failed to load cookies from {cookie_path}: {e}")

        # Quick auth probe: fail fast before opening the full Slides UI.
        self.page = self.context.new_page()
        try:
            self.page.goto("https://docs.google.com/presentation/create", wait_until="domcontentloaded", timeout=15000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: auth probe timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception("login required: user is logged out (please run scripts/bridges/login.py to sign in)")

        _log(f"[profile-{self.profile_id}] warmup: navigating to slides.new...")
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: slides.new timed out — continuing")

        if "accounts.google.com" in self.page.url:
            raise Exception("login required: user is logged out (please run scripts/bridges/login.py to sign in)")

        self._warmed.set()
        _log(f"[profile-{self.profile_id}] warmup: ready")

    def _restore_storage_state(self, sdata: dict) -> None:
        """Restore origin localStorage entries saved by Playwright storage_state()."""
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
                    f"[profile-{self.profile_id}] warmup: failed restoring localStorage for {origin}: {e}"
                )
            finally:
                try:
                    if page is not None:
                        page.close()
                except Exception:
                    pass

    def _persist_storage_state(self, request_id: str | None = None, reason: str = "auto-save") -> None:
        """Persist the current storage state only if it still looks authenticated."""
        if not self.context:
            return
        if self.page and "accounts.google.com" in self.page.url:
            _log(f"[profile-{self.profile_id}] {reason}: skip save, browser is on accounts.google.com")
            return
        storage = self.context.storage_state()
        if not storage_looks_usable(storage):
            _log(f"[profile-{self.profile_id}] {reason}: skip save, storage snapshot looks empty")
            return
        save_storage_snapshot(MASTER_STORAGE, storage, backup_path=f"{MASTER_STORAGE}.backup")
        profile_storage_path = f"{MASTER_STORAGE}.profile_{self.profile_id}"
        save_storage_snapshot(profile_storage_path, storage, backup_path=f"{profile_storage_path}.backup")
        if request_id:
            _log(f"[profile-{self.profile_id}][{request_id}] {reason}: saved session snapshot")
        else:
            _log(f"[profile-{self.profile_id}] {reason}: saved session snapshot")

    # ── Main loop ──────────────────────────────────────────────────────

    def run(self) -> None:
        """Thread target: warm up, then process queue until stopped."""
        try:
            self.warmup()
        except Exception as e:
            self._warmup_error = str(e)
            self._warmed.set()
            _log(f"[profile-{self.profile_id}] warmup failed: {e}")
            return  # thread exits — main thread will detect via health check

        while self._running:
            try:
                req = self.in_queue.get(timeout=1)
            except queue.Empty:
                continue

            if req is None:  # shutdown sentinel
                break

            result = self._generate(req)
            self.out_queue.put(result)

        # Shutdown
        _log(f"[profile-{self.profile_id}] shutting down...")
        try:
            if self.context:
                self._persist_storage_state(reason="shutdown-save")
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

        import shutil
        if "_" + str(os.getpid()) in self.profile_dir:
            try:
                shutil.rmtree(self.profile_dir, ignore_errors=True)
                _log(f"[profile-{self.profile_id}] cleaned up temporary profile directory {self.profile_dir}")
            except Exception as e:
                _log(f"[profile-{self.profile_id}] failed to clean up profile directory: {e}")
        else:
            _log(f"[profile-{self.profile_id}] preserving stable profile directory {self.profile_dir}")

        _log(f"[profile-{self.profile_id}] stopped")

    # ── Generation (same logic as before, extracted into ProfileWorker) ──

    def _fresh_page(self) -> None:
        """Close current page and open a fresh one at slides.new."""
        _log(f"[profile-{self.profile_id}] recovery: opening fresh page...")
        try:
            if self.page and not self.page.is_closed():
                self.page.close()
        except Exception:
            pass
        if not self.context:
            _log(f"[profile-{self.profile_id}] recovery: context is None — cannot recover")
            return
        self.page = self.context.new_page()
        try:
            self.page.goto("https://slides.new", wait_until="load", timeout=30000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] recovery: slides.new timed out — continuing")

    def _maybe_recycle_page(self) -> None:
        self._generation_count += 1
        if self._generation_count >= self._max_generations_before_page_recycle:
            _log(f"[profile-{self.profile_id}] recycling page after {self._generation_count} generations")
            self._fresh_page()
            self._generation_count = 0

    def _generate(self, req: dict) -> dict:
        """Execute one image generation. req = {id, prompt, output}."""
        request_id = req["id"]
        prompt = req["prompt"]
        output_path = req["output"]

        os.makedirs(os.path.dirname(os.path.abspath(output_path)) or ".", exist_ok=True)
        t0 = time.time()

        if "accounts.google.com" in self.page.url:
            return {"id": request_id, "status": "error", "error": "login required: user is logged out (please run scripts/bridges/login.py to sign in)", "profile": self.profile_id}

        try:
            # Step 1: Ensure Gemini panel is open
            ta = self.page.locator('textarea:visible').first
            panel_open = False
            try:
                if ta.is_visible():
                    panel_open = True
            except Exception:
                pass

            if not panel_open:
                _log(f"[profile-{self.profile_id}][{request_id}] clicking insert-generated-image...")
                btn = self.page.locator(
                    'button.insert-generated-image, '
                    '[data-view-id="insert-generated-image"], '
                    'div[role="button"]:has-text("Nano Banana Pro"), '
                    'button:has-text("Nano Banana Pro")'
                ).last
                try:
                    btn.click(force=True, timeout=5000)
                except PlaywrightTimeout:
                    _log(f"[profile-{self.profile_id}][{request_id}] first click timed out — recovery")
                    try:
                        self.page.keyboard.press("Escape")
                        self.page.wait_for_timeout(500)
                    except Exception:
                        pass
                    btn.click(force=True, timeout=5000)

                # Wait for any textarea to become visible
                try:
                    ta.wait_for(state="visible", timeout=25000)
                except PlaywrightTimeout:
                    _log(f"[profile-{self.profile_id}][{request_id}] click confirmed but textarea not found — recovery")
                    raise

            # Step 1.5: Switch to Immagine/Image tab to ensure we are in the image generation view
            _log(f"[profile-{self.profile_id}][{request_id}] switching to Immagine/Image tab...")
            try:
                tab = self.page.locator(
                    '[role="tab"]:has-text("Immagine"), [role="tab"]:has-text("Image"), '
                    'button:has-text("Immagine"), button:has-text("Image"), '
                    'div:has-text("Immagine"), div:has-text("Image")'
                ).first
                tab.click(force=True, timeout=5000)
                self.page.wait_for_timeout(1000)
            except Exception as te:
                _log(f"[profile-{self.profile_id}][{request_id}] warning: failed switching tab directly: {te}")

            # Step 2: Fill prompt
            _log(f"[profile-{self.profile_id}][{request_id}] filling prompt: '{prompt[:60]}...'")
            try:
                ta.wait_for(state="visible", timeout=25000)
            except PlaywrightTimeout:
                _log(f"[profile-{self.profile_id}][{request_id}] textarea not visible — recovery")
                self._fresh_page()
                btn2 = self.page.locator(
                    'button.insert-generated-image, '
                    '[data-view-id="insert-generated-image"], '
                    'div[role="button"]:has-text("Nano Banana Pro"), '
                    'button:has-text("Nano Banana Pro")'
                ).last
                btn2.click(force=True, timeout=5000)
                ta = self.page.locator('textarea:visible').first
                ta.wait_for(state="visible", timeout=25000)

            ta.fill(prompt)
            # fill() is synchronous — no sleep needed

            # Step 3: Select 16:9
            try:
                prop_btn = self.page.locator(
                    '[aria-label="Proporzioni"], '
                    '.image-synthesis [aria-label*="Proporzi"]'
                ).first
                if prop_btn.is_visible():
                    prop_btn.click(force=True, timeout=3000)
                    opt_169 = self.page.locator('*:has-text("16:9")').last
                    opt_169.wait_for(state="visible", timeout=3000)
                    opt_169.click(force=True, timeout=3000)
            except Exception:
                pass

            # Step 4: Submit
            _log(f"[profile-{self.profile_id}][{request_id}] submitting...")
            create_btn = self.page.locator(
                '.image-synthesis-creation-button, button[aria-label="Crea"]'
            ).first
            create_btn.click(force=True, timeout=5000)

            # Step 5: Wait for AI generation (poll for generated images)
            _log(f"[profile-{self.profile_id}][{request_id}] waiting for AI generation...")
            max_wait = 60  # seconds
            poll_interval = 3
            waited = 0
            while waited < max_wait:
                self.page.wait_for_timeout(poll_interval * 1000)
                waited += poll_interval
                imgs_check = self.page.locator(
                    '.docs-content-library-image-generation-item img, '
                    'img[src*="googleusercontent"]'
                ).all()
                if imgs_check:
                    _log(f"[profile-{self.profile_id}][{request_id}] images appeared after {waited}s")
                    break
            else:
                _log(f"[profile-{self.profile_id}][{request_id}] timed out waiting for AI after {max_wait}s")
                try:
                    os.makedirs("data/tmp", exist_ok=True)
                    self.page.screenshot(path="data/tmp/slide_ai_timeout.png")
                    _log(f"[profile-{self.profile_id}][{request_id}] saved timeout screenshot to data/tmp/slide_ai_timeout.png")
                except Exception as se:
                    _log(f"[profile-{self.profile_id}][{request_id}] failed to save timeout screenshot: {se}")

            # Step 6: Extract image
            imgs = self.page.locator(
                '.docs-content-library-image-generation-item img, '
                'img[src*="googleusercontent"]'
            ).all()
            _log(f"[profile-{self.profile_id}][{request_id}] found {len(imgs)} candidate images")

            saved = False
            image_bytes = b""
            if imgs:
                for img in imgs:
                    try:
                        src = img.get_attribute("src") or ""
                        if "googleusercontent" in src or "blob:" in src:
                            response = self.page.request.get(src, timeout=15000)
                            if response.status == 200:
                                image_bytes = response.body()
                                saved_format = _save_image_bytes(image_bytes, output_path)
                                elapsed = (time.time() - t0) * 1000
                                _log(
                                    f"[profile-{self.profile_id}][{request_id}] SUCCESS → "
                                    f"{output_path} ({len(image_bytes)} bytes, {saved_format}, {elapsed:.0f}ms)"
                                )
                                saved = True
                                break
                    except Exception as e:
                        _log(f"[profile-{self.profile_id}][{request_id}] extraction attempt failed: {e}")

            # Step 7: Fallback
            if not saved:
                _log(f"[profile-{self.profile_id}][{request_id}] fallback: File→Download→PNG...")
                try:
                    file_menu = self.page.locator("#docs-file-menu")
                    file_menu.click(timeout=5000)
                    download_item = self.page.locator(
                        '.apps-menuitem:has-text("Scarica"), '
                        '.apps-menuitem:has-text("Download")'
                    ).first
                    download_item.wait_for(state="visible", timeout=5000)
                    download_item.hover(timeout=3000)
                    png_item = self.page.locator('.apps-menuitem:has-text("PNG")').first
                    with self.page.expect_download(timeout=15000) as download_info:
                        png_item.click(timeout=5000)
                    download = download_info.value
                    temp_download_path = output_path + ".download"
                    download.save_as(temp_download_path)
                    with open(temp_download_path, "rb") as f:
                        image_bytes = f.read()
                    saved_format = _save_image_bytes(image_bytes, output_path)
                    try:
                        os.remove(temp_download_path)
                    except Exception:
                        pass
                    elapsed = (time.time() - t0) * 1000
                    _log(
                        f"[profile-{self.profile_id}][{request_id}] fallback saved → "
                        f"{output_path} ({len(image_bytes)} bytes, {saved_format}, {elapsed:.0f}ms)"
                    )
                    saved = True
                except Exception as fe:
                    _log(f"[profile-{self.profile_id}][{request_id}] fallback failed: {fe}")

            if not saved:
                return {"id": request_id, "status": "error", "error": "no image extracted", "profile": self.profile_id}

            self._maybe_recycle_page()

            elapsed_ms = int((time.time() - t0) * 1000)

            # Auto-save cookies to ensure session persists across runs/restarts
            try:
                self._persist_storage_state(request_id=request_id, reason="auto-save")
            except Exception as se:
                _log(f"[profile-{self.profile_id}][{request_id}] failed to auto-save cookies: {se}")

            return {
                "id": request_id,
                "status": "ok",
                "output": output_path,
                "elapsed_ms": elapsed_ms,
                "bytes": len(image_bytes),
                "profile": self.profile_id,
            }

        except PlaywrightTimeout as e:
            elapsed_ms = int((time.time() - t0) * 1000)
            _log(f"[profile-{self.profile_id}][{request_id}] timeout after {elapsed_ms}ms: {e}")
            try:
                os.makedirs("data/tmp", exist_ok=True)
                self.page.screenshot(path="data/tmp/slide_error.png")
                _log(f"[profile-{self.profile_id}][{request_id}] saved error screenshot to data/tmp/slide_error.png")
            except Exception as se:
                _log(f"[profile-{self.profile_id}][{request_id}] failed to save screenshot: {se}")
            try:
                self._fresh_page()
            except Exception:
                pass
            return {"id": request_id, "status": "error", "error": f"timeout after {elapsed_ms}ms: {e}", "profile": self.profile_id}
        except Exception as e:
            _log(f"[profile-{self.profile_id}][{request_id}] error: {traceback.format_exc()}")
            try:
                os.makedirs("data/tmp", exist_ok=True)
                self.page.screenshot(path="data/tmp/slide_error.png")
                _log(f"[profile-{self.profile_id}][{request_id}] saved error screenshot to data/tmp/slide_error.png")
            except Exception as se:
                _log(f"[profile-{self.profile_id}][{request_id}] failed to save screenshot: {se}")
            try:
                self._fresh_page()
            except Exception:
                pass
            return {"id": request_id, "status": "error", "error": f"{type(e).__name__}: {e}", "profile": self.profile_id}

    # ── Health ────────────────────────────────────────────────────────

    def health(self) -> dict:
        if not self._warmed.is_set():
            return {"status": "error", "error": "not warmed"}
        if not self.is_alive():
            return {"status": "error", "error": "thread died"}
        if self.page is None or self.page.is_closed():
            return {"status": "error", "error": "page closed"}
        if "accounts.google.com" in self.page.url:
            return {"status": "error", "error": "login required: user is logged out (please run scripts/bridges/login.py to sign in)"}
        return {"status": "ok"}

    # ── Stop ──────────────────────────────────────────────────────────

    def stop(self) -> None:
        self._running = False
        # Wake up the thread if it's blocked on in_queue.get()
        try:
            self.in_queue.put_nowait(None)
        except queue.Full:
            pass  # queue has a pending request — thread will stop after processing it


# ── Dispatcher (main thread) ───────────────────────────────────────────────

class SlideDispatcher:
    """Manages the single ProfileWorker thread and stdin requests."""

    def __init__(self, num_profiles: int, headful: bool = False) -> None:
        self.num_profiles = 1
        self.headful = headful
        self.profiles: list[ProfileWorker] = []
        self._shutdown_called = False

    def warmup_all(self) -> dict:
        """Start the single profile worker and wait for it to be ready."""
        self.profiles = [ProfileWorker(0, headful=self.headful)]
        for pw in self.profiles:
            pw.start()
        if not self.profiles[0]._warmed.wait(timeout=30):
            _log("slide_worker: profile-0 warmup timed out")
            return {"status": "error", "error": "profile-0 warmup timed out"}
        if self.profiles[0]._warmup_error:
            _log(f"slide_worker: profile-0 warmup failed immediately: {self.profiles[0]._warmup_error}")
            return {"status": "error", "error": self.profiles[0]._warmup_error}
        _log("slide_worker: profile ready")
        return {"status": "ready", "profiles": 1}

    def dispatch_generate(self, request_id: str, prompt: str, output_path: str) -> dict:
        """Enqueue the request on the single profile and block for the result."""
        req = {"id": request_id, "prompt": prompt, "output": output_path}
        pw = self.profiles[0]
        pw.in_queue.put(req)
        _log(f"slide_worker: [{request_id}] dispatched to profile-{pw.profile_id}")
        return pw.out_queue.get()

    def health_all(self) -> dict:
        """Check all profiles."""
        statuses = {}
        all_ok = True
        for pw in self.profiles:
            h = pw.health()
            statuses[str(pw.profile_id)] = h["status"]
            if h["status"] != "ok":
                all_ok = False
        return {"status": "ok" if all_ok else "degraded", "profiles": statuses}

    def shutdown_all(self) -> None:
        """Stop all profile workers and wait for them to join."""
        if self._shutdown_called:
            return
        self._shutdown_called = True
        _log("slide_worker: shutting down all profiles...")
        for pw in self.profiles:
            pw.stop()
        for pw in self.profiles:
            pw.join(timeout=10)
            if pw.is_alive():
                _log(f"slide_worker: profile-{pw.profile_id} did not stop within 10s")
        _log("slide_worker: all profiles stopped")


# ── Main loop ───────────────────────────────────────────────────────────────

def main() -> None:
    parser = argparse.ArgumentParser(description="PipelineGen persistent Chrome/Playwright image generation worker")
    parser.add_argument("--profiles", type=int, default=1, help="Number of concurrent Chrome profiles (default: 1)")
    parser.add_argument("--headful", action="store_true", help="Run browsers in headful mode")
    args = parser.parse_args()

    num_profiles = max(1, args.profiles)
    dispatcher = SlideDispatcher(num_profiles, headful=args.headful)
    _log(f"slide_worker: started with {num_profiles} profile(s), waiting for commands on stdin...")

    # Graceful shutdown on SIGTERM/SIGINT
    def _handle_signal(signum, frame):
        _log(f"slide_worker: received signal {signum}, shutting down...")
        dispatcher.shutdown_all()
        sys.exit(0)

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)

    # Read stdin line by line
    try:
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue

            try:
                req = json.loads(line)
            except json.JSONDecodeError as e:
                _error(None, f"invalid JSON: {e}")
                continue

            action = req.get("action", "")

            if action == "warmup":
                try:
                    resp = dispatcher.warmup_all()
                    _respond(resp)
                except Exception as e:
                    _error(None, f"warmup failed: {e}")

            elif action == "generate":
                request_id = req.get("id", "")
                prompt = req.get("prompt", "")
                output_path = req.get("output", "")
                if not prompt:
                    _error(request_id, "missing prompt")
                    continue
                if not output_path:
                    _error(request_id, "missing output path")
                    continue
                result = dispatcher.dispatch_generate(request_id, prompt, output_path)
                _respond(result)

            elif action == "health":
                _respond(dispatcher.health_all())

            elif action == "quit":
                _log("slide_worker: received quit command")
                dispatcher.shutdown_all()
                _respond({"status": "shutdown"})
                break

            else:
                _error(req.get("id"), f"unknown action: {action}")
    finally:
        _log("slide_worker: main loop exited — shutting down...")
        dispatcher.shutdown_all()

    _log("slide_worker: exiting")


if __name__ == "__main__":
    main()
