"""
Engine principale per l'automazione Google Flow.

BASATO SULLA VERSIONE STABILE FUNZIONANTE (commit c10c6f5).
L'unica differenza è la suddivisione in moduli + log estesi.
"""
import asyncio
import logging
import time
import random
from pathlib import Path

from ..base import DOWNLOAD_DIR, BaseAutomation, human_delay, human_scroll

from .capture import ImageCapturer
from .config import (
    AGENT_ACTIVE_INDICATOR_XPATHS,
    AGENT_CLOSE_BUTTON_SELECTOR,
    AGENT_INSTRUCTIONS_BUTTON_TEXT,
    AGENT_STRUMENTI_BUTTON_TEXT,
    AGENT_TOGGLE_XPATHS,
    APPROVE_BUTTON_SELECTORS,
    DONT_ASK_AGAIN_SELECTOR,
    IMAGE_COUNT_FOUR_SELECTOR,
    IMAGE_COUNT_TOGGLE_SELECTOR,
    IMAGE_WAIT_TIMEOUT,
    NEW_PROJECT_BUTTON_TEXTS,
    POST_ENTER_WAIT,
    POST_GOTO_WAIT,
    PROMPT_SELECTORS,
    SEND_BUTTON_SELECTORS,
    TYPE_DELAY_MS,
    log,
)

log = logging.getLogger("AutomationEngine.FlowEngine")


