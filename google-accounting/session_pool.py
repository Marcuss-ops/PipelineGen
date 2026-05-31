"""Warm session pool for Google Vids automation.

Pre-opens browser contexts and pre-loads tabs (pages) at startup/on-demand
so image/video generation requests can reuse them instead of spinning up
fresh browsers and loading heavy video editor pages each time.
"""

import asyncio
import logging
import time
from pathlib import Path
from typing import Optional

from playwright.async_api import async_playwright, Browser, BrowserContext, Page

from config import get_session_path, get_profile_path, DOWNLOAD_DIR
from automation.base import _get_realistic_user_agent, _STEALTH_INIT_SCRIPT

log = logging.getLogger("SessionPool")

# Maximum number of warm contexts per account
MAX_WARM_CONTEXTS = 2
# Maximum age (seconds) before a context is recycled
CONTEXT_MAX_AGE = 1800  # 30 minutes


class WarmSession:
    """A single pre-warmed browser context ready for reuse."""

    def __init__(self, context: BrowserContext, account: str, created_at: float):
        self.context = context
        self.account = account
        self.created_at = created_at
        self.in_use = False
        self.page_count = 0

    @property
    def age(self) -> float:
        return time.time() - self.created_at

    @property
    def is_expired(self) -> bool:
        return self.age > CONTEXT_MAX_AGE

    async def close(self):
        try:
            await self.context.close()
        except Exception:
            pass


