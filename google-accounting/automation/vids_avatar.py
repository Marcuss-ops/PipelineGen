import asyncio
import random
import re
import time
from pathlib import Path

from drive_client import download_file, list_exported_files
from reupload_drive_assets import upload_file_to_drive
from .base import (
    BaseAutomation,
    _append_selector_report,
    human_delay,
    log,
)


class GoogleVidsAvatarMixin:
    """Mixin class for Google Vids Avatar lip-sync generation using Veo rail with reference image."""

    async def _avatar_selector_inventory(self, page) -> None:
        """Dump all potentially relevant selectors for debugging avatar issues."""
        interesting = [
            "#content-library-rail-video-generation-element",
            "#content-library-rail-avatars-element",
            'div[aria-label="Avatar AI"]',
            "div[role='radio']",
            "video",
            "button",
            "textarea",
            'div[role="textbox"]',
            '[class*="VideoGeneration"]',
            '[class*="videoGenCreation"]',
        ]
        for selector in interesting:
            loc = page.locator(selector)
            try:
                count = await loc.count()
            except Exception:
                continue
            if count == 0:
                continue
            for idx in range(min(count, 8)):
                item = loc.nth(idx)
                try:
                    tag = await item.evaluate("(el) => el.tagName.toLowerCase()")
                    el_id = await item.get_attribute("id")
                    cls = await item.get_attribute("class")
                    aria = await item.get_attribute("aria-label")
                    role = await item.get_attribute("role")
                    text = ""
                    try:
                        text = (await item.inner_text())[:160]
                    except Exception:
                        text = ""
                    log.info(
                        "Avatar inventory selector=%s idx=%d tag=%s id=%s class=%s aria=%s role=%s text=%s",
                        selector, idx, tag, el_id or "", (cls or "")[:120], aria or "",
                        role or "", text.replace("\n", " ") if text else "",
                    )
                except Exception as exc:
                    log.debug("Avatar inventory failed selector=%s idx=%d err=%s", selector, idx, exc)

    async def _find_first(self, page, selectors: list[str], timeout: int = 5000):
        """Try each selector and return the first that matches."""
        for selector in selectors:
            loc = page.locator(selector).first
            try:
                if await loc.count() > 0:
                    return loc
            except Exception:
                continue
        return None

    async def _dismiss_dialogs(self, page):
        """Dismiss any dialogs or modals that might block interaction."""
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
                    await btns.first.click(force=True, timeout=3000)
                    await asyncio.sleep(0.5)
            except Exception:
                pass
        try:
            gs = page.locator('[data-view-id*="getting-started"]')
            if await gs.count() > 0:
                log.info("Dismissing Getting Started dialog")
                await page.keyboard.press("Escape")
                await asyncio.sleep(1)
                await page.mouse.click(10, 10)
                await asyncio.sleep(1)
        except Exception:
            pass

    async def generate_avatar(self, video_id: str, script: str, avatar_id: str = "James") -> Path | None:
        """Generates an AI Talking Head video using character reference image via Veo rail."""
        from storage import (
            get_project_id, save_project_id, get_structured_path,
            save_media_asset, save_generation_metadata, get_character,
        )

        char = get_character(avatar_id)
        if not char:
            log.error("Character '%s' not found in database", avatar_id)
            return None
        image_drive_id = char.get("image_drive_id")
        video_folder_id = char.get("metadata", {}).get("video_folder_id")
        if not image_drive_id:
            log.error("Character '%s' has no image_drive_id", avatar_id)
            return None

        temp_img_path = await download_file(image_drive_id, f"ref_{avatar_id}.png", "image")
        dest_folder = get_structured_path(media_type="avatar", style="ai", sub_style=avatar_id)

        if not video_id or video_id == "new":
            video_id = get_project_id("vids")
            video_id = video_id if video_id else "new"

        selector_report = {
            "kind": "vids_avatar",
            "video_id": video_id,
            "avatar_id": avatar_id,
            "script_preview": script[:120],
            "attempts": [],
            "generated_at": int(time.time()),
        }

        page = await self._goto_home()
        try:
            if video_id == "new" or not video_id:
                log.info("Creating new Vids project for Avatar")
                await page.click('[aria-label="Crea nuovo video"]', timeout=15000)
                await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
                video_id = self._extract_vid_id(page.url)
                if video_id:
                    save_project_id("vids", video_id)
            else:
                log.info("Opening existing Vids project: %s", video_id)
                await page.goto(
                    f"https://docs.google.com/videos/d/{video_id}/edit",
                    wait_until="domcontentloaded",
                )

            log.info("Waiting for editor to stabilize...")
            await asyncio.sleep(15)
            await self._dismiss_dialogs(page)

            # Open Veo generation rail
            try:
                await page.locator(
                    '#content-library-rail-video-generation-element'
                ).click(force=True, timeout=10000)
            except Exception:
                await page.get_by_label(
                    "Genera un video clip AI", exact=True
                ).click(force=True, timeout=10000)
            await asyncio.sleep(3)
            selector_report["attempts"].append({"stage": "open_veo_rail", "found": True})

            # Upload reference image
            upload_selectors = [
                ".videoGenCreationViewFileInputsInputSelectButton",
                "button[aria-label='Ingredienti']",
            ]
            upload_btn = None
            for sel in upload_selectors:
                loc = page.locator(sel).first
                if await loc.count() > 0:
                    upload_btn = loc
                    break
            if not upload_btn:
                raise RuntimeError("Reference image upload button not found")
            selector_report["attempts"].append({"stage": "upload_btn", "found": True})

            log.info("Uploading reference image...")
            async with page.expect_file_chooser() as fc_info:
                await upload_btn.click()
            file_chooser = await fc_info.value
            await file_chooser.set_files(temp_img_path)
            log.info("Reference image uploaded.")
            await asyncio.sleep(5)

            # Fill prompt with script text
            input_loc = page.locator(
                'textarea, [contenteditable="true"], div[role="textbox"]'
            ).first
            await input_loc.fill(script)
            await asyncio.sleep(2)
            selector_report["attempts"].append({"stage": "fill_script", "found": True})

            # Generate
            generate_btn = page.locator(
                'button:has-text("Genera"), button:has-text("Generate")'
            ).first
            await generate_btn.click()
            log.info("Avatar generation started for %s", avatar_id)
            selector_report["attempts"].append({"stage": "generate", "found": True})

            # Poll and download generated video
            final_path = await self._poll_and_download_video(page, video_id)

            if final_path and video_folder_id:
                log.info("Uploading generated avatar video to character folder: %s", video_folder_id)
                upload_file_to_drive(video_folder_id, final_path, final_path.name, "video/mp4")

            if final_path:
                metadata = {
                    "script": script,
                    "avatar_id": avatar_id,
                    "video_id": video_id,
                    "timestamp": int(time.time()),
                }
                save_generation_metadata(dest_folder, metadata)
                save_media_asset(
                    final_path, "GOOGLE_VIDS_AVATAR", final_path.name,
                    "video", "ai_avatar", avatar_id, script, video_id, metadata,
                )
                log.info("Avatar video saved: %s", final_path)

            return final_path

        except Exception as e:
            log.error("Errore generazione avatar: %s", e, exc_info=True)
            selector_report["result"] = "failed"
            selector_report["error"] = str(e)
            return None
        finally:
            selector_report.setdefault("result", "success")
            _append_selector_report(selector_report)
            if temp_img_path.exists():
                temp_img_path.unlink()
            await page.close()


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

    if paths:
        paths.sort(key=lambda p: p.stat().st_mtime, reverse=True)
        log.info("sync_project: returning newest file: %s", paths[0])
        return [paths[0]]

    return paths
