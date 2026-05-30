import asyncio
import os
import random
import re
import subprocess
import time
from pathlib import Path
from playwright.async_api import Page

from .base import (
    DOWNLOAD_DIR,
    BaseAutomation,
    _append_selector_report,
    human_delay,
    log,
)

class GoogleVidsVideoMixin:
    """Mixin class for Google Vids video generation logic."""

    async def _poll_and_download_video(self, page: Page, video_id: str, timeout_ms: int = 900000, zoom_centered: bool = True) -> Path | None:
        """Shared logic to poll for a generated video preview and download it."""
        from .base import DOWNLOAD_DIR, _append_selector_report
        dest_dir = DOWNLOAD_DIR / "videos"
        dest_dir.mkdir(parents=True, exist_ok=True)
        
        poll_interval_ms = 5000
        started_at = time.time()
        video_src = None
        selected_idx = -1
        
        while (time.time() - started_at) * 1000 < timeout_ms:
            try:
                Path("logs").mkdir(exist_ok=True)
                await page.screenshot(path="logs/poll_current.png")
            except Exception as e:
                log.warning("Failed to take polling screenshot: %s", e)
            await page.wait_for_timeout(poll_interval_ms)
            
            # Find preview container
            container_loc = None
            for selector in self.PREVIEW_CONTAINER_SELECTORS:
                loc = page.locator(selector)
                if await loc.count() > 0:
                    try:
                        if await loc.first.locator("video").count() > 0:
                            container_loc = loc.first
                            break
                    except Exception: continue

            candidates = container_loc.locator("video") if container_loc else page.locator("video")
            count = await candidates.count()
            for idx in range(count):
                loc = candidates.nth(idx)
                try:
                    src = await loc.evaluate("(el) => el.currentSrc || el.src || el.getAttribute('src') || ''")
                    if src and "inspirationgallery" not in src:
                        video_src = src
                        selected_idx = idx
                        break
                except Exception: continue
            
            if video_src: break
            log.info("Polling video preview... %.1fs", time.time() - started_at)

        if not video_src:
            return None

        raw_path = dest_dir / f"raw_{int(time.time())}.mp4"
        cookie_header = await self._build_cookie_header(video_src)
        downloaded = await self._download_direct_url(video_src, raw_path, referer=page.url, cookie_header=cookie_header)
        if not downloaded:
            async with page.expect_download(timeout=300000) as download_info:
                await page.evaluate(f"window.location.href = '{video_src}'")
            download = await download_info.value
            await download.save_as(str(raw_path))

        final_path = dest_dir / f"VIDEO_YT_{int(time.time())}.mp4"
        vf = "crop=in_w*0.9:in_h*0.9:(in_w-out_w)/2:(in_h-out_h)/2,scale=1920:1080" if zoom_centered else "scale=1920:1080"
        cmd = ['ffmpeg', '-y', '-i', str(raw_path), '-vf', vf, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', str(final_path)]
        
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode == 0:
            raw_path.unlink()
            return final_path
        return raw_path

    async def generate_character_video(self, video_id: str, character_id: str, prompt: str = "youtuber talking and gesturing while looking at camera") -> Path | None:
        """Generates an AI video using a character's reference image and moves it to their Drive folder."""
        from storage import get_project_id, save_project_id, get_character
        from drive_client import download_file, _build_service
        from googleapiclient.http import MediaFileUpload
        from drive_client import upload_file_to_drive

        char = get_character(character_id)
        if not char: return None
        image_drive_id = char.get("image_drive_id")
        video_folder_id = char.get("metadata", {}).get("video_folder_id")
        if not image_drive_id: return None

        temp_img_path = await download_file(image_drive_id, f"ref_{character_id}.png", "image")
        
        if not video_id or video_id == "new":
            video_id = get_project_id("vids") or "new"

        if video_id == "new":
            page = await self.context.new_page()
            await page.goto("https://docs.google.com/videos/create", wait_until="domcontentloaded")
            await asyncio.sleep(8)
            await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
            video_id = self._extract_vid_id(page.url)
            save_project_id("vids", video_id)
        else:
            page = await self._get_page(video_id)
            try:
                Path("logs").mkdir(exist_ok=True)
                await page.screenshot(path="logs/char_step1_open.png")
                log.info("Saved screenshot: logs/char_step1_open.png")
            except Exception as e:
                log.warning("Failed to save step1 screenshot: %s", e)

        try:
            await page.keyboard.press("Escape")
            try:
                await page.locator('#content-library-rail-video-generation-element').click(force=True, timeout=10000)
            except Exception:
                await page.get_by_label("Genera un video clip AI", exact=True).click(force=True, timeout=10000)
            await asyncio.sleep(3)
            try:
                await page.screenshot(path="logs/char_step2_generation_clicked.png")
                log.info("Saved screenshot: logs/char_step2_generation_clicked.png")
            except Exception as e:
                log.warning("Failed to save step2 screenshot: %s", e)

            # Upload Reference Image
            # Using robust selector found via inspection
            upload_selectors = [
                ".videoGenCreationViewFileInputsInputSelectButton",
                "button[aria-label='Ingredienti']",
                "/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[2]/div[4]/div[2]"
            ]
            
            upload_btn = None
            for sel in upload_selectors:
                loc = page.locator(sel if not sel.startswith("/") else f"xpath={sel}").first
                if await loc.count() > 0:
                    upload_btn = loc
                    break
            
            if not upload_btn:
                raise RuntimeError("Reference image upload button not found")

            log.info("Clicking reference image upload button...")
            async with page.expect_file_chooser() as fc_info:
                await upload_btn.click()
            file_chooser = await fc_info.value
            await file_chooser.set_files(temp_img_path)
            log.info("Reference image uploaded.")
            try:
                await page.screenshot(path="logs/char_step3_image_uploaded.png")
                log.info("Saved screenshot: logs/char_step3_image_uploaded.png")
            except Exception as e:
                log.warning("Failed to save step3 screenshot: %s", e)
            await asyncio.sleep(5)

            # Prompt
            input_loc = page.locator('textarea, [contenteditable="true"], div[role="textbox"]').first
            await input_loc.fill(prompt)
            try:
                await page.screenshot(path="logs/char_step4_prompt_filled.png")
                log.info("Saved screenshot: logs/char_step4_prompt_filled.png")
            except Exception as e:
                log.warning("Failed to save step4 screenshot: %s", e)
            
            # Generate
            await page.locator('button:has-text("Genera"), button:has-text("Generate")').first.click()
            log.info("Character video generation started for %s", character_id)
            try:
                await page.screenshot(path="logs/char_step5_generate_clicked.png")
                log.info("Saved screenshot: logs/char_step5_generate_clicked.png")
            except Exception as e:
                log.warning("Failed to save step5 screenshot: %s", e)

            final_path = await self._poll_and_download_video(page, video_id)
            
            if final_path and video_folder_id:
                log.info("Uploading generated video to character folder: %s", video_folder_id)
                upload_file_to_drive(video_folder_id, final_path, final_path.name, "video/mp4")
                
            return final_path

        finally:
            if temp_img_path.exists(): temp_img_path.unlink()
            await page.close()

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
            page = await self.context.new_page()
            try:
                # Navigate directly to the Vids create URL (bypass FAB click)
                await page.goto("https://docs.google.com/videos/create", wait_until="domcontentloaded")
                await asyncio.sleep(8)
                # Wait for redirect to /videos/d/ID/edit
                try:
                    await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
                except Exception:
                    log.info("wait_for_url failed, current url=%s, page title=%s", page.url, await page.title())
                    body = await page.locator("body").inner_text()
                    log.info("Page body (first 2000 chars): %s", body[:2000])
                    raise
                video_id = self._extract_vid_id(page.url)
                if video_id:
                    log.info("New Vids project created: %s, saving to cache.", video_id)
                    save_project_id("vids", video_id)
            except Exception as e:
                log.error("Failed to create new Vids project: %s", e)
                video_id = self._extract_vid_id(page.url)
                if not video_id:
                    await page.close()
                    raise
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
                Path("logs").mkdir(exist_ok=True)
                await page.screenshot(path="logs/vids_step1_open.png")
                log.info("Saved screenshot: logs/vids_step1_open.png")
            except Exception as e:
                log.warning("Failed to save step1 screenshot: %s", e)

        try:
            generation_timeout_ms = 900000
            poll_interval_ms = 5000
            log.info("Starting Vids generation for video_id=%s (timeout=%sms)", video_id, generation_timeout_ms)
            await human_delay(1500, 4000)
            # Dismiss any modal/dialog that might be blocking clicks
            for attempt in range(3):
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
                        '[data-view-id*="getting-started"] button, [data-view-id*="dialog"] button'
                    )
                    if await btns.count() > 0:
                        await btns.first.click(force=True, timeout=3000)
                        await asyncio.sleep(0.5)
                except Exception:
                    pass
            # Dismiss "Getting Started" dialog specifically
            try:
                gs = page.locator('[data-view-id*="getting-started"]')
                if await gs.count() > 0:
                    log.info("Dismissing Getting Started dialog")
                    await page.keyboard.press("Escape")
                    await asyncio.sleep(1)
                    # Click outside the dialog to dismiss
                    await page.mouse.click(10, 10)
                    await asyncio.sleep(1)
            except Exception:
                pass
            try:
                await page.locator('#content-library-rail-video-generation-element').click(force=True, timeout=10000)
                log.info("Opened Veo generation rail for video_id=%s", video_id)
            except Exception:
                await page.get_by_label("Genera un video clip AI", exact=True).click(force=True, timeout=10000)
                log.info("Opened Veo generation rail via force-click for video_id=%s", video_id)
            await human_delay(1000, 2500)
            try:
                await page.screenshot(path="logs/vids_step2_veo_opened.png")
                log.info("Saved screenshot: logs/vids_step2_veo_opened.png")
            except Exception as e:
                log.warning("Failed to save step2 screenshot: %s", e)
            
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
                log.info("Prompt input not found, dumping selector inventory for debugging")
                await self._selector_inventory(page, [
                    'textarea', '[contenteditable]', 'div[role="textbox"]',
                    'input[type="text"]', 'input', '[placeholder]',
                    '[aria-label*="video"]', '[aria-label*="prompt"]',
                    '[aria-label*="Descrivi"]', '[aria-label*="descri"]',
                ])
                raise RuntimeError("Prompt input not found in Google Vids UI.")
            
            # Digitazione umana
            await input_loc.click(force=True)
            await input_loc.type(prompt, delay=random.randint(50, 150))
            await human_delay(800, 2000)
            try:
                await page.screenshot(path="logs/vids_step3_prompt_filled.png")
                log.info("Saved screenshot: logs/vids_step3_prompt_filled.png")
            except Exception as e:
                log.warning("Failed to save step3 screenshot: %s", e)
            
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
                    await loc.first.click(force=True, timeout=10000)
                    clicked = True
                    selector_report["attempts"].append({
                        "stage": "generate_button_selected",
                        "selector": selector,
                        "matched": True,
                        "elapsed_ms": 0,
                    })
                    log.info("Clicked generate button via selector=%s for video_id=%s", selector, video_id)
                    try:
                        await page.screenshot(path="logs/vids_step4_generate_clicked.png")
                        log.info("Saved screenshot: logs/vids_step4_generate_clicked.png")
                    except Exception as e:
                        log.warning("Failed to save step4 screenshot: %s", e)
                    break
            if not clicked:
                raise RuntimeError("Generate button not found in Google Vids UI.")

            return await self._poll_and_download_video(page, video_id, generation_timeout_ms, zoom_centered)

        except Exception as e:
            log.error("Errore generazione video for video_id=%s: %s", video_id, e, exc_info=True)
            selector_report["result"] = "failed"
            selector_report["error"] = str(e)
            return None
        finally:
            selector_report.setdefault("result", "success")
            _append_selector_report(selector_report)
            await page.close()