class ImageFXFlowAutomation(BaseAutomation):
    """
    Automazione per Google Labs ImageFX Flow.
    
    FLOW:
    1. Naviga a labs.google/fx/it/tools/flow
    2. Attende caricamento (dashboard o editor)
    3. Trova textbox prompt (div[role="textbox"])
    4. Imposta conteggio immagini a x4
    5. Digita prompt con delay umano
    6. Preme Enter → invia all'Agente (che genera le immagini)
    7. Cattura immagini via network + DOM polling
    8. Salva su disco + DB
    """

    PROMPT_SELECTORS = PROMPT_SELECTORS

    async def generate_images(self, prompt: str, project_id: str = None, style: str = None) -> list[Path]:
        import_storage()
        
        # ── Setup ──────────────────────────────────────────────────────────────
        project_id = None  # Sempre fresh project
        main_style, sub_style = parse_style(style)
        dest_dir = get_structured_path(main_style, sub_style)
        debug_dir = DOWNLOAD_DIR / "debug"
        debug_dir.mkdir(parents=True, exist_ok=True)
        
        full_prompt = f"style {style} {prompt}" if style else prompt
        
        log.info("=" * 80)
        log.info(f"NUOVA GENERAZIONE FLOW")
        log.info(f"  Prompt: '{full_prompt}'")
        log.info(f"  Style: {style} → main={main_style}, sub={sub_style}")
        log.info(f"  Destinazione: {dest_dir}")
        log.info(f"  Debug: {debug_dir}")
        log.info("=" * 80)

        # ── Pagina ─────────────────────────────────────────────────────────────
        url = "https://labs.google/fx/it/tools/flow"
        page = await self.context.new_page()
        capturer = ImageCapturer(page, dest_dir, main_style, sub_style, prompt, style, full_prompt, project_id)

        # Blacklist immagini DOM pre-esistenti
        await capturer.blacklist_existing_images()

        # Network listener
        page.on("response", capturer.make_network_handler())
        
        # Log sintetico per le risposte di generazione (solo status)
        async def on_response_status(response):
            if "batchGenerateImages" in response.url and response.request.method == "POST":
                log.debug(f"batchGenerateImages status={response.status} url={response.url[:80]}")
        
        page.on("response", on_response_status)


        # ── Navigazione ────────────────────────────────────────────────────────
        log.info(f"🌐 Navigazione verso: {url}")
        await page.goto(url, wait_until="networkidle")
        try:
            await page.wait_for_load_state("networkidle", timeout=15000)
        except:
            log.info("Timeout attesa networkidle, continuo comunque...")
        
        # Ritardo umano dopo caricamento
        await human_delay(1500, 4000)
        await human_scroll(page)

        # ── Se landing page, clicca "Create with Google Flow" ──────────────────
        create_btn = page.locator('text=Create with Google Flow').first
        if await create_btn.count() > 0:
            log.info("🖱️ Landing page rilevata, clicco 'Create with Google Flow'...")
            await human_delay(800, 2000)
            await create_btn.click()
            await asyncio.sleep(5)
            try:
                await page.wait_for_load_state("networkidle", timeout=15000)
            except:
                pass
            await human_delay(1000, 3000)
        
        # ── Polling: dashboard? → Nuovo progetto / editor? → prosegui ─────────
        log.info("In attesa del caricamento della pagina (editor o dashboard)...")
        dashboard_clicked = False
        for _ in range(30):
            try:
                # 1. Già nell'editor? C'è il textbox?
                # Controlliamo prima gli XPath specifici forniti dall'utente
                for sel in self.PROMPT_SELECTORS:
                    loc = page.locator(sel).first
                    if await loc.count() > 0 and await loc.is_visible():
                        log.info(f"✅ Rilevato textbox dell'editor via '{sel}', procedo...")
                        return await self._execute_generation_flow(page, capturer, debug_dir, full_prompt, project_id, dest_dir, prompt, main_style, sub_style)

                # 2. Dashboard? Cerca pulsante per nuovo progetto (se non l'abbiamo già cliccato)
                if not dashboard_clicked:
                    found_btn = None
                    for btn_text in NEW_PROJECT_BUTTON_TEXTS:
                        loc = page.locator(f"text={btn_text}").first
                        if await loc.count() > 0:
                            # Filtro personaggi
                            context_text = await loc.evaluate("el => el.parentElement.innerText")
                            if "character" in context_text.lower() or "personaggi" in context_text.lower():
                                continue
                            found_btn = loc
                            break
                    
                    if found_btn:
                        log.info(f"🖱️ Dashboard rilevata, clic su '{await found_btn.inner_text()}'")
                        await found_btn.click(force=True)
                        dashboard_clicked = True
                        await asyncio.sleep(5)
                        continue

            except Exception as e:
                log.debug(f"Polling error: {e}")
            await asyncio.sleep(1)
        
        raise RuntimeError("Impostazione editor fallita (timeout 30s)")

    async def _execute_generation_flow(self, page, capturer, debug_dir, full_prompt, project_id, dest_dir, prompt, main_style, sub_style):
        """Sottoprocesso di inserimento prompt e invio."""
        try:
            # ── Controlla Agente ───────────────────────────────────
            await self._check_agent_status(page)

            # ── Imposta conteggio immagini x4 ─────────────────────────────
            await self._set_image_count_to_four(page)
            await human_delay(1000, 2000)

            # ── Trova textbox prompt ───────────────────────────────────────────────
            prompt_locator, prompt_selector = await self._find_prompt_textbox(page, debug_dir)
            if prompt_locator is None:
                raise RuntimeError("Prompt field not found")

            log.info(f"⌨️ Focus su {prompt_selector}")
            await prompt_locator.click(force=True)
            await human_delay(500, 1000)
            
            # Pulizia e scrittura
            await page.keyboard.press("Control+A")
            await page.keyboard.press("Backspace")
            await human_delay(300, 600)
            await page.keyboard.type(full_prompt, delay=TYPE_DELAY_MS)
            await asyncio.sleep(0.5)
            
            log.info(f"✅ Prompt digitato: '{full_prompt}'")
            await page.screenshot(path=str(debug_dir / f"PROMPT_READY_{int(time.time())}.png"))

            # ── Invio ────────────────────────────────────────────────
            capturer.can_capture = True
            log.info("🚀 Invio generazione...")
            
            # Proviamo Enter + Send Button
            await page.keyboard.press("Enter")
            await asyncio.sleep(0.5)
            
            found_send = False
            for sel in SEND_BUTTON_SELECTORS:
                try:
                    btn = page.locator(sel).first
                    if await btn.count() > 0 and await btn.is_visible():
                        log.info(f"🖱️ CLICK (Send Button): {sel}")
                        await btn.click(force=True)
                        found_send = True
                        break
                except: continue
            
            if not found_send:
                log.info("ℹ️ Pulsante invio non trovato, spero in Enter.")

            await human_delay(3000, 5000)

            # Screenshot post-invio
            await page.screenshot(path=str(debug_dir / f"GEN_START_{int(time.time())}.png"))

            # ── Gestione Approvazione Agente ───────────────────────────
            await self._handle_agent_approval(page, debug_dir)

            # ── Attesa Immagini ──────────────────────────────────────────────────
            log.info(f"⏳ In attesa delle immagini ({IMAGE_WAIT_TIMEOUT}s)...")
            await capturer.poll_dom_for_images()
            
            # ── Attesa caricamenti Drive/DB ─────────────────────────────────────
            await capturer.wait_for_uploads()

            results = capturer.get_results()
            log.info(f"📊 Risultato: {len(results)} immagini catturate")

            # ── Salva metadata.json ────────────────────────────────────────────
            _save_metadata(dest_dir, main_style, sub_style, prompt, full_prompt, project_id, results)

            return results

        except Exception as e:
            log.error(f"❌ ERRORE: {e}")
            import traceback
            log.error(traceback.format_exc())
            debug_path = debug_dir / f"FAIL_{int(time.time())}.png"
            try:
                await page.screenshot(path=str(debug_path))
                log.error(f"📸 Screenshot errore: {debug_path}")
            except:
                pass
            return []
        finally:
            await page.close()
            log.info("🔒 Pagina chiusa")

    async def _check_agent_status(self, page):
        """Controlla lo stato dell'agente senza modificarlo."""
        log.info("🕵️ Controllo stato Agente...")
        try:
            buttons = await page.locator('button, div[role="button"]').all()
            agent_btn = None
            for btn in buttons:
                text = (await btn.inner_text()).strip()
                if "Agente" in text or "Agent" in text:
                    agent_btn = btn
                    break
            
            if agent_btn:
                pressed = await agent_btn.get_attribute("aria-pressed")
                status = "ATTIVO" if pressed == "true" else "DISATTIVO"
                log.info(f"ℹ️ Stato Agente: {status}")
            else:
                log.info("ℹ️ Pulsante Agente non trovato.")
        except Exception as e:
            log.debug(f"Errore controllo Agente: {e}")



    async def _set_image_count_to_four(self, page):
        """Imposta il conteggio immagini a x4."""
        log.info("🔍 Controllo impostazione conteggio immagini (x4)...")
        try:
            # Prova a cercare il toggle direttamente
            toggle = page.locator(IMAGE_COUNT_TOGGLE_SELECTOR).first
            
            # Se non trovato, prova ad aprire "Impostazioni" prima
            if await toggle.count() == 0:
                log.info("ℹ️ Toggle x1/x2/x3 non trovato, provo ad aprire 'Impostazioni'...")
                settings_btn = page.locator('button:has-text("Impostazioni"), button:has-text("Settings"), button:has-text("tune")').first
                if await settings_btn.count() > 0:
                    log.info("🖱️ CLICK: Impostazioni")
                    await settings_btn.click()
                    await asyncio.sleep(2)
                    toggle = page.locator(IMAGE_COUNT_TOGGLE_SELECTOR).first

            if await toggle.count() > 0:
                log.info(f"🖱️ CLICK: toggle conteggio immagini ({IMAGE_COUNT_TOGGLE_SELECTOR})...")
                await toggle.click()
                await asyncio.sleep(1)
                
                # 3. Clicca su x4 nel menu a comparsa
                four_btn = page.locator(IMAGE_COUNT_FOUR_SELECTOR).first
                if await four_btn.count() > 0:
                    log.info(f"🖱️ CLICK: selezione 'x4' ({IMAGE_COUNT_FOUR_SELECTOR})...")
                    await four_btn.click()
                    await asyncio.sleep(1)
                    log.info("✅ Conteggio immagini impostato a x4")
                    
                    # Chiudi il menu premendo Escape
                    await page.keyboard.press("Escape")
                    await asyncio.sleep(0.5)
                else:
                    log.warning("⚠️ Opzione 'x4' non trovata nel menu")
            else:
                log.info("ℹ️ Toggle conteggio immagini non trovato.")
        except Exception as e:
            log.warning(f"⚠️ Errore durante impostazione x4: {e}")

    async def _handle_agent_approval(self, page, debug_dir):
        """Gestisce il pannello dell'agente che chiede conferma per la generazione."""
        log.info("🕵️ Controllo eventuale richiesta di approvazione Agente...")
        
        # 1. Cerca il checkbox "non chiedermelo più"
        try:
            checkbox = page.locator(DONT_ASK_AGAIN_SELECTOR).first
            if await checkbox.count() > 0 and await checkbox.is_visible():
                log.info("🔘 Trovato checkbox 'Non chiedermelo più', clicco...")
                await checkbox.click()
                await asyncio.sleep(0.5)
        except Exception as e:
            log.debug(f"Checkbox 'non chiedermelo' non trovato o non cliccabile: {e}")

        # 2. Cerca il pulsante "Approva" / "Genera"
        found_approve = False
        for selector in APPROVE_BUTTON_SELECTORS:
            try:
                btn = page.locator(selector).first
                if await btn.count() > 0 and await btn.is_visible():
                    log.info(f"✅ Trovato pulsante approvazione con selettore '{selector}', clicco...")
                    await btn.click()
                    found_approve = True
                    # Screenshot dopo il clic
                    await page.screenshot(path=str(debug_dir / f"APPROVE_CLICKED_{int(time.time())}.png"))
                    break
            except Exception as e:
                log.debug(f"Errore durante ricerca/clic pulsante '{selector}': {e}")
                continue
        
        if not found_approve:
            log.info("ℹ️ Nessun pulsante di approvazione rilevato (potrebbe non essere apparso).")
        else:
            log.info("🚀 Approvazione inviata, attesa avvio generazione...")
            await asyncio.sleep(2)

    async def _find_prompt_textbox(self, page, debug_dir):
        """Trova il textbox del prompt."""
        best_loc = None
        best_sel = None
        
        for sel in self.PROMPT_SELECTORS:
            loc = page.locator(sel).first
            if await loc.count() > 0 and await loc.is_visible():
                text = await loc.inner_text()
                if "creare" in text.lower() or "create" in text.lower() or "prompt" in text.lower():
                    log.info(f"🎯 Prompt field trovato via '{sel}'")
                    return loc, sel
                if best_loc is None:
                    best_loc = loc
                    best_sel = sel

        if best_loc:
            log.info(f"⚠️ Usando miglior candidato trovato via '{best_sel}'")
            return best_loc, best_sel
        
        return None, None

