import asyncio
import hashlib
import logging
import os
import random
import re
import sys
import time
import urllib.parse
import urllib.request
from datetime import datetime
from pathlib import Path

from playwright.async_api import Page

from .base import (
    DOWNLOAD_DIR,
    BaseAutomation,
    _append_selector_report,
    human_delay,
    human_scroll,
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

log = logging.getLogger("AutomationEngine")
if not log.handlers:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s: %(message)s"))
    log.addHandler(handler)
    log.setLevel(logging.INFO)

class ImageFXFlowAutomation(BaseAutomation):
    """Engine per l'automazione di Google Labs ImageFX Flow."""

    PROMPT_SELECTORS = [
        'div[role="textbox"]',
        'div[contenteditable="true"]',
        'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[1]/div/p',
    ]

    GENERATE_BUTTON_SELECTORS = [
        'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div/div/div[2]/div[2]/button[2]',
        'button:has-text("Crea")',
        'button:has-text("Generate")',
        'button[aria-label*="genera" i]',
    ]

    async def generate_images(self, prompt: str, project_id: str = None, style: str = None) -> list[Path]:
        from storage import (
            get_project_id, 
            get_structured_path, 
            save_generation_metadata, 
            save_media_asset
        )
        
        # Always use a new project session to avoid project history and duplicate asset extraction
        project_id = None

        # Split style for structure
        main_style = "default"
        sub_style = "general"
        if style:
            parts = style.split(maxsplit=1)
            main_style = parts[0]
            if len(parts) > 1:
                sub_style = parts[1]
        
        dest_dir = get_structured_path(main_style, sub_style)
        log.info(f"Cartella di destinazione strutturata: {dest_dir}")
        
        debug_dir = DOWNLOAD_DIR / "debug"
        debug_dir.mkdir(parents=True, exist_ok=True)
        
        if style:
            full_prompt = f"style {style} {prompt}"
            log.info(f"Prompt finale: {full_prompt}")
        else:
            full_prompt = prompt
        
        url = f"https://labs.google/fx/it/tools/flow/project/{project_id}" if project_id else "https://labs.google/fx/it/tools/flow"
            
        page = await self.context.new_page()
        
        new_saved_paths = []
        captured_response_urls: set[str] = set()
        saved_content_digests: set[str] = set()
        pre_existing_urls: set[str] = set()
        can_capture = False

        # Blacklist pre-existing images in the DOM to avoid capturing/downloading them
        try:
            existing_imgs = await page.locator('img[src*="googleusercontent.com"]').all()
            for img in existing_imgs:
                src = await img.get_attribute("src")
                if src:
                    captured_response_urls.add(src)
                    log.info("Blacklisted pre-existing image URL in DOM: %s", src[:120])
        except Exception as e:
            log.warning("Failed to blacklist existing images: %s", e)

        def _compute_digest(data: bytes) -> str:
            return hashlib.md5(data).hexdigest()

        async def handle_response(response):
            nonlocal new_saved_paths
            response_url = response.url
            
            if not can_capture:
                pre_existing_urls.add(response_url)
                return
            
            if response_url in pre_existing_urls:
                return
            if response_url in captured_response_urls:
                return
                
            content_type = response.headers.get("content-type", "")
            is_image = "image/" in content_type
            is_redirect = "media.getMediaUrlRedirect" in response_url
            
            if not (is_image or is_redirect):
                return

            try:
                if is_redirect:
                    log.info("📸 Intercettato REDIRECT immagine: %s", response_url[:120])
                    return

                if not response.ok:
                    return
                body = await response.body()
                if not body or len(body) < 1000:
                    return
                digest = _compute_digest(body)
                if digest in saved_content_digests:
                    return
                saved_content_digests.add(digest)
                ext = ".png" if "png" in content_type else ".jpg"
                filename = f"FLOW_{int(time.time())}_{len(new_saved_paths)}{ext}"
                path = dest_dir / filename
                path.write_bytes(body)
                captured_response_urls.add(response_url)
                new_saved_paths.append(path)
                log.info("📸 Salvata immagine via Network: %s", path.name)
                
                # Salva nel DB
                save_media_asset(
                    file_path=path,
                    source="google_flow",
                    name=filename,
                    style=main_style,
                    sub_style=sub_style,
                    prompt=prompt,
                    project_id=project_id,
                    metadata={"full_prompt": full_prompt, "style": style}
                )
            except Exception as e:
                log.debug(f"Errore handle_response: {e}")

        page.on("response", handle_response)
        
        log.info(f"Navigazione verso: {url}")
        await page.goto(url)
        await asyncio.sleep(15)

        # Se siamo sulla dashboard con il pulsante "+ Nuovo progetto", clicchiamolo per iniziare un nuovo progetto
        for btn_text in ["Nuovo progetto", "New project", "Nuovo", "New"]:
            loc = page.get_by_text(btn_text, exact=False).first
            if await loc.count() > 0:
                log.info(f"Rilevata dashboard di Flow, clic su '{btn_text}'...")
                await loc.click()
                await asyncio.sleep(5)
                break

        try:
            prompt_locator = None
            for selector in self.PROMPT_SELECTORS:
                loc = page.locator(selector).first
                if await loc.count() > 0:
                    prompt_locator = loc
                    log.info(f"Found Flow prompt: {selector}")
                    break

            if prompt_locator is None:
                debug_path = debug_dir / f"FAIL_{int(time.time())}.png"
                await page.screenshot(path=str(debug_path))
                raise RuntimeError(f"Prompt field not found. Screenshot: {debug_path}")

            log.info("Inserimento prompt...")
            await prompt_locator.click()
            await asyncio.sleep(1)
            await page.keyboard.press("Control+A")
            await page.keyboard.press("Backspace")
            await page.keyboard.type(full_prompt, delay=80)
            await asyncio.sleep(1)
            await page.keyboard.press("Enter")
            log.info("Prompt digitato e confermato con Enter.")

            # === Imposta il numero di immagini a 4 (massimo) con RETRY ===
            try:
                for attempt in range(3):
                    toggle = page.locator('button[id^="radix-"]:has-text("x1"), button[id^="radix-"]:has-text("x2"), button[id^="radix-"]:has-text("x3"), button:has-text("x1"), button:has-text("x2"), button:has-text("x3")').first
                    if await toggle.count() > 0:
                        current_text = await toggle.inner_text()
                        if "x4" in current_text:
                            log.info("Numero immagini già impostato a 4.")
                            break
                            
                        log.info(f"Tentativo {attempt+1}: Apertura menu conteggio (attuale: {current_text.strip()})...")
                        await toggle.click()
                        await asyncio.sleep(2)
                        
                        four_btn = page.locator('button[id$="trigger-4"], button[role="tab"]:has-text("x4"), button:has-text("x4")').first
                        if await four_btn.count() > 0:
                            await four_btn.click(force=True)
                            await asyncio.sleep(2)
                            
                            new_text = await toggle.inner_text()
                            if "x4" in new_text:
                                log.info("Impostato numero immagini a 4 (x4).")
                                break
                            else:
                                log.warning(f"Selezione x4 non riuscita (testo: {new_text.strip()}), riprovo...")
                        else:
                            log.warning("Pulsante x4 non trovato nel menu.")
                            # Forza chiusura menu se bloccato
                            await page.keyboard.press("Escape")
                            await asyncio.sleep(1)
                    else:
                        log.debug("Toggle conteggio non trovato in questo step.")
                        break
            except Exception as e:
                log.debug(f"Errore critico impostazione numero immagini: {e}")

            await asyncio.sleep(1)

            generate_locator = None
            for selector in self.GENERATE_BUTTON_SELECTORS:
                loc = page.locator(selector).first
                if await loc.count() > 0:
                    generate_locator = loc
                    log.info(f"Found generate button: {selector}")
                    break
            
            can_capture = True
            if generate_locator:
                await generate_locator.click(force=True)
                log.info("Pulsante Genera cliccato (force=True).")
            else:
                log.warning("Pulsante Genera non trovato, confido nell'Enter già premuto.")

            # Screenshot di verifica post-clic
            await page.screenshot(path=str(debug_dir / f"GEN_START_{int(time.time())}.png"))

            # Attesa cattura immagini
            log.info("In attesa delle immagini (60s)...")
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                if len(new_saved_paths) >= 4: break 
                
                imgs = await page.locator('img[src*="googleusercontent.com"]').all()
                for img in imgs:
                    src = await img.get_attribute("src")
                    if src and src not in captured_response_urls:
                        try:
                            log.info("🔍 Trovata immagine nel DOM, provo download diretto...")
                            resp = await page.request.get(src)
                            if resp.ok:
                                body = await resp.body()
                                if len(body) > 2000:
                                    digest = _compute_digest(body)
                                    if digest not in saved_content_digests:
                                        saved_content_digests.add(digest)
                                        captured_response_urls.add(src)
                                        filename = f"FLOW_DOM_{int(time.time())}_{len(new_saved_paths)}.jpg"
                                        path = dest_dir / filename
                                        path.write_bytes(body)
                                        new_saved_paths.append(path)
                                        log.info("📸 Salvata immagine via DOM: %s", path.name)
                                        
                                        # Salva nel DB
                                        save_media_asset(
                                            file_path=path,
                                            source="google_flow",
                                            name=filename,
                                            style=main_style,
                                            sub_style=sub_style,
                                            prompt=prompt,
                                            project_id=project_id,
                                            metadata={"full_prompt": full_prompt, "style": style, "method": "DOM"}
                                        )
                        except: pass
                
                await asyncio.sleep(5)

            # Salva metadata.json finale
            save_generation_metadata(dest_dir, {
                "generation_id": dest_dir.name,
                "timestamp": datetime.now().isoformat(),
                "style": {"main": main_style, "sub": sub_style},
                "prompt": prompt,
                "full_prompt": full_prompt,
                "project_id": project_id,
                "assets": [p.name for p in new_saved_paths]
            })

            return new_saved_paths
        except Exception as e:
            log.error(f"Errore: {e}")
            return []
        finally:
            await page.close()

async def generate_flow_images(prompt: str, project_id: str = None, style: str = None, account: str = None, headless: bool = True):
    async with ImageFXFlowAutomation(account=account, headless=headless) as engine:
        results = await engine.generate_images(prompt, project_id, style)
        return [str(p) for p in results]
