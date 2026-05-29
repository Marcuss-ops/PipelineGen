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
    IMMAGINI_TAB_SELECTOR = 'div.appsSketchyContentLibraryRailToolbarButtonIconRefreshed'

    ASPECT_RATIO_TRIGGER_SELECTOR = (
        'div.appsDocsAiGenerativeaiImageGenerateImagesInputAspectRatioPicker'
    )


    PROMPT_TEXTAREA_SELECTOR = (
        'textarea.docs-prompt-view-input.docs-image-synthesis-search-bar-input'
    )

    GENERATE_BUTTON_SELECTOR = (
        'button.image-synthesis-creation-button.image-synthesis-begin-create'
    )

    # Fallback selectors
    PROMPT_TEXTAREA_FALLBACKS = [
        'textarea.docs-prompt-view-input',
        'textarea.docs-image-synthesis-search-bar-input',
        'textarea[placeholder*="Descrivi la tua idea"]',
        '.docs-prompt-view-input-container textarea',
        'textarea',
    ]

    GENERATE_BUTTON_FALLBACKS = [
        'button.image-synthesis-begin-create',
        'button[aria-label="Crea"]',
        'button[data-tooltip="Crea"]',
        'button:has-text("Crea")',
        'button:has-text("Genera")',
    ]

    # ── Selector per il pulsante Veo 3.1 / "Video vuoto" all'avvio ──
    VEO_BUTTON_SELECTORS = [
        'button[data-view-id="getting-started-dialog-videogen"]',
        'button.appsDocsGettingStartedEntryPointSelectionViewButton.videogen',
        'button.videogen',
        '[data-view-id*="videogen"]',
        '[data-view-id*="video-gen"]',
        'button:has-text("Veo 3.1")',
        'button:has-text("Veo")',
    ]

    BLANK_VIDEO_SELECTORS = [
        'button.appsDocsGettingStartedEntryPointSelectionViewBlankCard',
        'button:has-text("Video vuoto")',
        'button:has-text("Blank video")',
        '[class*="BlankCard"]',
        'button[class*="EntryPointSelectionView"]',
    ]

    # ── Selector per chiudere il dialog "Inizia" all'avvio ──────────────
    GETTING_STARTED_CLOSE_SELECTORS = [
        'span.getting-started-dialog-close.docs-material-gm-dialog-title-close',
        'span.getting-started-dialog-close',
        '[aria-label="Chiudi"][data-tooltip="Chiudi"]',
        '.getting-started-dialog-close',
        '.docs-material-gm-dialog-title-close',
        'button[aria-label="Chiudi"]',
    ]

    # ── Selector per il backdrop del dialog ──────────────────────────────
    GETTING_STARTED_BACKDROP_SELECTORS = [
        '/html/body/div[2]/div/div[2]/span',
        'div.docs-material-gm-dialog-bg',
        'div[class*="dialog-bg"]',
    ]

    # ── Helper per click umano (hover + click) ─────────────────────────────
    async def _human_click(self, page, locator, timeout: int = 10000) -> bool:
        """
        Hover sul centro dell'elemento, pausa umana, poi click.
        Sostituisce click(force=True) per evitare rilevamento bot.
        """
        try:
            box = await locator.bounding_box(timeout=timeout)
            if not box:
                log.warning("human_click: bounding_box not found")
                await locator.click(force=True, timeout=timeout)
                return True

            # Coordinate randomiche dentro il box (per sembrare umano)
            x = box["x"] + box["width"] * random.uniform(0.2, 0.8)
            y = box["y"] + box["height"] * random.uniform(0.2, 0.8)

            # Movimento lento verso il punto
            steps = random.randint(5, 12)
            for i in range(steps):
                progress = (i + 1) / steps
                cx = box["x"] + box["width"] * 0.5
                cy = box["y"] + box["height"] * 0.5
                intermediate_x = cx * progress + x * (1 - progress) + random.uniform(-2, 2)
                intermediate_y = cy * progress + y * (1 - progress) + random.uniform(-2, 2)
                await page.mouse.move(intermediate_x, intermediate_y)
                await asyncio.sleep(random.uniform(0.01, 0.03))

            # Pausa sull'elemento prima di cliccare
            await asyncio.sleep(random.uniform(0.1, 0.4))

            await page.mouse.click(x, y)
            log.info("Human click at (%.0f, %.0f) size=%.0fx%.0f", x, y, box["width"], box["height"])
            return True
        except Exception as e:
            log.warning("human_click fallback to force click: %s", e)
            try:
                await locator.click(force=True, timeout=timeout)
                return True
            except Exception:
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

        # Prova a cliccare direttamente il trigger dell'aspect ratio
        try:
            picker = page.locator(self.ASPECT_RATIO_TRIGGER_SELECTOR).first
            if await picker.count() > 0:
                await self._human_click(page, picker, timeout=5000)
                await human_delay(500, 1000)
                log.info("Clicked aspect ratio picker trigger")
            else:
                log.warning("Aspect ratio picker trigger not found, trying fallback")
                return False
        except Exception as e:
            log.warning("Failed to click aspect ratio picker: %s", e)
            return False

        # Ora seleziona "Orizzontale 16:9" dal dropdown
        for sel in [
            'div[role="option"][aria-label*="16:9"]',
            'div[role="option"]:has-text("16:9")',
            'div[role="option"]:has-text("Orizzontale")',
            'div[data-tooltip*="16:9"]',
            'div[aria-label*="Orizzontale"]',
        ]:
            try:
                opt = page.locator(sel).first
                if await opt.count() > 0:
                    await self._human_click(page, opt, timeout=5000)
                    log.info("Selected 16:9 aspect ratio via: %s", sel)
                    await human_delay(300, 700)
                    return True
            except Exception:
                continue

        log.warning("Could not select 16:9 aspect ratio from dropdown")
        return False

    async def _fill_image_prompt(self, page, prompt: str) -> bool:
        """Fills the prompt textarea with the given prompt.
        
        Tecnica anti-bot: dopo il fill, aggiunge qualche carattere e li rimuove
        per simulare una digitazione più umana (fingerprint removal).
        """
        log.info("Filling image prompt...")

        ta = None
        selector_used = None

        # Prova prima con il selettore esatto
        try:
            ta = page.locator(self.PROMPT_TEXTAREA_SELECTOR).first
            if await ta.count() > 0 and await ta.is_visible():
                selector_used = "exact"
            else:
                ta = None
        except Exception as e:
            log.warning("Exact prompt selector failed: %s", e)

        # Fallback: prova i selettori alternativi
        if ta is None:
            for sel in self.PROMPT_TEXTAREA_FALLBACKS:
                try:
                    ta = page.locator(sel).first
                    if await ta.count() > 0 and await ta.is_visible():
                        selector_used = f"fallback: {sel}"
                        break
                    else:
                        ta = None
                except Exception:
                    continue

        if ta is None:
            log.error("Could not find prompt textarea")
            return False

        # Click + fill (evita timeout con prompt lunghi)
        await self._human_click(page, ta)
        await human_delay(200, 500)
        await ta.fill(prompt)
        log.info("Prompt filled via %s", selector_used)

        # ── ANTI-BOT FINGERPRINT: applica a TUTTI i path ──
        await self._apply_anti_bot_fingerprint(page)
        # ── FINE ANTI-BOT ──────────────────────────────────────────

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

            # Se siamo arrivati qui, prova un ultimo tentativo: cattura tutto il DOM
            log.warning("Timeout polling. Tentativo finale: dump completo DOM...")
            try:
                await page.screenshot(path="logs/vids_img_final.png", full_page=True)
                # Dump tutti gli URL delle img nel DOM
                img_urls = await page.evaluate("""() => {
                    const imgs = document.querySelectorAll('img');
                    return Array.from(imgs).map(el => ({
                        src: el.currentSrc || el.src || el.getAttribute('src') || '',
                        w: el.naturalWidth || el.width,
                        h: el.naturalHeight || el.height,
                        visible: el.offsetWidth > 0 && el.offsetHeight > 0,
                        tag: el.tagName,
                    }));
                }""")
                log.info("DOM dump complete: %d img elements found", len(img_urls))
                for item in img_urls:
                    log.info("  img: src=%s.. size=%dx%d visible=%s",
                             item["src"][:80], item["w"], item["h"], item["visible"])
            except Exception as e:
                log.warning("DOM dump failed: %s", e)

            log.warning("No generated image found after polling")
            return None

        finally:
            # Rimuovi il listener di rete
            try:
                page.remove_listener("response", on_response)
            except Exception:
                pass

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

        if self._external_page:
            page = self.page
            log.info("Using preloaded external page for image generation, bypassing navigation")
        else:
            page = await self._goto_home()
            try:
                if video_id == "new" or not video_id:
                    log.info("Creating new Vids project for Image Generation")
                    created = False
                    # Prova il metodo diretto /videos/create (funziona in generate_video)
                    try:
                        await page.goto(
                            "https://docs.google.com/videos/create",
                            wait_until="domcontentloaded",
                        )
                        await asyncio.sleep(8)
                        try:
                            await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
                            created = True
                            log.info("Created Vids project via /videos/create")
                        except Exception:
                            log.info("wait_for_url failed for /videos/create, current url: %s", page.url)
                    except Exception as e:
                        log.warning("Failed to create via /videos/create: %s", e)
                    
                    if not created:
                        # Fallback: clicca pulsante nuovo video
                        for sel in [
                            '[aria-label="Inizia un nuovo video"]',
                            '[aria-label="Crea nuovo video"]',
                        ]:
                            try:
                                loc = page.locator(sel).first
                                if await loc.count() > 0:
                                    await self._human_click(page, loc, timeout=10000)
                                    created = True
                                    log.info("Clicked new video button: %s", sel)
                                    break
                            except Exception:
                                continue
                    if not created:
                        # Ultimo fallback: naviga direttamente
                        await page.goto(
                            "https://docs.google.com/videos/create",
                            wait_until="domcontentloaded",
                        )
                        try:
                            await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
                        except Exception:
                            pass
                    video_id = self._extract_vid_id(page.url)
                    if video_id and video_id != "new":
                        save_project_id("vids", video_id)
                else:
                    log.info("Opening existing Vids project: %s", video_id)
                    await page.goto(
                        f"https://docs.google.com/videos/d/{video_id}/edit",
                        wait_until="domcontentloaded",
                    )

                log.info("Waiting for editor to stabilize...")
                await asyncio.sleep(15)
            except Exception as e:
                log.error("Navigation setup failed: %s", e)

        try:

            # Rimuovi dialoghi bloccanti
            log.info("Removing hanging/stuck dialog backdrops via JS injection...")
            try:
                await page.evaluate("""() => {
                    const bgs = document.querySelectorAll('.docs-material-gm-dialog-bg, [class*="dialog-bg"], .modal-backdrop');
                    bgs.forEach(el => el.remove());
                    const dialogs = document.querySelectorAll('div[role="dialog"], .docs-material-gm-dialog, .modal-dialog');
                    dialogs.forEach(el => el.remove());
                }""")
                await asyncio.sleep(2)
            except Exception as e:
                log.warning("Failed to remove dialog backdrops via JS: %s", e)

            # Dismiss dialogs con umanizzazione
            for _ in range(3):
                try:
                    await page.keyboard.press("Escape")
                    await asyncio.sleep(0.5)
                except Exception:
                    pass
                try:
                    btns = page.locator(
                        '[aria-label="Chiudi"], [aria-label="Close"], [aria-label="Dismiss"], '
                        '.modal-dialog-close, .docs-material-dialog-close, button:has-text("Chiudi"), '
                        'button:has-text("Close"), button:has-text("OK"), button:has-text("Ho capito"), '
                        '[data-view-id*="getting-started"] button'
                    )
                    if await btns.count() > 0:
                        await self._human_click(page, btns.first, timeout=3000)
                        await human_delay(400, 1000)
                except Exception:
                    pass

            # ── Step 0: Click Veo 3.1 or "Video vuoto" if present ───────────
            log.info("Step 0: Checking for Veo 3.1 / 'Video vuoto' button...")
            entry_clicked = False
            # Prima prova Veo 3.1 (carica dashboard direttamente)
            for sel in self.VEO_BUTTON_SELECTORS:
                try:
                    btn = page.locator(sel).first
                    if await btn.count() > 0 and await btn.is_visible():
                        await self._human_click(page, btn, timeout=10000)
                        await human_delay(2000, 4000)
                        entry_clicked = True
                        log.info("Clicked Veo 3.1 button via: %s", sel)
                        break
                except Exception as e:
                    log.warning("Veo selector %s failed: %s", sel, e)
            # Fallback: "Video vuoto"
            if not entry_clicked:
                log.info("Veo 3.1 not found, trying 'Video vuoto'...")
                for sel in self.BLANK_VIDEO_SELECTORS:
                    try:
                        btn = page.locator(sel).first
                        if await btn.count() > 0 and await btn.is_visible():
                            await self._human_click(page, btn, timeout=10000)
                            await human_delay(2000, 4000)
                            entry_clicked = True
                            log.info("Clicked 'Video vuoto' button via: %s", sel)
                            break
                    except Exception as e:
                        log.warning("Blank video selector %s failed: %s", sel, e)
            if not entry_clicked:
                log.info("No entry button found (maybe already in Vids editor)")

            # ── Step 0b: Chiudi dialog "Inizia" / getting-started ────────────
            if entry_clicked:
                log.info("Step 0b: Checking for getting-started dialog to close...")
                dialog_closed = False
                # Prima: X button di chiusura
                for sel in self.GETTING_STARTED_CLOSE_SELECTORS:
                    try:
                        btn = page.locator(sel).first
                        if await btn.count() > 0 and await btn.is_visible():
                            await self._human_click(page, btn, timeout=5000)
                            await human_delay(500, 1200)
                            dialog_closed = True
                            log.info("Closed getting-started dialog via: %s", sel)
                            break
                    except Exception as e:
                        log.warning("Getting-started close selector %s failed: %s", sel, e)
                # Fallback: clicca il backdrop per chiudere
                if not dialog_closed:
                    for sel in self.GETTING_STARTED_BACKDROP_SELECTORS:
                        try:
                            backdrop = page.locator(sel).first
                            if await backdrop.count() > 0:
                                await self._human_click(page, backdrop, timeout=5000)
                                await human_delay(500, 1200)
                                dialog_closed = True
                                log.info("Closed dialog via backdrop: %s", sel)
                                break
                        except Exception as e:
                            log.warning("Backdrop selector %s failed: %s", sel, e)
                if not dialog_closed:
                    log.info("No getting-started dialog to close")

            # ── Step 1: Click "Immagini" tab ──────────────────────────────────
            log.info("Step 1: Clicking Immagini tab...")
            immagini_clicked = False

            # IMMAGINI TAB: prima prova con l'icona toolbar + testo "Immagini" (selettore esatto)
            try:
                toolbar_btns = page.locator(self.IMMAGINI_TAB_SELECTOR)
                count = await toolbar_btns.count()
                for idx in range(count):
                    btn = toolbar_btns.nth(idx)
                    try:
                        btn_text = await btn.inner_text()
                        parent_text = await btn.locator("..").inner_text()
                        grandparent_text = await btn.locator("../..").inner_text()
                        combined = (btn_text + " " + parent_text + " " + grandparent_text).lower()
                        if "immagin" in combined or "image" in combined:
                            await self._human_click(page, btn, timeout=10000)
                            immagini_clicked = True
                            log.info("Clicked Immagini tab via toolbar button (idx=%d) text=%s", idx, combined[:100])
                            break
                    except Exception:
                        continue
            except Exception as e:
                log.warning("Failed to click Immagini via toolbar: %s", e)

            if not immagini_clicked:
                # Fallback: selectors specifici (ID, aria-label)
                immagini_selectors = [
                    '#content-library-rail-image-generation-element',
                    '#content-library-rail-images-element',
                    '#content-library-rail-immagini-element',
                    '[aria-label*="Genera un\'immagine" i]',
                    '[aria-label*="Genera immagin" i]',
                    '[data-view-id*="image-generation"]',
                    '[data-view-id*="image"]',
                    # Esclude 'Ritaglia'
                    '[aria-label*="immagin" i]:not([aria-label*="Ritaglia" i]):not([aria-label*="crop" i]):not([aria-label*="mask" i]):not([aria-label*="maschera" i]):not([class*="toolbar" i]):not([id*="mask" i])',
                    '[aria-label="Images"]',
                    '[aria-label*="image" i]:not([aria-label*="Ritaglia" i]):not([aria-label*="crop" i]):not([aria-label*="mask" i]):not([aria-label*="maschera" i]):not([class*="toolbar" i]):not([id*="mask" i])',
                ]
                for sel in immagini_selectors:
                    try:
                        loc = page.locator(sel).first
                        if await loc.count() > 0:
                            await self._human_click(page, loc, timeout=10000)
                            immagini_clicked = True
                            log.info("Clicked Immagini tab via: %s", sel)
                            break
                    except Exception as e:
                        log.warning("Selector failed %s: %s", sel, e)

            if not immagini_clicked:
                # Fallback: cerca per qualsiasi elemento che contenga "Immagini"
                for sel in [
                    'div[role="tab"]:has-text("Immagini")',
                    'div[role="tab"]:has-text("Images")',
                    'div[role="button"]:has-text("Immagini")',
                    'div[role="button"]:has-text("Images")',
                    'span:has-text("Immagini")',
                    'span:has-text("Images")',
                    'button:has-text("Immagini")',
                    'button:has-text("Images")',
                    'a:has-text("Immagini")',
                    'a:has-text("Images")',
                    '[class*="tab"]:has-text("Immagini")',
                    '[class*="tab"]:has-text("Images")',
                    ':has-text("Immagini")',
                ]:
                    try:
                        loc = page.locator(sel).first
                        if await loc.count() > 0:
                            await self._human_click(page, loc, timeout=10000)
                            immagini_clicked = True
                            log.info("Clicked Immagini tab via text: %s", sel)
                            break
                    except Exception:
                        continue

            if not immagini_clicked:
                # Debug: dump della UI per capire cosa c'è
                log.info("Immagini tab not found, dumping UI for debugging...")
                try:
                    await page.screenshot(path="logs/vids_img_debug.png", full_page=True)
                    # Dump all elements with ID, aria-label, role
                    dump = await page.evaluate("""() => {
                        const all = document.querySelectorAll('[id],[aria-label],[role="tab"],[role="button"],[class*="tab"]');
                        return Array.from(all).slice(0, 100).map(el => ({
                            tag: el.tagName,
                            id: el.id || '',
                            class: (el.className || '').toString().substring(0, 60),
                            aria: el.getAttribute('aria-label') || '',
                            role: el.getAttribute('role') || '',
                            text: (el.textContent || '').trim().substring(0, 80),
                        }));
                    }""")
                    for item in dump:
                        log.info("UI element: %s", item)
                except Exception as e:
                    log.warning("UI dump failed: %s", e)
                log.error("Could not find Immagini tab")
                raise RuntimeError("Immagini tab not found in Google Vids UI")

            selector_report["attempts"].append({
                "stage": "click_immagini_tab",
                "found": immagini_clicked,
            })
            if not immagini_clicked:
                raise RuntimeError("Immagini tab click failed")
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
            log.info("Step 2: Setting aspect ratio to 16:9...")
            ratio_set = await self._set_aspect_ratio_16_9(page)
            selector_report["attempts"].append({
                "stage": "set_aspect_ratio",
                "found": ratio_set,
            })
            await human_delay(500, 1000)

            # ── Step 3: Fill prompt ──────────────────────────────────────────
            log.info("Step 3: Filling prompt...")
            prompt_filled = await self._fill_image_prompt(page, prompt)
            selector_report["attempts"].append({
                "stage": "fill_prompt",
                "found": prompt_filled,
            })
            await human_delay(800, 1500)

            if not prompt_filled:
                raise RuntimeError("Could not fill image prompt in Google Vids UI")

            # ── Step 3b: Tab per sbloccare tasto genera ─────────────────────
            log.info("Step 3b: Tab after prompt to unlock generate button...")
            await asyncio.sleep(1)
            try:
                await page.keyboard.press("Tab")
                await human_delay(500, 1000)
                # Aggiungi anche uno spazio se Tab non basta
                await page.keyboard.press("Tab")
                await human_delay(500, 1000)
                log.info("Pressed Tab twice after prompt fill")
            except Exception as e:
                log.warning("Tab press failed: %s", e)

            # ── Step 4: Click Generate ───────────────────────────────────────
            log.info("Step 4: Clicking Generate button...")
            generate_clicked = await self._click_generate_image(page)
            selector_report["attempts"].append({
                "stage": "click_generate",
                "found": generate_clicked,
            })

            if not generate_clicked:
                raise RuntimeError("Generate button not found for image synthesis")

            # ── Step 5: Poll and download ────────────────────────────────────
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
            selector_report.setdefault("result", "success")
            _append_selector_report(selector_report)
            await page.close()
