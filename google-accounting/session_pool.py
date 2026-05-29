"""Warm session pool for Google Vids automation.

Pre-opens browser contexts at startup so image/video generation
requests can reuse them instead of spinning up fresh browsers each time.
"""

import asyncio
import logging
import time
from pathlib import Path
from typing import Optional

from playwright.async_api import async_playwright, Browser, BrowserContext, Page

from config import get_session_path, DOWNLOAD_DIR

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
    """Pool of warm Playwright browser contexts for Google Vids."""

    def __init__(self):
        self._playwright = None
        self._browser: Optional[Browser] = None
        self._sessions: dict[str, list[WarmSession]] = {}  # account -> sessions
        self._lock = asyncio.Lock()
        self._started = False

    async def start(self):
        """Initialize Playwright and launch the browser (call at startup)."""
        if self._started:
            return

        log.info("Starting session pool...")
        self._playwright = await async_playwright().start()

        launch_args = [
            "--disable-blink-features=AutomationControlled",
        ]

        self._browser = await self._playwright.chromium.launch(
            headless=True,
            args=launch_args,
            channel="chrome",
        )
        self._started = True
        log.info("Session pool started (browser launched)")

    async def stop(self):
        """Close all warm sessions and the browser."""
        if not self._started:
            return

        log.info("Stopping session pool...")
        async with self._lock:
            for account, sessions in self._sessions.items():
                for session in sessions:
                    await session.close()
            self._sessions.clear()

        if self._browser:
            await self._browser.close()
            self._browser = None
        if self._playwright:
            await self._playwright.stop()
            self._playwright = None

        self._started = False
        log.info("Session pool stopped")

    async def _create_context(self, account: str) -> BrowserContext:
        """Create a new browser context with session state."""
        session_path = get_session_path(account)
        if not session_path.exists():
            raise FileNotFoundError(
                f"Session not found for account '{account}' at {session_path}. "
                "Run login first."
            )

        user_agent = (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
            "AppleWebKit/537.36 (KHTML, like Gecko) "
            "Chrome/129.0.0.0 Safari/537.36"
        )

        context = await self._browser.new_context(
            storage_state=str(session_path),
            user_agent=user_agent,
            viewport={"width": 1920, "height": 1080},
            device_scale_factor=1,
        )

        # Stealth scripts
        await context.add_init_script("""
            Object.defineProperty(navigator, 'webdriver', {
                get: () => undefined
            });
            window.chrome = { runtime: {} };
            Object.defineProperty(navigator, 'languages', {
                get: () => ['it-IT', 'it', 'en-US', 'en']
            });
            Object.defineProperty(navigator, 'plugins', {
                get: () => [1, 2, 3]
            });
        """)

        return context

    async def _warmup_context(self, context: BrowserContext) -> bool:
        """Navigate to Google Vids home to warm up the context.

        Returns True if the context is ready, False if it needs recycling.
        """
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

    async def acquire(self, account: str) -> WarmSession:
        """Acquire a warm session for the given account.

        Creates new contexts if the pool is empty or all are in use.
        """
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


# Global singleton
pool = SessionPool()
