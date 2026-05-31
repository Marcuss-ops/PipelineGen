import asyncio
import random
import re
import shutil
import time
from pathlib import Path

from .base import (
    BaseAutomation,
    _append_selector_report,
    human_delay,
    log,
)


class GoogleVidsImagesMixin:
    """Mixin class for Google Vids image generation using the built-in image synthesis tool."""

    # ── Selectors per la generazione immagini ──────────────────────────────────
    IMMAGINI_TAB_SELECTOR = '#content-library-rail-image-synthesis-element'

    ASPECT_RATIO_TRIGGER_SELECTOR = (
        "div[aria-label*='Proporzioni' i], div[aria-label*='Aspect ratio' i], "
        'div.appsDocsAiGenerativeaiImageGenerateImagesInputAspectRatioPicker'
    )


    PROMPT_TEXTAREA_SELECTOR = (
        'textarea.docs-prompt-view-input.docs-image-synthesis-search-bar-input.goog-textarea'
    )

    GENERATE_BUTTON_SELECTOR = (
        'button.image-synthesis-creation-button.image-synthesis-begin-create'
    )

    # Fallback selectors
    PROMPT_TEXTAREA_FALLBACKS = [
        "textarea[placeholder*='Descrivi' i]",
        "textarea[aria-label*='Descrivi' i]",
        ".docs-prompt-view-input",
        "textarea.docs-image-synthesis-search-bar-input",
        'textarea.docs-prompt-view-input.docs-image-synthesis-search-bar-input',
        'textarea.docs-prompt-view-input',
        'textarea.docs-image-synthesis-search-bar-input',
        'div.appsDocsUiSidebarEl.genAiImageSidebarEl textarea',
        'div.appsDocsUiSidebarEl.genAiImageSidebarEl [contenteditable="true"]',
        '[aria-label*="Descrivi la tua idea" i]',
        '[placeholder*="Descrivi la tua idea" i]',
        'textarea[placeholder*="Describe your idea"]',
        'textarea[placeholder*="Descrivi la tua idea"]',
        '.docs-prompt-view-input-container textarea',
        'xpath=/html/body/aside/div/div[4]/div[1]/div[2]/div[1]/div/textarea',
        'textarea',
    ]

    GENERATE_BUTTON_FALLBACKS = [
        'button.image-synthesis-creation-button.image-synthesis-begin-create',
        'button.image-synthesis-begin-create',
        'button[aria-label="Create"]',
        'button[data-tooltip="Create"]',
        'button:has-text("Create")',
        'button[aria-label="Crea"]',
        'button[data-tooltip="Crea"]',
        'button:has-text("Crea")',
        'button:has-text("Genera")',
    ]

    # Progetto esistente da usare sempre (evita il popup "Ciao Massimo")
    SHARED_VIDS_PROJECT_ID = "1Kn_99mlEjC8kn4_dLBgfeoKZw7ohMz-TjBAvx3szNoQ"
    SHARED_VIDS_PROJECT_SCENE = "id.g57c7d542_0_0"

    # ── Helper per click umano (hover + click) ─────────────────────────────
    async def _human_click(self, page, locator, timeout: int = 10000) -> bool:
        """
        Sostituisce click(force=True) manuale con locator.click() per maggiore affidabilita'.
        Include scrolling, rimozione modali e fallback coordinate.
        """
        try:
            # 1. Ensure element is in view
            await locator.scroll_into_view_if_needed(timeout=timeout)
            await asyncio.sleep(0.5)
            
            # 2. Try standard Playwright click
            try:
                await locator.click(delay=random.randint(50, 150), timeout=timeout)
                log.info("Standard click on locator successful")
                return True
            except Exception as e:
                log.warning("Standard click failed: %s, trying force=True", e)
                
            # 3. Try forced click
            try:
                await locator.click(force=True, delay=random.randint(50, 150), timeout=timeout)
                log.info("Force click on locator successful")
                return True
            except Exception as e2:
                log.warning("Force click failed: %s, trying coordinate-based click", e2)

            # 4. Try coordinate-based mouse click as fallback
            box = await locator.bounding_box()
            if box:
                center_x = box["x"] + box["width"] / 2
                center_y = box["y"] + box["height"] / 2
                await page.mouse.click(center_x, center_y)
                log.info("Coordinate-based click at (%.0f, %.0f) successful", center_x, center_y)
                return True
            
            # 5. Last resort: JS dispatchEvent
            log.warning("Coordinate click failed (no box), trying dispatchEvent")
            await locator.dispatch_event('click')
            return True
            
        except Exception as e3:
            log.error("All click methods failed for locator: %s", e3)
            return False

    # ── Selectors per il polling dell'immagine generata ───────────────────
    # ATTENZIONE: usare selettori SPECIFICI, NON 'img' generico (prende icone UI)
    GENERATED_IMAGE_SELECTORS = [
        'img.appsDocsAiGenerativeaiImageUiImageGenerationThumbnailImage',
        'img[class*="ImageGenerationThumbnail"]',
        'img[class*="image-generation"]',
        'img[class*="generated"]',
        'div[class*="ImageGenerationThumbnail"] img',
        'div[class*="image-generation-result"] img',
    ]

    # Selector per rilevare stato "generazione completata" nella UI
    GENERATION_DONE_SELECTORS = [
        '[class*="SuccessfulImageGeneration"]',
        '[class*="image-generation-complete"]',
        '[class*="ImageGenerationThumbnail"]',
        '[data-view-id*="image-generation-success"]',
    ]

    async def _set_aspect_ratio_16_9(self, page):
        """Clicks the aspect ratio picker and selects 'Orizzontale 16:9'."""
        log.info("Setting aspect ratio to 16:9...")
        await page.wait_for_timeout(7000)

        trigger_selectors = [
            self.ASPECT_RATIO_TRIGGER_SELECTOR,
            '[role="listbox"][aria-label*="Proporzioni" i]',
            '[role="listbox"][data-tooltip*="Proporzioni" i]',
            '[aria-label*="Proporzioni" i]',
            '[data-tooltip*="Proporzioni" i]',
        ]

        picker = None
        for sel in trigger_selectors:
            try:
                loc = page.locator(sel).first
                await loc.wait_for(state="visible", timeout=30000)
                picker = loc
                log.info("Aspect ratio trigger found via: %s", sel)
                break
            except Exception:
                continue

        if picker is None:
            log.warning("Aspect ratio picker trigger not found, trying fallback")
            return False

        try:
            await picker.scroll_into_view_if_needed(timeout=5000)
            await self._human_click(page, picker, timeout=5000)
            await human_delay(500, 900)
            log.info("Clicked aspect ratio picker trigger")
        except Exception as e:
            log.warning("Failed to click aspect ratio picker: %s", e)
            return False

        # Ora seleziona la voce 16:9 nel dropdown, usando il testo visibile
        # che sappiamo essere presente quando il pannello è aperto.
        option_selectors = [
            'div[role="option"][aria-label*="16:9"]',
            '[role="option"][aria-label*="16:9"]',
            'div[role="option"]:has-text("16:9")',
            '[role="option"]:has-text("16:9")',
            'div[role="option"]:has-text("Orizzontale")',
            '[role="option"]:has-text("Orizzontale")',
            'div[role="option"]:has-text("Landscape")',
            '[role="option"]:has-text("Landscape")',
            'div[aria-label*="Landscape 16:9"]',
            'div[aria-label*="Orizzontale"]',
            'div[aria-label*="Landscape"]',
        ]
        for sel in option_selectors:
            try:
                opt = page.locator(sel).first
                if await opt.count() > 0:
                    await self._human_click(page, opt, timeout=5000)
                    log.info("Selected 16:9 aspect ratio via: %s", sel)
                    await human_delay(300, 700)
                    return True
            except Exception:
                continue

        try:
            opt = page.get_by_role("option", name=re.compile(r"16:9|Orizzontale|Landscape", re.I)).first
            if await opt.count() > 0:
                await self._human_click(page, opt, timeout=5000)
                log.info("Selected 16:9 aspect ratio via role-based lookup")
                await human_delay(300, 700)
                return True
        except Exception:
            pass

        log.warning("Could not select 16:9 aspect ratio from dropdown")
        return False

    async def _fill_image_prompt(self, page, prompt: str) -> bool:
        """Fills the prompt textarea with the given prompt.
        """
        log.info("Filling image prompt...")

        ta = None
        selector_used = None

        # Prova prima con il selettore esatto e attendi che diventi visibile.
        selectors = [self.PROMPT_TEXTAREA_SELECTOR, *self.PROMPT_TEXTAREA_FALLBACKS]
        for sel in selectors:
            try:
                ta = page.locator(sel).first
                await ta.wait_for(state="visible", timeout=15000)
                selector_used = "exact" if sel == self.PROMPT_TEXTAREA_SELECTOR else f"fallback: {sel}"
                break
            except Exception:
                ta = None
                continue

        if ta is None:
            log.error("Could not find prompt textarea")
            return False

        # Google Vids enables the submit button only after real keyboard input.
        # Use actual typing events, but keep the sequence simple and deterministic.
        await ta.click(timeout=5000)
        await human_delay(200, 500)
        await ta.focus()
        await page.keyboard.type(prompt, delay=random.randint(15, 35))
        await page.wait_for_timeout(800)
        log.info("Prompt filled via %s", selector_used)

        return True

    async def _apply_anti_bot_fingerprint(self, page):
        """Apply anti-bot fingerprint: add extra chars then remove them via Backspace
        to simulate human typing correction behavior.
        """
        fingerprint_chars = random.choice(["q", "w", "e", "a", "s", "d", "z", "x"])
        fingerprint_extra = " " + fingerprint_chars * random.randint(1, 3)
        
        await human_delay(150, 400)
        await page.keyboard.type(fingerprint_extra, delay=random.randint(80, 180))
        await human_delay(100, 250)
        # Rimuove i caratteri extra (Backspace = 1 char per key)
        for _ in range(len(fingerprint_extra)):
            await page.keyboard.press("Backspace")
            await asyncio.sleep(random.uniform(0.05, 0.15))
        await human_delay(100, 300)
        log.info("Anti-bot fingerprint applied: added '%s' and removed it", fingerprint_extra.strip())

    async def _click_generate_image(self, page) -> bool:
        """Clicks the generate (Crea) button for image synthesis."""
        log.info("Clicking generate image button...")

        # Prova prima con il selettore esatto
        try:
            btn = page.locator(self.GENERATE_BUTTON_SELECTOR).first
            if await btn.count() > 0:
                await self._human_click(page, btn, timeout=10000)
                log.info("Generate button clicked via exact selector")
                return True
        except Exception as e:
            log.warning("Exact generate button selector failed: %s", e)

        # Fallback
        for sel in self.GENERATE_BUTTON_FALLBACKS:
            try:
                btn = page.locator(sel).first
                if await btn.count() > 0:
                    await self._human_click(page, btn, timeout=10000)
                    log.info("Generate button clicked via fallback: %s", sel)
                    return True
            except Exception:
                continue

        log.error("Could not find generate button for images")
        return False

    async def _poll_and_download_image(self, page, video_id: str, timeout_ms: int = 300000) -> Path | None:
        """Poll for a generated image preview and download it.

        Utilizza DUE meccanismi in parallelo:
        1. Network listener — cattura risposte HTTP content-type image/* (come Flow)
        2. DOM polling — controlla img elements con dimensione > 200px
        """
        from .base import DOWNLOAD_DIR

        dest_dir = DOWNLOAD_DIR / "images" / "vids"
        dest_dir.mkdir(parents=True, exist_ok=True)

        # Collezioni per network listener
        captured_urls: set[str] = set()
        pre_existing_urls: set[str] = set()
        captured_digests: set[str] = set()
        saved_paths: list[Path] = []

        def _digest(data: bytes) -> str:
            import hashlib
            return hashlib.md5(data).hexdigest()

        # ── Network Listener (come Flow) ──────────────────────────────────
        async def on_response(response):
            if response.url in pre_existing_urls or response.url in captured_urls:
                return

            content_type = response.headers.get("content-type", "")
            if "image/" not in content_type:
                return

            try:
                body = await response.body()
                if not body or len(body) < 5000:  # almeno 5KB
                    return

                digest = _digest(body)
                if digest in captured_digests:
                    return

                ext = ".png" if "png" in content_type else ".jpg"
                filename = f"NET_{int(time.time())}_{len(saved_paths)}{ext}"
                path = dest_dir / filename
                path.write_bytes(body)

                captured_urls.add(response.url)
                captured_digests.add(digest)
                saved_paths.append(path)
                log.info("Network capture: %s (%d bytes) from %s", filename, len(body), response.url[:100])
            except Exception as e:
                log.debug("Network capture error: %s", e)

        # Blacklist immagini pre-esistenti
        try:
            all_imgs = await page.locator('img').all()
            for img in all_imgs:
                src = await img.get_attribute("src")
                if src:
                    pre_existing_urls.add(src)
            log.info("Blacklisted %d pre-existing images", len(pre_existing_urls))
        except Exception as e:
            log.debug("Blacklist error: %s", e)

        # Registra listener
        page.on("response", on_response)

        try:
            started_at = time.time()
            poll_interval_ms = 2500

            while (time.time() - started_at) * 1000 < timeout_ms:
                await page.wait_for_timeout(poll_interval_ms)

                # 1) Controlla se network ha già catturato immagini
                if saved_paths:
                    final_path = saved_paths[0]
                    log.info("Image obtained via Network listener: %s", final_path)
                    return final_path

                # 2) Controlla indicatori UI "generazione completata"
                generation_done = False
                for sel in self.GENERATION_DONE_SELECTORS:
                    try:
                        if await page.locator(sel).first.count() > 0:
                            generation_done = True
                            break
                    except Exception:
                        continue

                # 3) DOM polling: cerca img con dimensioni reali
                for selector in self.GENERATED_IMAGE_SELECTORS:
                    try:
                        candidates = page.locator(selector)
                        count = await candidates.count()
                        for idx in range(count):
                            loc = candidates.nth(idx)
                            try:
                                info = await loc.evaluate("""(el) => {
                                    const src = el.currentSrc || el.src || el.getAttribute('src') || '';
                                    const w = el.naturalWidth || el.width || 0;
                                    const h = el.naturalHeight || el.height || 0;
                                    const bg = window.getComputedStyle(el).backgroundImage || '';
                                    const visible = el.offsetWidth > 0 && el.offsetHeight > 0;
                                    return { src, w, h, bg, visible };
                                }""")
                                src = info.get("src", "")
                                w = info.get("w", 0)
                                h = info.get("h", 0)
                                bg = info.get("bg", "")
                                visible = info.get("visible", False)

                                # Se non c'è src, controlla background-image
                                if not src and bg and bg != "none":
                                    import re as _re
                                    m = _re.search(r'url\("?(.+?)"?\)', bg)
                                    if m:
                                        src = m.group(1)

                                if not src or not visible:
                                    continue
                                if src.startswith("data:"):
                                    continue
                                if w < 200 and h < 200:
                                    continue

                                # Pattern anti-icona
                                icon_patterns = [
                                    "icon", "material", "svg", "docs-",
                                    "apps-", "sketchy", "toolbar", "logo",
                                    "arrow", "menu", "close", "search",
                                ]
                                src_lower = src.lower()
                                if any(p in src_lower for p in icon_patterns):
                                    continue

                                # Scarica l'immagine via network
                                if src not in captured_urls:
                                    try:
                                        resp = await page.request.get(src, max_redirects=10)
                                        if resp.ok:
                                            body = await resp.body()
                                            if body and len(body) >= 5000:
                                                ext = ".png"
                                                ct = resp.headers.get("content-type", "")
                                                if "jpeg" in ct or "jpg" in ct:
                                                    ext = ".jpg"
                                                elif "webp" in ct:
                                                    ext = ".webp"
                                                filename = f"DOM_{int(time.time())}_{len(saved_paths)}{ext}"
                                                path = dest_dir / filename
                                                path.write_bytes(body)
                                                captured_urls.add(src)
                                                saved_paths.append(path)
                                                log.info("DOM capture: %s (%d bytes) via img[%s]",
                                                         filename, len(body), selector[:60])
                                                return path
                                    except Exception as e:
                                        log.debug("DOM download error: %s", e)

                            except Exception:
                                continue
                    except Exception:
                        continue

                log.info(
                    "Polling... %.1fs (generation_done=%s, network=%d, dom=%d)",
                    time.time() - started_at, generation_done,
                    len(captured_urls), len(saved_paths)
                )

            log.warning("Timeout polling for generated image.")
            log.warning("No generated image found after polling")
            return None

        finally:
            # Rimuovi il listener di rete
            try:
                page.remove_listener("response", on_response)
            except Exception:
                pass

    async def _debug_screenshot(self, page, name: str):
        """Helper to save a debug screenshot in the logs folder and upload to user's Drive."""
        try:
            timestamp = int(time.time())
            path = Path("logs") / f"vids_debug_{timestamp}_{name}.png"
            await page.screenshot(path=str(path))
            log.info("Saved debug screenshot: %s", path)
            
            # Automatically upload to user's Drive folder for live debugging
            try:
                from drive_client import upload_file_to_drive
                debug_folder_id = "1HinlvxnAFknV3wCSB9cuKA4gZVivdXC7"
                upload_file_to_drive(debug_folder_id, path, path.name, "image/png")
                log.info("Successfully uploaded debug screenshot %s to Drive folder %s", path.name, debug_folder_id)
            except Exception as e_upload:
                log.warning("Failed to upload debug screenshot to Drive: %s", e_upload)
        except Exception as e:
            log.warning("Failed to save debug screenshot '%s': %s", name, e)

    async def generate_vids_image(
        self,
        video_id: str,
        prompt: str,
        aspect_ratio: str = "16:9",
    ) -> Path | None:
        """
        Generates an image inside Google Vids using the built-in image synthesis tool.

        Steps:
        1. Open Vids project (or create new)
        2. Click "Immagini" tab in the content library rail
        3. Set aspect ratio to 16:9
        4. Fill prompt textarea
        5. Click Generate
        6. Poll and download the generated image
        """
        from storage import get_project_id, save_project_id, get_structured_path, save_media_asset, save_generation_metadata

        if not video_id or video_id == "new":
            video_id = get_project_id("vids")
            video_id = video_id if video_id else "new"

        selector_report = {
            "kind": "vids_image",
            "video_id": video_id,
            "prompt_preview": prompt[:120],
            "attempts": [],
            "generated_at": int(time.time()),
        }

                # Usa SEMPRE il progetto condiviso per evitare il popup "Ciao Massimo"
        # Se però fallisce, proveremo a crearne uno nuovo
        effective_video_id = video_id if (video_id and video_id != "new") else self.SHARED_VIDS_PROJECT_ID

        if self._external_page:
            page = self.page
            if effective_video_id not in page.url:
                project_url = (
                    f"https://docs.google.com/videos/d/{effective_video_id}/edit"
                    f"?scene={self.SHARED_VIDS_PROJECT_SCENE}"
                    f"#scene={self.SHARED_VIDS_PROJECT_SCENE}"
                )
                log.info(f"Navigating to project {effective_video_id}...")
                await page.goto(project_url, wait_until="domcontentloaded", timeout=60000)
                await asyncio.sleep(10)
            log.info("Using preloaded external page for image generation")
        else:
            page = await self._goto_home()
            project_url = (
                f"https://docs.google.com/videos/d/{effective_video_id}/edit"
                f"?scene={self.SHARED_VIDS_PROJECT_SCENE}"
                f"#scene={self.SHARED_VIDS_PROJECT_SCENE}"
            )
            log.info(f"Navigating to project {effective_video_id}...")
            await page.goto(project_url, wait_until="domcontentloaded", timeout=60000)
            await asyncio.sleep(15)

        from .modal_handler import start_modal_killer
        stop_modals = asyncio.Event()
        modal_task = asyncio.create_task(start_modal_killer(page, stop_modals))
        
        try:
            # Attendiamo che l'editor carichi la sidebar prima di procedere
            log.info("Waiting for Vids editor sidebar to load...")
            sidebar_found = False
            editor_selectors = [
                self.IMMAGINI_TAB_SELECTOR,
                'text="Immagine"',
                'text="Immagini"',
                'text="Images"',
                'text="Avatar"',
                'div[role="menubar"]',
                '.sketchy-desktop-viewport',
            ]
            for sel in editor_selectors:
                try:
                    await page.wait_for_selector(sel, timeout=10000)
                    sidebar_found = True
                    log.info(f"Editor sidebar detected via selector: {sel}")
                    break
                except Exception:
                    continue
            if not sidebar_found:
                log.warning("Sidebar not found in shared project. Attempting to create a NEW project via Vids Home...")
                try:
                    await page.goto("https://vids.google.com", wait_until="domcontentloaded")
                    await asyncio.sleep(8)
                    create_btn = page.locator('div[role="button"]:has-text("Crea"), div[role="button"]:has-text("Create")').first
                    if await create_btn.count() > 0:
                        await create_btn.click()
                        await asyncio.sleep(15)
                        await page.wait_for_selector(self.IMMAGINI_TAB_SELECTOR, timeout=30000)
                        sidebar_found = True
                        log.info("Sidebar detected in new project!")
                except Exception as e2:
                    log.error(f"Failed to create new project as fallback: {e2}")

            if not sidebar_found:
                log.warning("Sidebar still not detected, proceeding anyway (fingers crossed)")

            # No popup dismissal needed - we're using the shared existing project

            # Deselect any active elements (Escape + backdrop click) to ensure contextual toolbars (e.g. Image Options) are closed
            try:
                from .modal_handler import dismiss_vids_modals
                await dismiss_vids_modals(page)
                
                log.info("Deselecting active elements to show main toolbar... (Current URL: %s)", page.url)
                for _ in range(3):
                    await page.keyboard.press("Escape")
                    await human_delay(200, 400)
                
                # Check again after Escape
                await dismiss_vids_modals(page)
                
                backdrop_sel = '.sketchy-desktop-viewport, .docs-editor-container, .apps-layer-front'
                backdrop = page.locator(backdrop_sel).first
                if await backdrop.count() > 0:
                    await self._human_click(page, backdrop, timeout=3000)
                    await human_delay(300, 600)
            except Exception as e:
                log.warning("Deselect failed: %s", e)

            # ── Step 1: Click "Immagini" tab ──────────────────────────────────
            await self._debug_screenshot(page, "before_tab_click")
            log.info("Step 1: Waiting 8 seconds for React event listeners to hydrate...")
            await asyncio.sleep(8)
            log.info("Step 1: Clicking Immagini tab...")
            immagini_clicked = False
            
            immagini_tab_selectors = [
                self.IMMAGINI_TAB_SELECTOR,
                '#content-library-rail-image-synthesis-element',
                '[id*="image-synthesis"]',
                'text="Immagine"',
                'text="Immagini"',
                'text="Images"',
                '[aria-label*="Immagine" i]',
                '[aria-label*="Immagini" i]',
                '[aria-label*="Images" i]',
                '[data-tooltip*="Immagine" i]',
                '[data-tooltip*="Immagini" i]',
                '[data-tooltip*="Images" i]',
                'button:has-text("Immagine")',
                'button:has-text("Immagini")',
                'button:has-text("Images")',
                'div[role="button"]:has-text("Immagine")',
                'div[role="button"]:has-text("Immagini")',
                'div[role="button"]:has-text("Images")',
                'div:has-text("Immagine")',
                'div:has-text("Immagini")',
                'div:has-text("Images")',
                '#content-library-rail-image-synthesis-element button',
            ]
            
            prompt_textarea_selectors = [
                'textarea.docs-prompt-view-input',
                'textarea.docs-image-synthesis-search-bar-input',
                'textarea[aria-label*="Descrivi" i]',
                'textarea[placeholder*="Descrivi" i]',
                'textarea[placeholder*="Describe" i]',
            ]
            
            async def is_panel_open():
                for p_sel in prompt_textarea_selectors:
                    try:
                        loc = page.locator(p_sel).first
                        if await loc.count() > 0 and await loc.is_visible():
                            return True
                    except Exception:
                        pass
                return False

            for sel in immagini_tab_selectors:
                try:
                    btn = page.locator(sel).first
                    if await btn.count() > 0:
                        log.info(f"Step 1: Attempting click on Immagine tab via selector: {sel}")
                        await self._human_click(page, btn, timeout=10000)
                        await asyncio.sleep(4)
                        if await is_panel_open():
                            immagini_clicked = True
                            log.info(f"Clicked Immagini tab successfully via selector: {sel} and verified panel is open!")
                            await self._debug_screenshot(page, "after_tab_click")
                            break
                        else:
                            log.warning(f"Clicked selector {sel} but panel is still not open (no visible textarea).")
                except Exception as e:
                    log.warning(f"Failed to click Immagini tab using selector {sel}: {e}")
                    
            if not immagini_clicked:
                # Try fallback clicks directly in case the initial loop missed it
                fallback_clicks = [
                    'text="Immagine"',
                    'text="Immagini"',
                    'text="Images"',
                    '[role="button"][aria-label*="Immagine" i]',
                    '[role="button"][aria-label*="Images" i]',
                    'button[aria-label*="Immagine" i]',
                    'button[aria-label*="Images" i]',
                    '[role="button"]:has-text("Immagine")',
                    '[role="button"]:has-text("Images")',
                    'button:has-text("Immagine")',
                    'button:has-text("Images")',
                    'div[role="button"]:has-text("Immagine")',
                    'div[role="button"]:has-text("Images")',
                    'div:has-text("Immagine")',
                    'div:has-text("Immagini")',
                    'div:has-text("Images")',
                ]
                for sel in fallback_clicks:
                    try:
                        loc = page.locator(sel).first
                        if await loc.count() > 0:
                            log.info("Retrying Immagini click via fallback selector: %s", sel)
                            await self._human_click(page, loc, timeout=10000)
                            await asyncio.sleep(4)
                            if await is_panel_open():
                                immagini_clicked = True
                                log.info(f"Fallback click succeeded via selector: {sel}")
                                await self._debug_screenshot(page, "after_tab_click")
                                break
                    except Exception:
                        continue
                    
            if not immagini_clicked:
                # Let's save a screenshot specifically for this failure
                await page.screenshot(path="logs/vids_img_error.png")
                raise RuntimeError("Immagini tab not found in Google Vids UI")

            selector_report["attempts"].append({
                "stage": "click_immagini_tab",
                "found": immagini_clicked,
            })
            if not immagini_clicked:
                raise RuntimeError("Immagini tab click failed")

            log.info("Panel is verified open. Proceeding to aspect ratio.")

            # Attesa lunga per far caricare il pannello laterale
            log.info("Waiting for image generation panel to load...")
            await asyncio.sleep(5)
            # Simula un movimento del mouse per attivare lazy loading
            try:
                await page.mouse.move(
                    random.randint(300, 600),
                    random.randint(300, 600),
                    steps=10,
                )
            except Exception:
                pass
            await asyncio.sleep(3)

            # ── Step 2: Set aspect ratio to 16:9 ────────────────────────────
            await self._debug_screenshot(page, "before_aspect_ratio")
            log.info("Step 2: Setting aspect ratio to 16:9...")
            ratio_set = await self._set_aspect_ratio_16_9(page)
            selector_report["attempts"].append({
                "stage": "set_aspect_ratio",
                "found": ratio_set,
            })
            await human_delay(500, 1000)
            await self._debug_screenshot(page, "after_aspect_ratio")

            # ── Step 3: Fill prompt ──────────────────────────────────────────
            await self._debug_screenshot(page, "before_fill_prompt")
            from .modal_handler import dismiss_vids_modals
            await dismiss_vids_modals(page)
            log.info("Step 3: Filling prompt...")
            prompt_filled = await self._fill_image_prompt(page, prompt)
            selector_report["attempts"].append({
                "stage": "fill_prompt",
                "found": prompt_filled,
            })
            await human_delay(800, 1500)

            if not prompt_filled:
                raise RuntimeError("Could not fill image prompt in Google Vids UI")

            # Wait for the UI to enable the create button after the prompt input event.
            await dismiss_vids_modals(page)
            log.info("Step 3b: Waiting for generate button to enable...")
            try:
                await page.wait_for_function(
                    """() => {
                        const btn = document.querySelector('button.image-synthesis-creation-button.image-synthesis-begin-create');
                        if (!btn) return false;
                        const cls = (btn.className || '').toString();
                        return !cls.includes('goog-button-disabled') && !cls.includes('disabled');
                    }""",
                    timeout=10000,
                )
                log.info("Generate button enabled")
                await self._debug_screenshot(page, "after_prompt_filled")
            except Exception as e:
                log.warning("Generate button did not report enabled state in time: %s", e)

            # ── Step 4: Click Generate ───────────────────────────────────────
            await self._debug_screenshot(page, "before_click_generate")
            await dismiss_vids_modals(page)
            log.info("Step 4: Clicking Generate button...")
            generate_clicked = await self._click_generate_image(page)
            selector_report["attempts"].append({
                "stage": "click_generate",
                "found": generate_clicked,
            })

            if not generate_clicked:
                raise RuntimeError("Generate button not found for image synthesis")

            # ── Step 5: Poll and download ────────────────────────────────────
            await self._debug_screenshot(page, "after_click_generate")
            log.info("Step 5: Polling for generated image...")
            final_path = await self._poll_and_download_image(page, video_id, timeout_ms=300000)

            if final_path:
                dest_folder = get_structured_path(
                    media_type="images",
                    style="vids",
                    sub_style="generated",
                )
                metadata = {
                    "prompt": prompt,
                    "aspect_ratio": aspect_ratio,
                    "video_id": video_id,
                    "source": "GOOGLE_VIDS_IMAGES",
                    "timestamp": int(time.time()),
                }
                save_generation_metadata(dest_folder, metadata)
                save_media_asset(
                    final_path,
                    "GOOGLE_VIDS_IMAGES",
                    final_path.name,
                    "image",
                    "vids",
                    "generated",
                    prompt,
                    video_id,
                    metadata,
                )
                log.info("Image generated and saved: %s", final_path)

            return final_path

        except Exception as e:
            log.error("Error generating Vids image: %s", e, exc_info=True)
            try:
                await page.screenshot(path="logs/vids_img_error.png", full_page=True)
                log.info("Saved error screenshot to logs/vids_img_error.png")
            except Exception as se:
                log.warning("Failed to save error screenshot: %s", se)
            selector_report["result"] = "failed"
            selector_report["error"] = str(e)
            return None
        finally:
            stop_modals.set()
            await modal_task
            selector_report.setdefault("result", "success")
            _append_selector_report(selector_report)
            await page.close()
