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


def _get_realistic_user_agent() -> str:
    """Returns a realistic Chrome user-agent matching the system-installed Chrome version."""
    import subprocess
    import re
    try:
        for cmd in [
            ["google-chrome", "--version"],
            ["google-chrome-stable", "--version"],
            ["chromium-browser", "--version"],
            ["chromium", "--version"],
        ]:
            try:
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
                if result.returncode == 0:
                    match = re.search(r"(\d+\.\d+\.\d+\.\d+)", result.stdout)
                    if match:
                        version = match.group(1)
                        major = version.split(".")[0]
                        log.info("Detected Chrome version: %s", version)
                        return (
                            f"Mozilla/5.0 (X11; Linux x86_64) "
                            f"AppleWebKit/537.36 (KHTML, like Gecko) "
                            f"Chrome/{version} Safari/537.36"
                        )
            except (FileNotFoundError, subprocess.TimeoutExpired):
                continue
    except Exception as e:
        log.warning("Could not detect Chrome version: %s", e)
    # Fallback: recent stable Chrome version
    log.warning("Falling back to hardcoded Chrome user-agent")
    return (
        "Mozilla/5.0 (X11; Linux x86_64) "
        "AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/136.0.0.0 Safari/537.36"
    )


# ---------------------------------------------------------------------------
# Comprehensive stealth init script - defeats most bot-detection fingerprinting
# ---------------------------------------------------------------------------
_STEALTH_INIT_SCRIPT = """
// 1. Hide webdriver property
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });

// 2. Realistic window.chrome object
window.chrome = {
    app: {
        isInstalled: false,
        InstallState: { DISABLED: 'disabled', INSTALLED: 'installed', NOT_INSTALLED: 'not_installed' },
        RunningState: { CANNOT_RUN: 'cannot_run', READY_TO_RUN: 'ready_to_run', RUNNING: 'running' }
    },
    runtime: {
        OnInstalledReason: { CHROME_UPDATE: 'chrome_update', INSTALL: 'install', SHARED_MODULE_UPDATE: 'shared_module_update', UPDATE: 'update' },
        OnRestartRequiredReason: { APP_UPDATE: 'app_update', GC_PRESSURE: 'gc_pressure', OS_UPDATE: 'os_update' },
        PlatformArch: { ARM: 'arm', ARM64: 'arm64', MIPS: 'mips', MIPS64: 'mips64', X86_32: 'x86-32', X86_64: 'x86-64' },
        PlatformNaclArch: { ARM: 'arm', MIPS: 'mips', MIPS64: 'mips64', X86_32: 'x86-32', X86_64: 'x86-64' },
        PlatformOs: { ANDROID: 'android', CROS: 'cros', LINUX: 'linux', MAC: 'mac', OPENBSD: 'openbsd', WIN: 'win' },
        RequestUpdateCheckStatus: { NO_UPDATE: 'no_update', THROTTLED: 'throttled', UPDATE_AVAILABLE: 'update_available' }
    }
};

// 3. Realistic navigator.plugins (not empty, not [1,2,3])
const makePlugin = (name, description, filename, mimeTypes) => {
    const plugin = Object.create(Plugin.prototype);
    Object.defineProperties(plugin, {
        name: { value: name },
        description: { value: description },
        filename: { value: filename },
        length: { value: mimeTypes.length }
    });
    mimeTypes.forEach((mt, i) => {
        const mime = Object.create(MimeType.prototype);
        Object.defineProperties(mime, {
            type: { value: mt.type },
            suffixes: { value: mt.suffixes },
            description: { value: mt.description },
            enabledPlugin: { value: plugin }
        });
        Object.defineProperty(plugin, i, { value: mime });
        Object.defineProperty(plugin, mt.type, { value: mime });
    });
    return plugin;
};
try {
    const plugins = [
        makePlugin('Chrome PDF Plugin', 'Portable Document Format', 'internal-pdf-viewer', [
            { type: 'application/x-google-chrome-pdf', suffixes: 'pdf', description: 'Portable Document Format' }
        ]),
        makePlugin('Chrome PDF Viewer', '', 'mhjfbmdgcfjbbpaeojofohoefgiehjai', [
            { type: 'application/pdf', suffixes: 'pdf', description: '' }
        ]),
        makePlugin('Native Client', '', 'internal-nacl-plugin', [
            { type: 'application/x-nacl', suffixes: '', description: 'Native Client Executable' },
            { type: 'application/x-pnacl', suffixes: '', description: 'Portable Native Client Executable' }
        ])
    ];
    Object.defineProperty(navigator, 'plugins', {
        get: () => {
            const arr = Object.create(PluginArray.prototype);
            plugins.forEach((p, i) => {
                Object.defineProperty(arr, i, { value: p });
                Object.defineProperty(arr, p.name, { value: p });
            });
            Object.defineProperty(arr, 'length', { value: plugins.length });
            arr.item = (i) => plugins[i] || null;
            arr.namedItem = (name) => plugins.find(p => p.name === name) || null;
            arr.refresh = () => {};
            return arr;
        }
    });
} catch(e) {}

// 4. Language
Object.defineProperty(navigator, 'languages', { get: () => ['it-IT', 'it', 'en-US', 'en'] });

// 5. Hardware properties (typical workstation)
try {
    Object.defineProperty(navigator, 'hardwareConcurrency', { get: () => 8 });
} catch(e) {}
try {
    Object.defineProperty(navigator, 'deviceMemory', { get: () => 8 });
} catch(e) {}

// 6. Permissions API - avoid returning 'denied' for automation-related queries
const originalQuery = window.navigator.permissions ? window.navigator.permissions.query.bind(window.navigator.permissions) : null;
if (originalQuery) {
    window.navigator.permissions.query = (parameters) => (
        parameters.name === 'notifications'
            ? Promise.resolve({ state: Notification.permission })
            : originalQuery(parameters)
    );
}

// 7. Network connection
try {
    Object.defineProperty(navigator, 'connection', {
        get: () => ({
            effectiveType: '4g',
            rtt: 50,
            downlink: 10,
            saveData: false
        })
    });
} catch(e) {}

// 8. Screen properties
try {
    Object.defineProperty(screen, 'colorDepth', { get: () => 24 });
    Object.defineProperty(screen, 'pixelDepth', { get: () => 24 });
} catch(e) {}

// 9. Prevent iframe detection of webdriver
const originalContentWindow = HTMLIFrameElement.prototype.__lookupGetter__('contentWindow');
if (originalContentWindow) {
    Object.defineProperty(HTMLIFrameElement.prototype, 'contentWindow', {
        get: function() {
            const cw = originalContentWindow.call(this);
            if (cw) {
                try {
                    Object.defineProperty(cw.navigator, 'webdriver', { get: () => undefined });
                } catch(e) {}
            }
            return cw;
        }
    });
}
"""


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
            "--disable-features=IsolateOrigins,site-per-process",
            "--disable-site-isolation-trials",
            "--no-sandbox",
            "--disable-setuid-sandbox",
            "--disable-dev-shm-usage",
            "--disable-accelerated-2d-canvas",
            "--no-first-run",
            "--no-zygote",
            "--disable-gpu",
            "--window-size=1920,1080",
        ]
        
        user_agent = _get_realistic_user_agent()
        
        # If legacy session JSON exists but persistent profile is empty, we'll import cookies after launch
        legacy_state_path = None
        if self.session_path.exists() and not (self.profile_path.exists() and any(self.profile_path.iterdir())):
            log.info("Legacy storage state JSON found — will import cookies after context launch")
            legacy_state_path = self.session_path

        self.context = await self.playwright.chromium.launch_persistent_context(
            user_data_dir=str(self.profile_path),
            headless=self.headless,
            args=launch_args,
            channel="chrome",
            user_agent=user_agent,
            viewport={'width': 1920, 'height': 1080},
            device_scale_factor=1,
            locale="it-IT",
            timezone_id="Europe/Rome",
        )

        # Import legacy cookies if needed
        if legacy_state_path:
            try:
                import json as _json
                state = _json.loads(legacy_state_path.read_text(encoding="utf-8"))
                cookies = state.get("cookies", [])
                if cookies:
                    await self.context.add_cookies(cookies)
                    log.info("Imported %d cookies from legacy session JSON", len(cookies))
            except Exception as e:
                log.warning("Failed to import legacy cookies: %s", e)
        
        await self.context.add_init_script(_STEALTH_INIT_SCRIPT)
        
        log.info("Persistent browser context ready for account=%s", self.account or "default")
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self._external_context or self._external_page:
            return  # Don't close external context or page
        if self.context:
            await self.context.close()
        if hasattr(self, 'playwright') and self.playwright:
            await self.playwright.stop()