def parse_style(style: str | None):
    """Divide style in main/sub per struttura cartelle."""
    main_style = "default"
    sub_style = "general"
    if style:
        parts = style.split(maxsplit=1)
        main_style = parts[0]
        if len(parts) > 1:
            sub_style = parts[1]
    return main_style, sub_style


def import_storage():
    """Import differito per evitare circular imports."""
    global get_structured_path, _save_metadata
    from storage import get_structured_path
    
    def _save_metadata(dest_dir, main_style, sub_style, prompt, full_prompt, project_id, paths):
        """Salva metadata.json della generazione."""
        try:
            from storage import save_generation_metadata
            save_generation_metadata(dest_dir, {
                "generation_id": dest_dir.name,
                "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S"),
                "style": {"main": main_style, "sub": sub_style},
                "prompt": prompt,
                "full_prompt": full_prompt,
                "project_id": project_id,
                "assets": [p.name for p in paths]
            })
            log.info(f"✅ metadata.json salvato in {dest_dir}")
        except Exception as e:
            log.warning(f"⚠️ Errore salvataggio metadata: {e}")


async def generate_flow_images(prompt: str, project_id: str = None, style: str = None,
                               account: str = None, headless: bool = True):
    """Entry point: genera immagini via Google Flow."""
    async with ImageFXFlowAutomation(account=account, headless=headless) as engine:
        results = await engine.generate_images(prompt, project_id, style)
        paths = [str(p) for p in results]
        log.info(f"🏁 generate_flow_images completato: {len(paths)} immagini")
        return paths