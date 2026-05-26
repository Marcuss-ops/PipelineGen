"""
Modulo di cattura immagini per Google Flow.

Responsabile di:
- Ascoltare le risposte di rete (network listener)
- Polling del DOM per immagini googleusercontent.com
- Salvare immagini su disco
- Salvare metadati nel DB

Basato sulla versione STABILE funzionante (commit c10c6f5).
"""
import hashlib
import logging
import time
from pathlib import Path

from playwright.async_api import Page

from .config import (
    DOM_POLL_INTERVAL,
    IMAGE_DOM_PATTERN,
    IMAGE_WAIT_TIMEOUT,
    MAX_IMAGES,
    MEDIA_REDIRECT_PATTERN,
    MIN_DOM_IMAGE_BYTES,
)

log = logging.getLogger("AutomationEngine.FlowCapture")


class ImageCapturer:
    """
    Gestisce la cattura delle immagini generate da Google Flow.
    
    Usa DUE meccanismi in parallelo:
    1. Network listener (handle_response) — cattura le immagini via risposte HTTP
    2. DOM polling — cattura immagini dal DOM come fallback
    """

    def __init__(self, page: Page, dest_dir: Path, main_style: str, sub_style: str,
                 prompt: str, style: str, full_prompt: str, project_id: str = None):
        self.page = page
        self.dest_dir = dest_dir
        self.main_style = main_style
        self.sub_style = sub_style
        self.prompt = prompt
        self.style = style
        self.full_prompt = full_prompt
        self.project_id = project_id

        # Stato interno
        self.new_saved_paths: list[Path] = []
        self.captured_response_urls: set[str] = set()
        self.saved_content_digests: set[str] = set()
        self.pre_existing_urls: set[str] = set()
        self.can_capture = False

        # Log iniziale
        log.info(f"ImageCapturer inizializzato → {dest_dir}")
        log.info(f"  main_style={main_style}, sub_style={sub_style}")
        log.info(f"  prompt='{prompt}'")

    def _compute_digest(self, data: bytes) -> str:
        return hashlib.md5(data).hexdigest()

    async def blacklist_existing_images(self):
        """Blacklist immagini già presenti nel DOM all'avvio."""
        try:
            existing_imgs = await self.page.locator(f'img[src*="{IMAGE_DOM_PATTERN}"]').all()
            count = 0
            for img in existing_imgs:
                src = await img.get_attribute("src")
                if src:
                    self.captured_response_urls.add(src)
                    count += 1
                    log.info(f"  Blacklisted DOM img #{count}: {src[:120]}")
            log.info(f"Blacklisted {count} immagini pre-esistenti nel DOM")
        except Exception as e:
            log.warning(f"Errore blacklist immagini DOM: {e}")

    def make_network_handler(self):
        """
        Crea e restituisce la callback per page.on("response").
        Cattura immagini via risposte HTTP di rete.
        """
        async def handle_response(response):
            response_url = response.url

            # STEP 1: Pre-capture — accumula URL per deduplicazione
            if not self.can_capture:
                self.pre_existing_urls.add(response_url)
                if "image" in response.headers.get("content-type", ""):
                    log.debug(f"  [pre-capture] URL immagine: {response_url[:120]}")
                return

            # STEP 2: Deduplicazione
            if response_url in self.pre_existing_urls:
                log.debug(f"  [SKIP] pre-existing: {response_url[:80]}")
                return
            if response_url in self.captured_response_urls:
                log.debug(f"  [SKIP] già catturato: {response_url[:80]}")
                return

            # STEP 3: Filtra per content-type
            content_type = response.headers.get("content-type", "")
            is_image = "image/" in content_type
            is_redirect = MEDIA_REDIRECT_PATTERN in response_url

            if not (is_image or is_redirect):
                return

            log.info(f"📸 NETWORK EVENT: content_type={content_type} url={response_url[:150]}")

            if is_redirect:
                log.info(f"  → Redirect immagine (salta): {response_url[:120]}")
                return

            # STEP 4: Scarica e salva
            try:
                if not response.ok:
                    log.warning(f"  → Response NOT OK: {response.status}")
                    return

                body = await response.body()
                if not body:
                    log.warning(f"  → Body vuoto")
                    return

                log.info(f"  → Body size: {len(body)} bytes, content_type: {content_type}")

                if len(body) < MIN_DOM_IMAGE_BYTES:
                    log.info(f"  → Body troppo piccolo (< {MIN_DOM_IMAGE_BYTES}), salto")
                    return

                digest = self._compute_digest(body)
                if digest in self.saved_content_digests:
                    log.info(f"  → Digest già salvato, salto")
                    return

                self.saved_content_digests.add(digest)
                ext = ".png" if "png" in content_type else ".jpg"
                filename = f"FLOW_{int(time.time())}_{len(self.new_saved_paths)}{ext}"
                path = self.dest_dir / filename
                path.write_bytes(body)

                self.captured_response_urls.add(response_url)
                self.new_saved_paths.append(path)
                log.info(f"📸 ✅ Salvata immagine via Network: {path.name} ({len(body)} bytes)")

                # Salva nel DB
                await self._save_to_db(path, filename, "network")

            except Exception as e:
                log.debug(f"Errore handle_response: {e}")

        return handle_response

    async def poll_dom_for_images(self):
        """
        Polling del DOM per immagini con src contenente 'googleusercontent.com'.
        Fallback quando il network listener non cattura immagini.
        """
        log.info("Avvio polling DOM per immagini...")
        deadline = time.monotonic() + IMAGE_WAIT_TIMEOUT
        
        while time.monotonic() < deadline:
            if len(self.new_saved_paths) >= MAX_IMAGES:
                log.info(f"Raggiunto limite di {MAX_IMAGES} immagini, fermo polling DOM")
                break

            imgs = await self.page.locator(f'img[src*="{IMAGE_DOM_PATTERN}"]').all()
            found_new = 0
            
            for img in imgs:
                src = await img.get_attribute("src")
                if not src or src in self.captured_response_urls:
                    continue

                try:
                    log.info(f"🔍 DOM new img: {src[:120]}...")
                    resp = await self.page.request.get(src)
                    
                    if not resp.ok:
                        log.warning(f"  → GET fallito: {resp.status}")
                        continue

                    body = await resp.body()
                    log.info(f"  → Body size: {len(body)} bytes")

                    if len(body) < MIN_DOM_IMAGE_BYTES:
                        log.info(f"  → Body troppo piccolo (< {MIN_DOM_IMAGE_BYTES}), salto")
                        self.captured_response_urls.add(src)
                        continue

                    digest = self._compute_digest(body)
                    if digest in self.saved_content_digests:
                        log.info(f"  → Digest duplicato, salto")
                        self.captured_response_urls.add(src)
                        continue

                    self.saved_content_digests.add(digest)
                    self.captured_response_urls.add(src)

                    filename = f"FLOW_DOM_{int(time.time())}_{len(self.new_saved_paths)}.jpg"
                    path = self.dest_dir / filename
                    path.write_bytes(body)
                    self.new_saved_paths.append(path)
                    found_new += 1
                    log.info(f"📸 ✅ Salvata immagine via DOM: {path.name} ({len(body)} bytes)")

                    # Salva nel DB
                    await self._save_to_db(path, filename, "DOM")

                except Exception as e:
                    log.debug(f"Errore download DOM img: {e}")

            if found_new > 0:
                log.info(f"Trovate e salvate {found_new} nuove immagini via DOM")
            else:
                log.debug("Polling DOM: nessuna nuova immagine trovata")

            await asyncio.sleep(DOM_POLL_INTERVAL)

        log.info(f"Polling DOM terminato. Totale immagini catturate: {len(self.new_saved_paths)}")

    async def _save_to_db(self, path: Path, filename: str, method: str):
        """Salva metadati dell'immagine nel database."""
        try:
            from storage import save_media_asset
            save_media_asset(
                file_path=path,
                source="google_flow",
                name=filename,
                style=self.main_style,
                sub_style=self.sub_style,
                prompt=self.prompt,
                project_id=self.project_id,
                metadata={"full_prompt": self.full_prompt, "style": self.style, "method": method}
            )
            log.info(f"  → DB: salvato {filename}")
        except Exception as e:
            log.warning(f"  → DB: errore salvataggio {filename}: {e}")

    def get_results(self) -> list[Path]:
        return self.new_saved_paths


# Import needed by poll_dom_for_images
import asyncio