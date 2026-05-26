import asyncio
import re
import time
from pathlib import Path

from drive_client import download_file, list_exported_files
from .base import (
    BaseAutomation,
    log,
)

class GoogleVidsAvatarMixin:
    """Mixin class for Google Vids Lip Sync Avatar generation logic."""

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
