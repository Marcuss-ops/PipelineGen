import asyncio
import json
import logging
import time
import random
import os
from pathlib import Path
from playwright.async_api import async_playwright, Browser, BrowserContext, Page
from config import get_session_path, DOWNLOAD_DIR, GOOGLE_VIDS_BASE_URL

log = logging.getLogger("AutomationEngine")
SELECTOR_REPORT_FILE = os.getenv("GOOGLE_ACCOUNTING_SELECTOR_REPORT_FILE", "").strip()


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
    def __init__(self, account: str = None, headless: bool = True):
        self.account = account
        self.headless = headless
        self.browser: Browser = None
        self.context: BrowserContext = None
        self.session_path = get_session_path(account)
        
        if not self.session_path.exists():
            raise FileNotFoundError(f"Sessione non trovata per account '{account or 'default'}' in {self.session_path}. Eseguire prima il login.")

    async def __aenter__(self):
        log.info("Starting browser context for account=%s headless=%s", self.account or "default", self.headless)
        self.playwright = await async_playwright().start()
        
        # Argomenti anti-rilevamento robot
        launch_args = [
            "--disable-blink-features=AutomationControlled",
        ]
        
        self.browser = await self.playwright.chromium.launch(
            headless=self.headless,
            args=launch_args,
            channel="chrome"
        )
        
        # Stealth: User agent reale e viewport standard
        user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
        
        self.context = await self.browser.new_context(
            storage_state=str(self.session_path),
            user_agent=user_agent,
            viewport={'width': 1920, 'height': 1080},
            device_scale_factor=1,
        )
        
        # Aggiungi script per nascondere Playwright (stealth potenziato)
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
        
        log.info("Browser context ready for account=%s", self.account or "default")
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.context:
            await self.context.close()
        if self.browser:
            await self.browser.close()
        await self.playwright.stop()
