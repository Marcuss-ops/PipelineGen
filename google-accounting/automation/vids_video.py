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
