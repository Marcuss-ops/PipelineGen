"""
Modulo di cattura immagini per Google Flow.

Responsabile di:
- Ascoltare le risposte di rete (network listener)
- Polling del DOM per immagini googleusercontent.com
- Salvare immagini su disco
- Salvare metadati nel DB e Google Drive
"""
import asyncio
import hashlib
import logging
import time
from pathlib import Path

from playwright.async_api import Page

from .config import (
    DOM_POLL_INTERVAL,
    IMAGE_DOM_PATTERN,
    IMAGE_DOM_PATTERN_FALLBACK,
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
        self._pending_tasks = set()

        # Log iniziale
        log.info(f"ImageCapturer inizializzato → {dest_dir}")
        log.info(f"  main_style={main_style}, sub_style={sub_style}")
        log.info(f"  prompt='{prompt}'")

    async def wait_for_uploads(self):
        """Attende che tutti i caricamenti pendenti su Drive/DB siano completati."""
        if self._pending_tasks:
            log.info(f"⏳ In attesa di {len(self._pending_tasks)} caricamenti pendenti...")
            await asyncio.gather(*self._pending_tasks, return_exceptions=True)
            log.info("✅ Tutti i caricamenti completati.")

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
            # STEP 1: Pre-capture check
            if not self.can_capture:
                self.pre_existing_urls.add(response.url)
                return

            # Aggiunge il task di salvataggio al set dei pendenti
            task = asyncio.create_task(self._process_response(response))
            self._pending_tasks.add(task)
            task.add_done_callback(self._pending_tasks.discard)

        return handle_response

    async def _process_response(self, response):
        """Logica interna di processamento risposta separata per essere taskable."""
        response_url = response.url

        # Deduplicazione
        if response_url in self.pre_existing_urls:
            return
        if response_url in self.captured_response_urls:
            return

        # Filtra per content-type
        content_type = response.headers.get("content-type", "")
        is_image = "image/" in content_type
        is_redirect = MEDIA_REDIRECT_PATTERN in response_url
        
        if not (is_image or is_redirect):
            return

        if is_redirect:
            log.debug(f"Ignoro redirect: {response_url}")
            return

        # Scarica e salva
        try:
            if not response.ok:
                log.debug(f"Response not ok ({response.status}): {response_url}")
                return

            body = await response.body()
            if not body:
                log.debug(f"Empty body: {response_url}")
                return
                
            if len(body) < MIN_DOM_IMAGE_BYTES:
                log.debug(f"Body too small ({len(body)} bytes) < {MIN_DOM_IMAGE_BYTES}: {response_url[:100]}...")
                return

            digest = self._compute_digest(body)
            if digest in self.saved_content_digests:
                log.debug(f"Duplicate content (digest {digest}): {response_url[:100]}...")
                return

            self.saved_content_digests.add(digest)
            ext = ".png" if "png" in content_type else ".jpg"
            filename = f"FLOW_{int(time.time())}_{len(self.new_saved_paths)}{ext}"
            path = self.dest_dir / filename
            path.write_bytes(body)

            self.captured_response_urls.add(response_url)
            self.new_saved_paths.append(path)
            log.info(f"📸 ✅ Salvata immagine via Network: {path.name} ({len(body)} bytes)")

            # Salva nel DB e Drive
            await self._save_to_db(path, filename, "network")

        except Exception as e:
            log.debug(f"Errore process_response: {e}")

    async def poll_dom_for_images(self):
        """
        Polling del DOM per immagini con src contenente 'googleusercontent.com' o fallback.
        Gestisce anche i redirect URL relativi di Google Flow.
        """
        log.info("Avvio polling DOM per immagini...")
        deadline = time.monotonic() + IMAGE_WAIT_TIMEOUT
        BASE_URL = "https://labs.google"
        
        while time.monotonic() < deadline:
            if len(self.new_saved_paths) >= MAX_IMAGES:
                log.info(f"Raggiunto limite di {MAX_IMAGES} immagini, fermo polling DOM")
                break

            selectors = ['img']
            
            for selector in selectors:
                imgs = await self.page.locator(selector).all()
                for img in imgs:
                    src = await img.get_attribute("src")
                    if not src:
                        continue
                    
                    # Converte URL relativi in assoluti
                    abs_src = src if src.startswith("http") else f"{BASE_URL}{src}"
                    
                    # Log per debug (solo URL non ancora visti)
                    if abs_src not in self.captured_response_urls and abs_src not in self.pre_existing_urls:
                        log.debug(f"DOM img found: {src[:150]}")

                    # Filtra: accetta googleusercontent, fallback, e redirect di Flow
                    is_flow_redirect = MEDIA_REDIRECT_PATTERN in src
                    is_gusercontent = IMAGE_DOM_PATTERN in src
                    is_fallback = IMAGE_DOM_PATTERN_FALLBACK in src
                    
                    if not (is_gusercontent or is_fallback or is_flow_redirect):
                        continue
                        
                    if abs_src in self.captured_response_urls:
                        continue

                    # Processa come task asincrono
                    task = asyncio.create_task(self._process_dom_image(abs_src))
                    self._pending_tasks.add(task)
                    task.add_done_callback(self._pending_tasks.discard)

            await asyncio.sleep(DOM_POLL_INTERVAL)

        log.info(f"Polling DOM terminato. Totale immagini catturate finora: {len(self.new_saved_paths)}")

    async def _process_dom_image(self, src):
        """Scarica e salva un'immagine trovata nel DOM, seguendo i redirect."""
        try:
            log.info(f"🔍 DOM img detected, downloading: {src[:120]}...")
            
            # Segui i redirect manualmente per ottenere l'URL finale dell'immagine
            resp = await self.page.request.get(src, max_redirects=10)
            
            if not resp.ok:
                log.debug(f"DOM request not ok ({resp.status}): {src[:100]}...")
                return

            # Controlla il content-type per assicurarsi che sia un'immagine
            content_type = resp.headers.get("content-type", "")
            if "image/" not in content_type and "octet-stream" not in content_type:
                log.debug(f"DOM non-image content-type ({content_type}): {src[:100]}...")
                return

            body = await resp.body()
            if not body:
                log.debug(f"DOM empty body: {src[:100]}...")
                return
                
            if len(body) < MIN_DOM_IMAGE_BYTES:
                log.debug(f"DOM body too small ({len(body)} bytes) < {MIN_DOM_IMAGE_BYTES}: {src[:100]}...")
                self.captured_response_urls.add(src)
                return

            digest = self._compute_digest(body)
            if digest in self.saved_content_digests:
                log.debug(f"DOM duplicate content: {src[:100]}...")
                self.captured_response_urls.add(src)
                return

            self.saved_content_digests.add(digest)
            self.captured_response_urls.add(src)

            ext = ".png" if "png" in content_type else ".jpg"
            filename = f"FLOW_DOM_{int(time.time())}_{len(self.new_saved_paths)}{ext}"
            path = self.dest_dir / filename
            path.write_bytes(body)
            self.new_saved_paths.append(path)
            log.info(f"📸 ✅ Salvata immagine via DOM redirect: {path.name} ({len(body)} bytes)")

            # Salva nel DB e Drive
            await self._save_to_db(path, filename, "DOM")

        except Exception as e:
            log.debug(f"Errore download DOM img: {e}")

    async def _save_to_db(self, path: Path, filename: str, method: str):
        """
        Disabilitato: Il servizio Go gestisce l'ingestion finale con Hash e metadati ricchi.
        Manteniamo il log per tracciare la cattura locale.
        """
        log.info(f"📸 Immagine catturata localmente ({method}): {filename}")

    def get_results(self) -> list[Path]:
        return self.new_saved_paths
