import asyncio
import functools
import json
import logging
import time
import random
import os
from pathlib import Path
from typing import Callable, TypeVar
from playwright.async_api import async_playwright, Browser, BrowserContext, Page
from config import get_session_path, get_profile_path, DOWNLOAD_DIR, GOOGLE_VIDS_BASE_URL

log = logging.getLogger("AutomationEngine")
SELECTOR_REPORT_FILE = os.getenv("GOOGLE_ACCOUNTING_SELECTOR_REPORT_FILE", "").strip()


# ---------------------------------------------------------------------------
# Retry decorator with exponential backoff
# ---------------------------------------------------------------------------
def retry_async(max_retries: int = 3, base_delay: float = 5.0, max_delay: float = 60.0, jitter: bool = True):
    """Decorator that retries an async function with exponential backoff.
    
    Args:
        max_retries: Maximum number of retry attempts (total calls = max_retries + 1).
        base_delay: Initial delay in seconds between retries.
        max_delay: Maximum delay cap in seconds.
        jitter: If True, adds random jitter to prevent thundering herd.
    """
    def decorator(func: Callable):
        @functools.wraps(func)
        async def wrapper(*args, **kwargs):
            last_exception = None
            for attempt in range(max_retries + 1):
                try:
                    return await func(*args, **kwargs)
                except Exception as e:
                    last_exception = e
                    if attempt < max_retries:
                        delay = min(base_delay * (2 ** attempt), max_delay)
                        if jitter:
                            delay = delay * (0.5 + random.random())
                        log.warning(
                            "Attempt %d/%d for %s failed: %s — retrying in %.1fs",
                            attempt + 1, max_retries + 1, func.__name__, e, delay
                        )
                        await asyncio.sleep(delay)
                    else:
                        log.error(
                            "All %d attempts for %s exhausted. Last error: %s",
                            max_retries + 1, func.__name__, e
                        )
            raise last_exception
        return wrapper
    return decorator


def _append_selector_report(entry: dict) -> None:
    if not SELECTOR_REPORT_FILE:
        return
    report_path = Path(SELECTOR_REPORT_FILE)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    payload: list[dict] = []
    if report_path.exists():
        try:
            existing = json.loads(report_path.read_text(encoding="utf-8"))
            if isinstance(existing, list):
                payload = existing
        except Exception:
            payload = []
    payload.append(entry)
    report_path.write_text(json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8")

async def human_delay(min_ms=500, max_ms=2500):
    """Simula una pausa umana casuale."""
    await asyncio.sleep(random.uniform(min_ms, max_ms) / 1000)

async def human_scroll(page: Page):
    """Simula uno scrolling casuale."""
    try:
        for _ in range(random.randint(1, 3)):
            await page.mouse.wheel(0, random.randint(100, 400))
            await human_delay(300, 800)
            await page.mouse.wheel(0, random.randint(-400, -100))
            await human_delay(300, 800)
    except: pass

class BaseAutomation:
    def __init__(self, account: str = None, headless: bool = True, external_context: BrowserContext = None, external_page: Page = None):
        self.account = account
        self.headless = headless
        self.browser: Browser = None
        self.context: BrowserContext = external_context
        self.page: Page = external_page
        self._external_page = external_page is not None
        self._external_context = external_context is not None or self._external_page
        self.profile_path = get_profile_path(account)
        self.session_path = get_session_path(account)
        
        login_exists = self.session_path.exists() or (self.profile_path.exists() and any(self.profile_path.iterdir()))
        if not self._external_context and not login_exists:
            raise FileNotFoundError(f"Sessione o profilo Chrome non trovato per account '{account or 'default'}'. Eseguire prima il login.")

    async def __aenter__(self):
        if self._external_page:
            log.info("Using external page for account=%s", self.account or "default")
            return self
        if self._external_context:
            log.info("Using external context for account=%s", self.account or "default")
            return self

        log.info("Starting persistent browser context for account=%s headless=%s", self.account or "default", self.headless)
        self.playwright = await async_playwright().start()
        
        launch_args = [
            "--disable-blink-features=AutomationControlled",
        ]
        
        user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
        
        # If legacy session JSON exists but persistent profile is empty, import it once
        storage_state = None
        if self.session_path.exists() and not (self.profile_path.exists() and any(self.profile_path.iterdir())):
            log.info("Migrating legacy storage state JSON to persistent context profile")
            storage_state = str(self.session_path)

        self.context = await self.playwright.chromium.launch_persistent_context(
            user_data_dir=str(self.profile_path),
            headless=self.headless,
            args=launch_args,
            channel="chrome",
            user_agent=user_agent,
            viewport={'width': 1920, 'height': 1080},
            device_scale_factor=1,
            storage_state=storage_state
        )
        
        await self.context.add_init_script("""
            Object.defineProperty(navigator, 'webdriver', {
                get: () => undefined
            });
            window.chrome = {
                runtime: {}
            };
            Object.defineProperty(navigator, 'languages', {
                get: () => ['it-IT', 'it', 'en-US', 'en']
            });
            Object.defineProperty(navigator, 'plugins', {
                get: () => [1, 2, 3]
            });
        """)
        
        log.info("Persistent browser context ready for account=%s", self.account or "default")
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self._external_context or self._external_page:
            return  # Don't close external context or page
        if self.context:
            await self.context.close()
        if hasattr(self, 'playwright') and self.playwright:
            await self.playwright.stop()
