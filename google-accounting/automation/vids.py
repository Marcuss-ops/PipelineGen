import asyncio
import os
import random
import re
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from playwright.async_api import Page

from drive_client import download_file, list_exported_files
from .base import (
    GOOGLE_VIDS_BASE_URL,
    BaseAutomation,
    human_delay,
    human_scroll,
    log,
)

from .vids_video import GoogleVidsVideoMixin
from .vids_avatar import GoogleVidsAvatarMixin, sync_project

class GoogleVidsAutomation(GoogleVidsVideoMixin, GoogleVidsAvatarMixin, BaseAutomation):
    """Engine per l'automazione di Google Vids."""

    PROMPT_TEXTAREA_SELECTORS = [
        'textarea[aria-label^="Descrivi il tuo video di 8 secondi."]',
        'textarea.javascriptMaterialdesignGm3WizTextFieldOutlined-text-field__input',
        'textarea#c1',
        'textarea',
        '[contenteditable="true"]',
        'div[role="textbox"]',
        'input[type="text"]',
        '[placeholder*="video"]',
        '[placeholder*="prompt"]',
        '[placeholder*="Descrivi"]',
    ]

    GENERATE_BUTTON_SELECTORS = [
        'button.videoGenCreationViewGenerateButton',
        'button[data-view-id="videoGenCreationViewGenerateButton"]',
        'button:has-text("Genera"):not([data-view-id*="getting-started"])',
        'button:has-text("Genera")',
        'button:has-text("Generate")',
        'button:has-text("Crea")',
        'xpath=/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[2]/div[5]/span/div[1]/button',
    ]

    PREVIEW_CONTAINER_SELECTORS = [
        'div.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailVideoGenerationThumbnailContainer',
        'xpath=/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[3]/div',
        'xpath=/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[3]/div/div[1]',
        'div.appsDocsAiGenerativeaiVideoUiSidebarWizVideogenfooterInspirationGalleryVideoOverlay',
    ]

    PREVIEW_VIDEO_SELECTORS = [
        'xpath=/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[3]/div/div[1]/video',
        'div.appsDocsAiGenerativeaiVideoUiSidebarWizVideogenfooterInspirationGalleryVideoOverlay video',
        'video.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailVideoGenerationThumbnail',
        'video.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailInsertableVideoGenerationThumbnail',
        'video',
    ]

    async def _goto_home(self) -> Page:
        page = await self.context.new_page()
        await page.goto("https://docs.google.com/videos/u/0/?usp=direct_url", wait_until="networkidle")
        await page.wait_for_timeout(1500)
        return page

    @staticmethod
    def _extract_vid_id(href: str | None) -> str | None:
        if not href:
            return None
        match = re.search(r"/videos/d/([^/?#]+)/", href)
        if match:
            return match.group(1)
        return None

    @staticmethod
    def _parse_recent_titles(body_text: str) -> list[str]:
        if not body_text:
            return []

        lines = [line.strip() for line in body_text.splitlines()]
        try:
            start = len(lines) - 1 - lines[::-1].index("Video recenti") + 1
        except ValueError:
            return []

        try:
            end = lines.index("Di proprietà di chiunque", start)
        except ValueError:
            end = len(lines)

        titles: list[str] = []
        ignore = {
            "",
            "Video recenti",
            "Inizia un nuovo video",
            "Vids",
            "Salvato in Drive",
            "Riproduci",
            "Condividi",
            "File",
            "Modifica",
            "Visualizza",
            "Inserisci",
            "Formato",
            "Scena",
            "Disponi",
            "Strumenti",
            "Guida",
            "Mostra durata",
        }
        for line in lines[start:end]:
            if line in ignore:
                continue
            if re.fullmatch(r"\d{1,2}\s\w+\s\d{4}", line):
                continue
            if re.fullmatch(r"\d{2}:\d{2}\.\d", line):
                continue
            if line.startswith("Aperto"):
                continue
            if line not in titles:
                values = line.strip()
                if values:
                    titles.append(values)
        return titles

    async def list_projects(self) -> list[dict]:
        """List all Google Vids projects from the home page."""
        page = await self._goto_home()
        try:
            projects: list[dict] = []
            seen_ids: set[str] = set()

            async def add_project(name: str, href: str | None):
                vid_id = self._extract_vid_id(href)
                if not vid_id or vid_id in seen_ids:
                    return
                seen_ids.add(vid_id)
                projects.append({"name": name, "id": vid_id, "url": href or f"{GOOGLE_VIDS_BASE_URL}/{vid_id}/edit"})

            # Primary path: explicit links.
            for locator in [
                'a[href*="/videos/d/"]',
                'a[href*="/videos/u/"]',
            ]:
                count = await page.locator(locator).count()
                for idx in range(count):
                    item = page.locator(locator).nth(idx)
                    href = await item.get_attribute("href")
                    text = (await item.inner_text()).strip()
                    if text:
                        await add_project(text, href)

            if projects:
                return projects

            # Some accounts only expose recent videos after opening the new-video panel.
            try:
                if await page.get_by_text("Inizia un nuovo video", exact=False).count():
                    await page.get_by_text("Inizia un nuovo video", exact=False).first.click(timeout=5000)
                    await page.wait_for_timeout(1500)
            except Exception:
                pass

            # Fallback: parse the "recent videos" section and click titles to resolve IDs.
            body_text = await page.locator("body").inner_text()
            recent_titles = self._parse_recent_titles(body_text)
            for title in recent_titles:
                try:
                    title_loc = page.get_by_text(title, exact=True)
                    if await title_loc.count() == 0:
                        title_loc = page.get_by_text(title, exact=False)
                    if await title_loc.count() == 0:
                        continue
                    await title_loc.first.click(timeout=5000)
                    await page.wait_for_timeout(1500)
                    await add_project(title, page.url)
                    await page.go_back(wait_until="networkidle")
                    await page.wait_for_timeout(1000)
                except Exception:
                    try:
                        await page.goto("https://vids.google.com", wait_until="networkidle")
                        await page.wait_for_timeout(1000)
                    except Exception:
                        pass

            return projects
        finally:
            await page.close()

    async def _get_page(self, video_id: str) -> Page:
        page = await self.context.new_page()
        url = f"{GOOGLE_VIDS_BASE_URL}/{video_id}/edit"
        await human_delay(1000, 3000)
        log.info("Opening Vids page for video_id=%s url=%s", video_id, url)
        await page.goto(url, wait_until="domcontentloaded")
        await asyncio.sleep(8)
        await human_scroll(page)
        log.info("Vids page ready for video_id=%s page_url=%s", video_id, page.url)
        return page

    @staticmethod
    async def _download_direct_url(
        video_src: str,
        raw_path: Path,
        referer: str | None = None,
        cookie_header: str | None = None,
    ) -> bool:
        headers = {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
            "Accept": "*/*",
        }
        if referer:
            headers["Referer"] = referer
        if cookie_header:
            headers["Cookie"] = cookie_header

        request = urllib.request.Request(video_src, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                raw_path.write_bytes(response.read())
            return True
        except urllib.error.HTTPError as exc:
            log.warning("Direct download HTTP error status=%s url=%s", exc.code, video_src[:180])
        except Exception as exc:
            log.warning("Direct download failed url=%s err=%s", video_src[:180], exc)
        return False

    async def _build_cookie_header(self, url: str) -> str | None:
        try:
            cookies = await self.context.cookies(url)
        except Exception as exc:
            log.warning("Unable to load cookies for %s err=%s", url[:180], exc)
            return None
        if not cookies:
            return None
        pairs = []
        for cookie in cookies:
            name = cookie.get("name")
            value = cookie.get("value")
            if name is None or value is None:
                continue
            pairs.append(f"{name}={value}")
        return "; ".join(pairs) if pairs else None

    @staticmethod
    def _load_selector_file(path: str | None) -> list[str]:
        if not path:
            return []
        selector_path = Path(path)
        if not selector_path.exists():
            return []
        selectors: list[str] = []
        for raw_line in selector_path.read_text(encoding="utf-8").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#"):
                continue
            selectors.append(line)
        return selectors

    async def _dump_selector_inventory(self, page: Page) -> None:
        interesting_selectors = [
            "video",
            "button",
            "textarea",
            'div[class*="Successfulvideogenerationthumbnail"]',
            'div[class*="VideoGenerationThumbnail"]',
            'div[class*="VideogenfooterInspirationGallery"]',
        ]
        for selector in interesting_selectors:
            loc = page.locator(selector)
            try:
                count = await loc.count()
            except Exception:
                continue
            if count == 0:
                continue
            for idx in range(min(count, 12)):
                item = loc.nth(idx)
                try:
                    tag = await item.evaluate("(el) => el.tagName.toLowerCase()")
                    el_id = await item.get_attribute("id")
                    cls = await item.get_attribute("class")
                    aria = await item.get_attribute("aria-label")
                    src = ""
                    try:
                        src = await item.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                    except Exception:
                        src = ""
                    text = ""
                    try:
                        text = (await item.inner_text())[:160]
                    except Exception:
                        text = ""
                    log.info(
                        "Selector inventory selector=%s idx=%d tag=%s id=%s class=%s aria=%s src=%s text=%s",
                        selector,
                        idx,
                        tag,
                        el_id or "",
                        cls or "",
                        aria or "",
                        src[:180] if src else "",
                        text.replace("\n", " ") if text else "",
                    )
                except Exception as exc:
                    log.debug("Selector inventory failed selector=%s idx=%d err=%s", selector, idx, exc)


async def generate_video_ai_v2(video_id: str, prompt: str, account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        result = await engine.generate_video(video_id, prompt)
        return str(result) if result else None


async def generate_avatar_v1(video_id: str, script: str, avatar_id: str = "James", account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        result = await engine.generate_avatar(video_id, script, avatar_id)
        return str(result) if result else None


async def list_projects(account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        return await engine.list_projects()
