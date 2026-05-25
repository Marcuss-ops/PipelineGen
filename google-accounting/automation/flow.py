import asyncio
import os
import random
import time
import urllib.parse
import urllib.request
from pathlib import Path

from playwright.async_api import Page

from .base import (
    DOWNLOAD_DIR,
    BaseAutomation,
    _append_selector_report,
    human_delay,
    human_scroll,
    log,
)

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
        # Selettori specifici per il campo prompt di Flow (div contenteditable con ruolo textbox)
        'div[contenteditable="true"][role="textbox"]',
        'div[contenteditable="true"][aria-label*="prompt" i]',
        'div[contenteditable="true"][aria-label*="descrivi" i]',
        'textarea[aria-label*="prompt" i]',
        'textarea',
        # Fallback generico — ultima spiaggia
        '[contenteditable="true"]',
    ]

    GENERATE_BUTTON_SELECTORS = [
        'button[aria-label*="genera" i]',
        'button[aria-label*="create" i]',
        'button[aria-label*="generate" i]',
        'button:has-text("Crea")',
        'button:has-text("Genera")',
        'button:has-text("Generate")',
        # Material Icons "arrow_forward" è una ligatura, non testo visibile
        # Il pulsante con l'icona freccia è spesso l'ultimo nell'angolo
        'button:has(.material-icons:has-text("arrow_forward"))',
        'button:has(i:has-text("arrow_forward"))',
    ]

    GENERATED_IMAGE_SELECTORS = [
        'img[alt="Immagine generata"]',
        'img[alt*="generata" i]',
        'img[src*="/fx/api/trpc/media.getMediaUrlRedirect"]',
    ]

    async def generate_images(self, prompt: str, project_id: str = None, style: str = None) -> list[Path]:
        dest_dir = DOWNLOAD_DIR / "images" / (project_id or "general")
        dest_dir.mkdir(parents=True, exist_ok=True)
        selector_report = {
            "kind": "flow",
            "project_id": project_id,
            "prompt_preview": prompt[:120],
            "style": style or "",
            "attempts": [],
            "generated_at": int(time.time()),
        }
        
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

        # Clicca il pulsante "Create with Google Flow" o "Try Google Flow"
        # per passare dalla landing page al workspace vero e proprio.
        cta_selectors = [
            'button:has-text("Create with Google Flow")',
            'button:has-text("Try Google Flow")',
            'button:has-text("Try in Google Flow")',
            'a:has-text("Create with Google Flow")',
        ]
        for cta_sel in cta_selectors:
            cta_btn = page.locator(cta_sel).first
            if await cta_btn.count() > 0 and await cta_btn.is_visible():
                log.info("Click su CTA: %s", cta_sel)
                await cta_btn.click()
                await asyncio.sleep(5)
                await page.wait_for_load_state("networkidle", timeout=30000)
                break

        # Dopo il CTA si arriva al dashboard. Clicca "+ Nuovo progetto"
        # per entrare nel workspace con il campo prompt.
        new_project_selectors = [
            'button:has-text("Nuovo progetto")',
            'button:has-text("New project")',
            'button:has-text("+ Nuovo progetto")',
            'a:has-text("Nuovo progetto")',
            '[aria-label*="nuovo progetto" i]',
            '[aria-label*="new project" i]',
        ]
        for np_sel in new_project_selectors:
            np_btn = page.locator(np_sel).first
            if await np_btn.count() > 0 and await np_btn.is_visible():
                log.info("Click su nuovo progetto: %s", np_sel)
                await np_btn.click()
                await asyncio.sleep(5)
                await page.wait_for_load_state("networkidle", timeout=30000)
                break

        new_saved_paths = []
        captured_response_urls: set[str] = set()
        # Dedup basato sul contenuto: traccia MD5 delle immagini salvate
        # Funziona anche quando response.url e img src sono URL diversi
        saved_content_digests: set[str] = set()
        can_capture = False

        def _compute_digest(data: bytes) -> str:
            import hashlib
            return hashlib.md5(data).hexdigest()

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
                digest = _compute_digest(body)
                if digest in saved_content_digests:
                    log.debug("Response handler: contenuto già salvato (digest=%s), skip", digest[:12])
                    captured_response_urls.add(response_url)
                    return
                saved_content_digests.add(digest)

                path = dest_dir / f"FLOW_IMG_{timestamp}_{len(new_saved_paths)}{ext}"
                path.write_bytes(body)
                captured_response_urls.add(response_url)
                new_saved_paths.append(path)
                log.info("📸 RESPONSE HANDLER: salvata %s (%d byte, digest=%s, url=%s)", path.name, len(body), digest[:12], response_url[:120])
            except Exception as e:
                log.warning("Failed to capture Flow image response url=%s err=%s", response_url, e)

        page.on("response", handle_response)

        try:
            prompt_locator = None
            for selector in self.PROMPT_SELECTORS:
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
                            selector_report["attempts"].append({
                                "stage": "generated_image",
                                "selector": selector,
                                "matched": True,
                                "elapsed_ms": 0,
                                "src": src,
                            })
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
                        raw_data = await self._download_direct_url_raw(absolute_src, referer=page.url, cookie_header=cookie_header)
                        if raw_data is None:
                            log.warning("Download immagine Flow fallito: %s", absolute_src)
                            continue
                        # Dedup basato sul contenuto (cross-meccanismo)
                        digest = _compute_digest(raw_data)
                        if digest in saved_content_digests:
                            log.info("✅ POLLING: contenuto già salvato (digest=%s), saltato", digest[:12])
                            continue
                        saved_content_digests.add(digest)
                        path.write_bytes(raw_data)
                        new_saved_paths.append(path)
                        log.info("📸 Nuova immagine Flow salvata: %s (%d byte, digest=%s)", path, len(raw_data), digest[:12])
                else:
                    if seen_any_image:
                        idle_rounds += 1
                        if idle_rounds >= 3:
                            break

                await asyncio.sleep(5)

            return new_saved_paths
        except Exception as e:
            log.error(f"Errore generazione ImageFX Flow: {e}")
            selector_report["result"] = "failed"
            selector_report["error"] = str(e)
            return []
        finally:
            selector_report.setdefault("result", "empty" if not new_saved_paths else "success")
            _append_selector_report(selector_report)
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

    @staticmethod
    async def _download_direct_url_raw(
        image_url: str,
        referer: str | None = None,
        cookie_header: str | None = None,
    ) -> bytes | None:
        """Scarica i byte grezzi di un'immagine. Usato per dedup basato sul contenuto."""
        import urllib.request
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
                return response.read()
        except Exception as e:
            log.warning("Failed to download Flow image raw url=%s err=%s", image_url, e)
            return None


async def generate_flow_images(prompt: str, project_id: str = None, style: str = None, account: str = None, headless: bool = True):
    async with ImageFXFlowAutomation(account=account, headless=headless) as engine:
        results = await engine.generate_images(prompt, project_id, style)
        return [str(p) for p in results]
