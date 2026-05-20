import asyncio
import logging
import time
import subprocess
import random
import os
import re
import urllib.parse
import urllib.request
import urllib.error
from pathlib import Path
from playwright.async_api import async_playwright, Browser, BrowserContext, Page
from config import get_session_path, DOWNLOAD_DIR, GOOGLE_VIDS_BASE_URL
from drive_client import list_exported_files, download_file

log = logging.getLogger("AutomationEngine")

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
        self.browser = await self.playwright.chromium.launch(
            headless=self.headless,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--start-maximized"
            ]
        )
        self.context = await self.browser.new_context(
            storage_state=str(self.session_path),
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
            viewport={"width": 1920, "height": 1080}
        )
        log.info("Browser context ready for account=%s", self.account or "default")
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.context:
            await self.context.close()
        if self.browser:
            await self.browser.close()
        await self.playwright.stop()

class GoogleVidsAutomation(BaseAutomation):
    """Engine per l'automazione di Google Vids."""

    PROMPT_TEXTAREA_SELECTORS = [
        'textarea[aria-label^="Descrivi il tuo video di 8 secondi."]',
        'textarea.javascriptMaterialdesignGm3WizTextFieldOutlined-text-field__input',
        'textarea#c1',
        'textarea',
        '[contenteditable="true"]',
    ]

    GENERATE_BUTTON_SELECTORS = [
        'xpath=/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[2]/div[5]/span/div[1]/button',
        'button:has-text("Genera")',
        'button:has-text("Generate")',
        'button:has-text("Crea")',
        'button.videoGenCreationViewGenerateButton',
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
                titles.append(line)
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

    async def generate_video(self, video_id: str, prompt: str, zoom_centered: bool = True) -> Path:
        dest_dir = DOWNLOAD_DIR / "videos"
        dest_dir.mkdir(parents=True, exist_ok=True)
        
        page = await self._get_page(video_id)
        try:
            generation_timeout_ms = 900000
            poll_interval_ms = 5000
            log.info("Starting Vids generation for video_id=%s (timeout=%sms)", video_id, generation_timeout_ms)
            await human_delay(1500, 4000)
            try:
                await page.locator('#content-library-rail-video-generation-element').click(timeout=10000)
                log.info("Opened Veo generation rail for video_id=%s", video_id)
            except Exception:
                await page.get_by_text("Veo", exact=False).click(timeout=10000)
                log.info("Opened Veo generation rail via text fallback for video_id=%s", video_id)
            await human_delay(1000, 2500)
            
            input_candidates = [
                *self.PROMPT_TEXTAREA_SELECTORS,
            ]
            input_loc = None
            for selector in input_candidates:
                loc = page.locator(selector)
                if await loc.count():
                    input_loc = loc.first
                    break
            if input_loc is None:
                raise RuntimeError("Prompt input not found in Google Vids UI.")
            
            # Digitazione umana
            await input_loc.click()
            await input_loc.type(prompt, delay=random.randint(50, 150))
            await human_delay(800, 2000)
            
            log.info("Submitting generation prompt for video_id=%s prompt=%s", video_id, prompt[:80])
            clicked = False
            for selector in self.GENERATE_BUTTON_SELECTORS:
                loc = page.locator(selector)
                if await loc.count():
                    await loc.first.click(timeout=10000)
                    clicked = True
                    log.info("Clicked generate button via selector=%s for video_id=%s", selector, video_id)
                    break
            if not clicked:
                raise RuntimeError("Generate button not found in Google Vids UI.")

            started_at = time.time()
            video_src = None
            selected_idx = -1
            debug_dump_done = False
            while (time.time() - started_at) * 1000 < generation_timeout_ms:
                await page.wait_for_timeout(poll_interval_ms)
                if os.getenv("GOOGLE_VIDS_DEBUG_DUMP_SELECTORS") == "1" and not debug_dump_done:
                    await self._dump_selector_inventory(page)
                    debug_dump_done = True
                container_loc = None
                for selector in self.PREVIEW_CONTAINER_SELECTORS:
                    loc = page.locator(selector)
                    if await loc.count():
                        try:
                            video_children = await loc.first.locator("video").count()
                        except Exception:
                            video_children = 0
                        if video_children == 0:
                            log.info("Preview container candidate via selector=%s had no video children yet", selector)
                            continue
                        container_loc = loc.first
                        log.info("Found preview container via selector=%s video_children=%d", selector, video_children)
                        break

                candidates = container_loc.locator("video") if container_loc is not None else page.locator("video")
                count = await candidates.count()
                src_log = []
                for idx in range(count):
                    loc = candidates.nth(idx)
                    try:
                        src = await loc.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                        aria = await loc.get_attribute("aria-label")
                        cls = await loc.get_attribute("class")
                        if src:
                            src_log.append(f"{idx}:{src[:140]}")
                        if src and "inspirationgallery" not in src:
                            video_src = src
                            selected_idx = idx
                            log.info(
                                "Selected generated preview candidate idx=%d src=%s aria=%s class=%s",
                                idx,
                                src[:180],
                                aria or "",
                                cls or "",
                            )
                            break
                    except Exception as inner_err:
                        log.debug("preview poll failed idx=%d err=%s", idx, inner_err)
                log.info(
                    "Polling generated video preview for video_id=%s elapsed=%.1fs candidates=%d sources=%s",
                    video_id,
                    time.time() - started_at,
                    count,
                    src_log[:3],
                )
                if not video_src:
                    debug_selector_file = os.getenv("GOOGLE_VIDS_DEBUG_SELECTORS_FILE")
                    extra_selectors = self._load_selector_file(debug_selector_file)
                    if extra_selectors:
                        log.info(
                            "Loaded %d extra preview selectors from %s",
                            len(extra_selectors),
                            debug_selector_file,
                        )

                    for selector in [*self.PREVIEW_VIDEO_SELECTORS, *extra_selectors]:
                        loc = page.locator(selector)
                        if await loc.count() == 0:
                            continue
                        for idx in range(await loc.count()):
                            direct_loc = loc.nth(idx)
                            try:
                                src = await direct_loc.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                                aria = await direct_loc.get_attribute("aria-label")
                                cls = await direct_loc.get_attribute("class")
                                if src:
                                    log.info(
                                        "Direct preview selector candidate selector=%s idx=%d src=%s aria=%s class=%s",
                                        selector,
                                        idx,
                                        src[:180],
                                        aria or "",
                                        cls or "",
                                    )
                                if src and "inspirationgallery" not in src:
                                    video_src = src
                                    selected_idx = idx
                                    break
                            except Exception as inner_err:
                                log.debug("direct preview selector failed selector=%s idx=%d err=%s", selector, idx, inner_err)
                        if video_src:
                            break
                if video_src:
                    break

            if not video_src:
                body_text = await page.locator("body").inner_text()
                log.error(
                    "Generated video preview not found for video_id=%s page_url=%s body_snippet=%s",
                    video_id,
                    page.url,
                    body_text[:1200],
                )
                raise RuntimeError("Generated video preview not found in Google Vids UI.")

            # Stronger direct lookup using the exact preview video selector before download.
            preview_video = None
            if container_loc is not None:
                for selector in self.PREVIEW_VIDEO_SELECTORS:
                    loc = container_loc.locator(selector)
                    if await loc.count():
                        preview_video = loc.first
                        break
            if preview_video is not None:
                try:
                    direct_src = await preview_video.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                    if direct_src:
                        video_src = direct_src
                        log.info("Using direct preview video src for video_id=%s src=%s", video_id, video_src[:180])
                except Exception:
                    pass

            if not video_src:
                direct_candidates = [
                    'video.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailVideoGenerationThumbnail',
                    'video.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailInsertableVideoGenerationThumbnail',
                    'div.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailVideoGenerationThumbnailContainer video',
                ]
                for selector in direct_candidates:
                    loc = page.locator(selector)
                    if await loc.count():
                        try:
                            direct_src = await loc.first.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                            if direct_src:
                                video_src = direct_src
                                log.info("Using direct selector=%s src=%s", selector, video_src[:180])
                                break
                        except Exception:
                            continue

            raw_path = dest_dir / f"raw_{int(time.time())}.mp4"
            log.info("Downloading generated video directly from src for video_id=%s src=%s", video_id, video_src[:180])
            cookie_header = await self._build_cookie_header(video_src)
            downloaded = await self._download_direct_url(video_src, raw_path, referer=page.url, cookie_header=cookie_header)
            if not downloaded:
                async with page.expect_download(timeout=300000) as download_info:
                    log.info("Fallback download capture for video_id=%s idx=%d src=%s", video_id, selected_idx, video_src[:180])
                    await page.evaluate(f"window.location.href = '{video_src}'")

                download = await download_info.value
                log.info("Fallback download captured for video_id=%s -> temp file", video_id)
                await download.save_as(str(raw_path))
                log.info("Saved fallback download to %s", raw_path)
            else:
                log.info("Saved direct download to %s", raw_path)
            
            final_path = dest_dir / f"VIDEO_YT_{int(time.time())}.mp4"
            vf_filter = "crop=in_w*0.9:in_h*0.9:(in_w-out_w)/2:(in_h-out_h)/2,scale=1920:1080" if zoom_centered else "scale=1920:1080"
            
            cmd = [
                'ffmpeg', '-y', '-i', str(raw_path),
                '-vf', vf_filter,
                '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-preset', 'fast', '-crf', '20',
                '-c:a', 'aac', '-b:a', '192k', str(final_path)
            ]
            
            log.info("Running ffmpeg on %s -> %s", raw_path, final_path)
            proc = subprocess.run(cmd, capture_output=True, text=True)
            if proc.returncode == 0:
                raw_path.unlink()
                log.info("Final video ready at %s", final_path)
                return final_path
            log.error("ffmpeg failed rc=%s stderr=%s", proc.returncode, proc.stderr[-2000:])
            return raw_path
        except Exception as e:
            log.error("Errore generazione video for video_id=%s: %s", video_id, e, exc_info=True)
            return None
        finally:
            await page.close()

STYLE_MAP = {
    "realistic": "extremely detailed, realistic, 8k, photorealistic, cinematic lighting",
    "cartoon": "cartoon style, 2d, colorful, high quality animation, vibrant",
    "medieval": "medieval style, fantasy, historical, detailed oil painting, epic atmosphere",
    "cyberpunk": "cyberpunk aesthetic, neon lights, futuristic, dark atmosphere, high tech",
    "watercolor": "watercolor painting style, soft colors, artistic, fluid textures",
    "3d-render": "3d render, octane render, unreal engine 5 style, volumetric lighting, masterpiece",
    "sketch": "hand-drawn sketch, pencil drawing, monochrome, detailed lines, artistic",
    "cinematic": "cinematic lighting, movie shot, 35mm lens, highly detailed, dramatic",
}

class ImageFXFlowAutomation(BaseAutomation):
    """Engine per l'automazione di Google Labs ImageFX Flow."""

    PROMPT_SELECTORS = [
        '[contenteditable="true"]',
        'textarea',
    ]

    GENERATE_BUTTON_SELECTORS = [
        'button:has-text("arrow_forward")',
        'button:has-text("Crea")',
        'button:has-text("Genera")',
        'button:has-text("Generate")',
    ]

    GENERATED_IMAGE_SELECTORS = [
        'img[alt="Immagine generata"]',
        'img[src*="/fx/api/trpc/media.getMediaUrlRedirect"]',
    ]

    async def generate_images(self, prompt: str, project_id: str = None, style: str = None) -> list[Path]:
        dest_dir = DOWNLOAD_DIR / "images" / (project_id or "general")
        dest_dir.mkdir(parents=True, exist_ok=True)
        
        # Applichiamo lo stile se presente
        if style and style.lower() in STYLE_MAP:
            full_prompt = f"{prompt}, {STYLE_MAP[style.lower()]}"
            log.info(f"Stile '{style}' applicato al prompt.")
        else:
            full_prompt = prompt
        
        if project_id:
            url = f"https://labs.google/fx/it/tools/flow/project/{project_id}"
        else:
            url = "https://labs.google/fx/it/tools/flow"
            
        page = await self.context.new_page()
        await human_delay(1000, 3000)
        log.info(f"Navigazione verso: {url}")
        await page.goto(url, wait_until="networkidle")
        await asyncio.sleep(5)
        await human_scroll(page)

        new_saved_paths = []
        captured_response_urls: set[str] = set()
        can_capture = False

        async def handle_response(response):
            nonlocal new_saved_paths
            if not can_capture:
                return

            content_type = response.headers.get("content-type", "")
            if "image/" not in content_type:
                return

            response_url = response.url
            if "flow-content.google/image/" not in response_url and "media.getMediaUrlRedirect" not in response_url:
                return
            if response_url in captured_response_urls:
                return

            try:
                body = await response.body()
                if not body:
                    return

                ext = ".jpg"
                if "png" in content_type:
                    ext = ".png"

                timestamp = int(time.time())
                path = dest_dir / f"FLOW_IMG_{timestamp}_{len(new_saved_paths)}{ext}"
                path.write_bytes(body)
                captured_response_urls.add(response_url)
                new_saved_paths.append(path)
                log.info("Nuova immagine Flow catturata: %s url=%s", path.name, response_url)
            except Exception as e:
                log.warning("Failed to capture Flow image response url=%s err=%s", response_url, e)

        page.on("response", handle_response)

        try:
            prompt_locator = None
            for selector in self.PROMPT_SELECTORS:
                loc = page.locator(selector)
                if await loc.count():
                    prompt_locator = loc.first
                    log.info("Found Flow prompt selector=%s", selector)
                    break
            if prompt_locator is None:
                raise RuntimeError("Flow prompt field not found.")

            await human_delay(2000, 5000)
            tag_name = await prompt_locator.evaluate("(el) => el.tagName")
            if tag_name == "TEXTAREA":
                await prompt_locator.fill(full_prompt)
            else:
                await prompt_locator.click()
                await human_delay(500, 1500)
                await page.keyboard.type(full_prompt, delay=random.randint(40, 180))
            await human_delay(1000, 3000)

            generate_locator = None
            for selector in self.GENERATE_BUTTON_SELECTORS:
                loc = page.locator(selector)
                if await loc.count():
                    generate_locator = loc.last
                    log.info("Found Flow generate selector=%s count=%d", selector, await loc.count())
                    break
            if generate_locator is None:
                raise RuntimeError("Flow generate button not found.")

            existing_images = set()
            for selector in self.GENERATED_IMAGE_SELECTORS:
                loc = page.locator(selector)
                for idx in range(await loc.count()):
                    src = await loc.nth(idx).get_attribute("src")
                    if src:
                        existing_images.add(src)

            can_capture = True
            await generate_locator.click()

            wait_time = 90
            deadline = time.monotonic() + wait_time
            idle_rounds = 0
            seen_any_image = False
            log.info("In attesa della generazione Flow (%ss)...", wait_time)

            while time.monotonic() < deadline:
                current_new: list[str] = []
                for selector in self.GENERATED_IMAGE_SELECTORS:
                    loc = page.locator(selector)
                    count = await loc.count()
                    for idx in range(count):
                        src = await loc.nth(idx).get_attribute("src")
                        if src and src not in existing_images and src not in current_new:
                            current_new.append(src)

                if current_new:
                    seen_any_image = True
                    idle_rounds = 0
                    for src in current_new:
                        existing_images.add(src)
                        absolute_src = urllib.parse.urljoin("https://labs.google", src)
                        file_name = urllib.parse.unquote(urllib.parse.parse_qs(urllib.parse.urlparse(absolute_src).query).get("name", [f"flow_{int(time.time())}"])[0])
                        file_name = Path(file_name).stem or f"FLOW_IMG_{int(time.time())}"
                        path = dest_dir / f"{file_name}.jpg"
                        if path.exists():
                            continue
                        cookie_pairs = [f"{cookie['name']}={cookie['value']}" for cookie in await self.context.cookies([absolute_src])]
                        cookie_header = "; ".join(cookie_pairs) if cookie_pairs else None
                        if self._download_direct_url(absolute_src, path, referer=page.url, cookie_header=cookie_header):
                            new_saved_paths.append(path)
                            log.info("Nuova immagine Flow salvata: %s", path)
                        else:
                            log.warning("Download immagine Flow fallito: %s", absolute_src)
                else:
                    if seen_any_image:
                        idle_rounds += 1
                        if idle_rounds >= 3:
                            break

                await asyncio.sleep(5)

            return new_saved_paths
        except Exception as e:
            log.error(f"Errore generazione ImageFX Flow: {e}")
            return []
        finally:
            await page.close()

    @staticmethod
    def _download_direct_url(
        image_url: str,
        dest_path: Path,
        referer: str | None = None,
        cookie_header: str | None = None,
    ) -> bool:
        headers = {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
            "Accept": "image/*,*/*;q=0.8",
        }
        if referer:
            headers["Referer"] = referer
        if cookie_header:
            headers["Cookie"] = cookie_header

        request = urllib.request.Request(image_url, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                dest_path.write_bytes(response.read())
            return True
        except Exception as e:
            log.warning("Failed to download Flow image url=%s err=%s", image_url, e)
            return False

# Helper per mantenere compatibilità e pulizia
async def list_projects(account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        return await engine.list_projects()

async def sync_project(video_id: str, file_type: str = "all", account: str = None, headless: bool = True):
    exported_files = list_exported_files(video_id)
    if not exported_files:
        log.warning("No exported Drive files found for project %s", video_id)
        return []

    allowed_file_types = {"video", "image", "all"}
    if file_type not in allowed_file_types:
        raise ValueError(f"Unsupported file_type: {file_type}")

    paths: list[Path] = []
    for file_meta in exported_files:
        mime_type = file_meta.get("mimeType", "")
        if file_type == "video" and mime_type != "video/mp4":
            continue
        if file_type == "image" and not mime_type.startswith("image/"):
            continue
        if file_type == "all" and mime_type != "video/mp4" and not mime_type.startswith("image/"):
            continue

        sub_type = "video" if mime_type == "video/mp4" else "image"
        downloaded = await download_file(file_meta["id"], file_meta["name"], sub_type)
        paths.append(downloaded)
        log.info("Downloaded exported file: %s", downloaded)

    return paths

async def generate_video_ai_v2(video_id: str, prompt: str, account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        result = await engine.generate_video(video_id, prompt)
        return str(result) if result else None

async def generate_flow_images(prompt: str, project_id: str = None, style: str = None, account: str = None, headless: bool = True):
    async with ImageFXFlowAutomation(account=account, headless=headless) as engine:
        results = await engine.generate_images(prompt, project_id, style)
        return [str(p) for p in results]
