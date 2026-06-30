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
        self.profile_dir = PROFILE_DIR
        self.headful = headful

        # in_queue: max 1 pending request — enforces "1 job at a time per profile"
        self.in_queue: queue.Queue = queue.Queue(maxsize=1)
        # out_queue: result of the currently processing request
        self.out_queue: queue.Queue = queue.Queue()

        self.playwright = None
        self.context = None
        self.page = None
        self._warmed = threading.Event()
        self._running = True
        self._generation_count = 0
        self._max_generations_before_page_recycle = 20

    # ── Warmup ────────────────────────────────────────────────────────

    def warmup(self) -> None:
        """Launch persistent browser, load cookies, navigate to slides.new."""
        _log(f"[profile-{self.profile_id}] warmup: launching browser...")

        self.playwright = sync_playwright().start()
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

        # Load session cookies if available
        if os.path.exists(MASTER_STORAGE):
            try:
                with open(MASTER_STORAGE) as f:
                    sdata = json.load(f)
                    if "cookies" in sdata and sdata["cookies"]:
                        self.context.add_cookies(sdata["cookies"])
                        _log(f"[profile-{self.profile_id}] warmup: loaded session cookies")
            except Exception as e:
                _log(f"[profile-{self.profile_id}] warmup: failed to load cookies: {e}")

        _log(f"[profile-{self.profile_id}] warmup: navigating to slides.new...")
        self.page = self.context.new_page()
        try:
            self.page.goto("https://slides.new", wait_until="domcontentloaded", timeout=30000)
            self.page.wait_for_load_state("networkidle", timeout=15000)
        except PlaywrightTimeout:
            _log(f"[profile-{self.profile_id}] warmup: slides.new timed out — continuing")

        self._warmed.set()
        _log(f"[profile-{self.profile_id}] warmup: ready")

    # ── Main loop ──────────────────────────────────────────────────────

    def run(self) -> None:
        """Thread target: warm up, then process queue until stopped."""
        try:
            self.warmup()
        except Exception as e:
            _log(f"[profile-{self.profile_id}] warmup failed: {e}")
            self._warmed.clear()
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
                storage = self.context.storage_state()
                storage_path = f"{MASTER_STORAGE}.profile_{self.profile_id}"
                os.makedirs(os.path.dirname(storage_path) or ".", exist_ok=True)
                with open(storage_path, "w") as f:
                    json.dump(storage, f, indent=2)
                _log(f"[profile-{self.profile_id}] saved session to {storage_path}")
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
            self.page.goto("https://slides.new", wait_until="domcontentloaded", timeout=30000)
            self.page.wait_for_load_state("networkidle", timeout=15000)
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

        try:
            # Step 1: Click insert-generated-image
            _log(f"[profile-{self.profile_id}][{request_id}] clicking insert-generated-image...")
            btn = self.page.locator(
                'button.insert-generated-image, '
                '[data-view-id="insert-generated-image"], '
                'div:has-text("Nano Banana Pro")'
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

            # Wait for the textarea to become visible (was fixed sleep(2))
            ta_wait = self.page.locator('.image-synthesis textarea, textarea').first
            try:
                ta_wait.wait_for(state="visible", timeout=10000)
            except PlaywrightTimeout:
                _log(f"[profile-{self.profile_id}][{request_id}] click confirmed but textarea not found — recovery")
                raise

            # Step 2: Fill prompt
            _log(f"[profile-{self.profile_id}][{request_id}] filling prompt: '{prompt[:60]}...'")
            ta = self.page.locator('.image-synthesis textarea, textarea').first
            try:
                ta.wait_for(state="visible", timeout=10000)
            except PlaywrightTimeout:
                _log(f"[profile-{self.profile_id}][{request_id}] textarea not visible — recovery")
                self._fresh_page()
                btn2 = self.page.locator(
                    'button.insert-generated-image, '
                    '[data-view-id="insert-generated-image"], '
                    'div:has-text("Nano Banana Pro")'
                ).last
                btn2.click(force=True, timeout=5000)
                ta = self.page.locator('.image-synthesis textarea, textarea').first
                ta.wait_for(state="visible", timeout=10000)

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
                                with open(output_path, "wb") as f:
                                    f.write(image_bytes)
                                elapsed = (time.time() - t0) * 1000
                                _log(
                                    f"[profile-{self.profile_id}][{request_id}] SUCCESS → "
                                    f"{output_path} ({len(image_bytes)} bytes, {elapsed:.0f}ms)"
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
                    download.save_as(output_path)
                    image_bytes = open(output_path, "rb").read()
                    elapsed = (time.time() - t0) * 1000
                    _log(
                        f"[profile-{self.profile_id}][{request_id}] fallback saved → "
                        f"{output_path} ({len(image_bytes)} bytes, {elapsed:.0f}ms)"
                    )
                    saved = True
                except Exception as fe:
                    _log(f"[profile-{self.profile_id}][{request_id}] fallback failed: {fe}")

            if not saved:
                return {"id": request_id, "status": "error", "error": "no image extracted", "profile": self.profile_id}

            self._maybe_recycle_page()

            elapsed_ms = int((time.time() - t0) * 1000)
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
                self._fresh_page()
            except Exception:
                pass
            return {"id": request_id, "status": "error", "error": f"timeout after {elapsed_ms}ms: {e}", "profile": self.profile_id}
        except Exception as e:
            _log(f"[profile-{self.profile_id}][{request_id}] error: {traceback.format_exc()}")
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
