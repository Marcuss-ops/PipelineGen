import asyncio
import os
import random
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from playwright.async_api import Page

from drive_client import download_file, list_exported_files

from .base import (
    DOWNLOAD_DIR,
    GOOGLE_VIDS_BASE_URL,
    BaseAutomation,
    _append_selector_report,
    human_delay,
    human_scroll,
    log,
)

class GoogleVidsAutomation(BaseAutomation):
    """Engine per l'automazione di Google Vids."""

    PROMPT_TEXTAREA_SELECTORS = [
        'textarea[aria-label^="Descrivi il tuo video di 8 secondi."]',
        'textarea.javascriptMaterialdesignGm3WizTextFieldOutlined-text-field__input',
        'textarea#c1',
        'textarea',
        '[contenteditable=\"true\"]',
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
        from storage import get_project_id, save_project_id
        
        dest_dir = DOWNLOAD_DIR / "videos"
        dest_dir.mkdir(parents=True, exist_ok=True)

        # Se video_id è nullo o "new", prova a caricare dalla cache
        if not video_id or video_id == "new":
            video_id = get_project_id("vids")
            if video_id:
                log.info("Reusing cached Vids project ID: %s", video_id)
            else:
                video_id = "new"

        selector_report = {
            "kind": "vids",
            "video_id": video_id,
            "prompt_preview": prompt[:120],
            "attempts": [],
            "generated_at": int(time.time()),
        }
        
        if video_id == "new" or not video_id:
            log.info("Creating new Vids project for Video Generation")
            page = await self._goto_home()
            try:
                await page.click('div[aria-label="Inizia un nuovo video"]', timeout=5000)
                await page.wait_for_timeout(3000)
                # Wait for redirect to /videos/d/ID/edit
                await page.wait_for_url(re.compile(r"/videos/d/"), timeout=15000)
                video_id = self._extract_vid_id(page.url)
                if video_id:
                    log.info("New Vids project created: %s, saving to cache.", video_id)
                    save_project_id("vids", video_id)
            except Exception as e:
                log.error("Failed to create new Vids project: %s", e)
                # Try fallback to direct open if redirect happened but wait failed
                video_id = self._extract_vid_id(page.url)
                if not video_id:
                    await page.close()
                    raise
        else:
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
                attempt_started = time.time()
                loc = page.locator(selector)
                matched = await loc.count() > 0
                selector_report["attempts"].append({
                    "stage": "prompt",
                    "selector": selector,
                    "matched": matched,
                    "elapsed_ms": int((time.time() - attempt_started) * 1000),
                })
                if matched:
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
                attempt_started = time.time()
                loc = page.locator(selector)
                matched = await loc.count() > 0
                selector_report["attempts"].append({
                    "stage": "generate_button",
                    "selector": selector,
                    "matched": matched,
                    "elapsed_ms": int((time.time() - attempt_started) * 1000),
                })
                if matched:
                    await loc.first.click(timeout=10000)
                    clicked = True
                    selector_report["attempts"].append({
                        "stage": "generate_button_selected",
                        "selector": selector,
                        "matched": True,
                        "elapsed_ms": 0,
                    })
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
                    attempt_started = time.time()
                    loc = page.locator(selector)
                    matched = await loc.count() > 0
                    selector_report["attempts"].append({
                        "stage": "preview_container",
                        "selector": selector,
                        "matched": matched,
                        "elapsed_ms": int((time.time() - attempt_started) * 1000),
                    })
                    if matched:
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
                        attempt_started = time.time()
                        loc = page.locator(selector)
                        matched = await loc.count() > 0
                        selector_report["attempts"].append({
                            "stage": "preview_video",
                            "selector": selector,
                            "matched": matched,
                            "elapsed_ms": int((time.time() - attempt_started) * 1000),
                        })
                        if not matched:
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
            selector_report["result"] = "failed"
            selector_report["error"] = str(e)
            return None
        finally:
            selector_report.setdefault("result", "success")
            _append_selector_report(selector_report)
            await page.close()


    async def generate_avatar(self, video_id: str, script: str, avatar_id: str = "James") -> Path | None:
        """Generates an AI Talking Head (Avatar) in Google Vids using finalized selectors."""
        from storage import get_project_id, save_project_id, get_structured_path, save_media_asset, save_generation_metadata

        dest_folder = get_structured_path(media_type="avatar", style="ai", sub_style=avatar_id)
        
        if not video_id or video_id == "new":
            video_id = get_project_id("vids")
            video_id = video_id if video_id else "new"

        page = await self._goto_home()
        try:
            if video_id == "new" or not video_id:
                log.info("Creating new Vids project for Avatar")
                await page.click('[aria-label="Crea nuovo video"]', timeout=15000)
                await page.wait_for_url(re.compile(r"/videos/d/"), timeout=20000)
                video_id = self._extract_vid_id(page.url)
                if video_id:
                    save_project_id("vids", video_id)
            else:
                log.info("Opening existing Vids project: %s", video_id)
                await page.goto(f"https://docs.google.com/videos/d/{video_id}/edit", wait_until="domcontentloaded")

            log.info("Waiting for editor to stabilize (30s)...")
            await asyncio.sleep(30)

            # 1. Clicca tasto Avatar (ID Fisso trovato nel dump: BTN 79)
            avatar_btn = page.locator('#content-library-rail-avatars-element').first
            
            if not avatar_btn or await avatar_btn.count() == 0:
                raise RuntimeError("Avatar button not found")
                
            log.info("Clicking Avatar button...")
            await avatar_btn.click(timeout=15000)
            await asyncio.sleep(5)

            # 2. Seleziona l'avatar specifico
            # Cambia è BTN 105
            change_btn = page.locator('div[role="button"]:has-text("Cambia")').first
            if await change_btn.count() > 0:
                log.info("Clicking 'Cambia' avatar button...")
                await change_btn.click()
                await asyncio.sleep(5)
                # Selezione avatar per nome
                avatar_choice = page.locator(f'div[role="radio"][aria-label*="{avatar_id}"]').first
                if await avatar_choice.count() > 0:
                    log.info(f"Selecting avatar: {avatar_id}")
                    await avatar_choice.click()
                    await asyncio.sleep(2)
                    # Pulsante Seleziona è BTN 125
                    select_btn = page.locator('div[role="button"]:has-text("Seleziona")').first
                    await select_btn.click()
                    await asyncio.sleep(3)

            # 3. Inserisci lo script
            # In Vids, l'input è spesso nel body della sidebar, proviamo ad intercettarlo tramite testo
            # O usiamo il selettore generico textbox
            log.info("Filling script area...")
            textarea = page.locator('[aria-label="Script video"] [role="textbox"]').first
            
            # Se fallisce, usiamo un fallback generico
            if not textarea or await textarea.count() == 0:
                textarea = page.locator('.appsFlixScriptsSidebarWorkspace [role="textbox"]').first

            if not textarea or await textarea.count() == 0:
                raise RuntimeError("Script textarea not found")

            await textarea.click()
            await page.keyboard.press("Control+A")
            await page.keyboard.press("Backspace")
            await page.keyboard.type(script, delay=40)
            await asyncio.sleep(3)

            # 4. Genera Preview
            preview_btn = page.locator('div[role="button"]:has-text("Anteprima")').first
            
            if await preview_btn.count() > 0:
                log.info("Clicking Anteprima button...")
                await preview_btn.click()
                log.info("Waiting 30 seconds for preview...")
                await asyncio.sleep(30)

            # 5. Genera finale
            # Genera è BTN 108
            generate_btn = page.locator('div[role="button"]:has-text("Genera")').first
            
            if not generate_btn or await generate_btn.count() == 0:
                raise RuntimeError("Generate button not found")

            log.info("Clicking Generate (Lip Sync) button...")
            await generate_btn.click()
            
            log.info("Waiting 130 seconds for lip-sync generation...")
            await asyncio.sleep(130)

            # 6. Registrazione finale
            metadata = {"script": script, "avatar_id": avatar_id, "video_id": video_id, "timestamp": int(time.time())}
            save_generation_metadata(dest_folder, metadata)

            paths = await sync_project(video_id, file_type="video", account=self.account, headless=self.headless)
            if paths:
                final_path = paths[0]
                save_media_asset(final_path, "GOOGLE_VIDS_AVATAR", final_path.name, "video", "ai_avatar", avatar_id, script, video_id, metadata)
                return final_path
            return None

        except Exception as e:
            log.error("Errore generazione avatar: %s", e, exc_info=True)
            return None
        finally:
            await page.close()


async def sync_project(video_id: str, file_type: str = "all", account: str = None, headless: bool = True):
    from drive_client import download_file, list_exported_files
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


async def generate_avatar_v1(video_id: str, script: str, avatar_id: str = "James", account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        result = await engine.generate_avatar(video_id, script, avatar_id)
        return str(result) if result else None