class SessionPool:
    """Pool of warm Playwright browser contexts and loaded Pages for Google Vids."""

    def __init__(self):
        self._playwright = None
        self._sessions: dict[str, list[WarmSession]] = {}  # account -> sessions
        self._warm_pages: dict[tuple[str, str], list[Page]] = {}  # (account, video_id) -> pages
        self._active_pages: set[Page] = set()
        self._lock = asyncio.Lock()
        self._started = False

    async def start(self):
        """Initialize Playwright (call at startup)."""
        if self._started:
            return

        log.info("Starting session pool...")
        self._playwright = await async_playwright().start()
        self._started = True
        log.info("Session pool started")

    async def stop(self):
        """Close all warm sessions and loaded pages."""
        if not self._started:
            return

        log.info("Stopping session pool...")
        async with self._lock:
            # Close active and warm pages
            for page in list(self._active_pages):
                try:
                    await page.close()
                except Exception:
                    pass
            self._active_pages.clear()

            for pages in self._warm_pages.values():
                for page in pages:
                    try:
                        await page.close()
                    except Exception:
                        pass
            self._warm_pages.clear()

            for account, sessions in self._sessions.items():
                for session in sessions:
                    await session.close()
            self._sessions.clear()

        if self._playwright:
            await self._playwright.stop()
            self._playwright = None

        self._started = False
        log.info("Session pool stopped")

    async def _create_context(self, account: str) -> BrowserContext:
        """Create a new persistent browser context using native Snap Chromium subprocess and CDP."""
        profile_path = get_profile_path(account)
        
        log.info("Cleaning stale lock files for profile: %s", profile_path)
        import os
        for lock_name in ["SingletonLock", "SingletonSocket", "SingletonCookie"]:
            lock_file = profile_path / lock_name
            if lock_file.exists() or lock_file.is_symlink():
                try:
                    if lock_file.is_symlink():
                        lock_file.unlink()
                    else:
                        os.unlink(str(lock_file))
                    log.info("Cleared stale lock file: %s", lock_name)
                except Exception as e:
                    log.warning("Could not clear lock %s: %s", lock_name, e)

        import subprocess
        snap_executable = "/snap/bin/chromium"
        
        # Costruiamo gli argomenti di lancio nativi
        launch_args = [
            snap_executable,
            "--remote-debugging-port=9222",
            f"--user-data-dir={profile_path}",
            "--password-store=basic",
            "--profile-directory=Profile 1",
            "--no-first-run",
            "--start-maximized",
        ]
        
        # Supporto headless nativo di Chrome per il background
        # Usiamo --headless=new per una perfetta esecuzione senza dipendere da X11/Xvfb!
        launch_args.append("--headless=new")
        
        env = os.environ.copy()
        
        log.info("Launching native Snap Chromium via subprocess: %s", " ".join(launch_args))
        # Chiudiamo eventuali istanze appese sulla porta 9222 prima dell'avvio
        os.system("killall -9 chrome chromium-browser chromium 2>/dev/null")
        await asyncio.sleep(1)
        
        subprocess.Popen(launch_args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        await asyncio.sleep(5)
        
        # Connessione CDP
        log.info("Connecting to Snap Chromium via CDP over port 9222...")
        browser = await self._playwright.chromium.connect_over_cdp("http://localhost:9222")
        context = browser.contexts[0]
        
        # Aggiungiamo un listener per chiudere il processo quando il contesto viene chiuso
        async def on_close():
            log.info("CDP context closed, ensuring native Chromium process is terminated")
            os.system("killall -9 chrome chromium-browser chromium 2>/dev/null")
        context.on("close", lambda ctx: asyncio.create_task(on_close()))
        
        return context

    async def _warmup_context(self, context: BrowserContext) -> bool:
        """Navigate to Google Vids home to warm up the context."""
        try:
            page = await context.new_page()
            await page.goto(
                "https://docs.google.com/videos/u/0/?usp=direct_url",
                wait_until="domcontentloaded",
                timeout=30000,
            )
            await page.wait_for_timeout(3000)

            # Quick sanity check: are we on Google Vids?
            ready = "docs.google.com/videos" in page.url
            await page.close()
            return ready
        except Exception as e:
            log.warning("Warmup failed: %s", e)
            try:
                await page.close()
            except Exception:
                pass
            return False

    async def warmup_account(self, account: str):
        """Pre-warm a browser context for the given account at startup."""
        if not self._started:
            await self.start()
        log.info("Pre-warming context for account=%s at startup...", account)
        async with self._lock:
            sessions = self._sessions.setdefault(account, [])
            if len(sessions) < MAX_WARM_CONTEXTS:
                try:
                    context = await self._create_context(account)
                    ready = await self._warmup_context(context)
                    if ready:
                        session = WarmSession(context, account, time.time())
                        sessions.append(session)
                        log.info("Pre-warmed context ready for account=%s", account)
                    else:
                        await context.close()
                        log.warning("Pre-warm failed for account=%s", account)
                except Exception as e:
                    log.error("Failed to pre-warm account %s: %s", account, e)

    async def acquire(self, account: str) -> WarmSession:
        """Acquire a warm session for the given account."""
        if not self._started:
            await self.start()

        async with self._lock:
            sessions = self._sessions.get(account, [])

            # Find an available, non-expired session
            for session in sessions:
                if not session.in_use and not session.is_expired:
                    session.in_use = True
                    session.page_count += 1
                    log.info(
                        "Reused warm session for account=%s (age=%.0fs, uses=%d)",
                        account, session.age, session.page_count,
                    )
                    return session

            # Recycle expired sessions
            expired = [s for s in sessions if s.is_expired]
            for s in expired:
                await s.close()
                sessions.remove(s)

            # Create new context if under limit
            if len(sessions) < MAX_WARM_CONTEXTS:
                log.info("Creating new warm context for account=%s", account)
                try:
                    context = await self._create_context(account)
                    ready = await self._warmup_context(context)
                    if ready:
                        session = WarmSession(context, account, time.time())
                        session.in_use = True
                        sessions.append(session)
                        self._sessions[account] = sessions
                        log.info("New warm session ready for account=%s", account)
                        return session
                    else:
                        await context.close()
                        log.warning("Context warmup failed, falling back to ad-hoc")
                except Exception as e:
                    log.error("Failed to create context: %s", e)

            # All in use or creation failed - create a temporary one
            log.info("No warm session available, creating ad-hoc for account=%s", account)
            context = await self._create_context(account)
            session = WarmSession(context, account, time.time())
            session.in_use = True
            return session

    async def release(self, session: WarmSession):
        """Release a session back to the pool."""
        async with self._lock:
            session.in_use = False

            # If too old, close it
            if session.is_expired:
                await session.close()
                sessions = self._sessions.get(session.account, [])
                if session in sessions:
                    sessions.remove(session)
                log.info("Expired session closed for account=%s", session.account)
            else:
                log.info("Session released for account=%s (age=%.0fs)", session.account, session.age)

    # ── Page/Tab pooling ──────────────────────────────────────────────────────
    async def acquire_page(self, account: str, video_id: str) -> Page:
        """Acquire an already loaded Google Vids page for the given account/video."""
        if not self._started:
            await self.start()

        account = account or "favamassimo"
        key = (account, video_id)

        async with self._lock:
            pages = self._warm_pages.setdefault(key, [])
            while pages:
                page = pages.pop(0)
                if not page.is_closed():
                    self._active_pages.add(page)
                    log.info("Acquired warm preloaded page for account=%s video=%s", account, video_id)
                    return page

            # No warm page: acquire a context to spawn a new page
            log.info("No warm page for account=%s video=%s, creating new...", account, video_id)
            
            # Find or create a session
            sessions = self._sessions.setdefault(account, [])
            session = None
            for s in sessions:
                if not s.is_expired:
                    session = s
                    break
            
            if not session:
                context = await self._create_context(account)
                session = WarmSession(context, account, time.time())
                sessions.append(session)

            page = await session.context.new_page()
            shared_id = "1Kn_99mlEjC8kn4_dLBgfeoKZw7ohMz-TjBAvx3szNoQ"
            if video_id == "new":
                url = f"https://docs.google.com/videos/d/{shared_id}/edit"
                log.info("Opening shared Google Vids project: %s", url)
                await page.goto(url, wait_until="domcontentloaded", timeout=60000)
            else:
                url = f"https://docs.google.com/videos/d/{video_id}/edit"
                log.info("Loading heavy Google Vids editor URL: %s", url)
                await page.goto(url, wait_until="domcontentloaded", timeout=60000)
            await page.wait_for_timeout(6000)  # Wait for UI stabilization

            self._active_pages.add(page)
            return page

    async def release_page(self, account: str, video_id: str, page: Page):
        """Release a page back to the warm page pool."""
        account = account or "favamassimo"
        key = (account, video_id)

        async with self._lock:
            if page in self._active_pages:
                self._active_pages.remove(page)

            if not page.is_closed():
                # Store page back in the pool
                self._warm_pages.setdefault(key, []).append(page)
                log.info("Page released back to warm pool for account=%s video=%s", account, video_id)
            else:
                log.warning("Released page was closed, discarding")


# Global singleton
pool = SessionPool()
