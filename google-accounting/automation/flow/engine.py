"""
Engine principale per l'automazione Google Flow.

BASATO SULLA VERSIONE STABILE FUNZIONANTE (commit c10c6f5).
L'unica differenza è la suddivisione in moduli + log estesi.
"""
import asyncio
import logging
import time
from pathlib import Path

from ..base import DOWNLOAD_DIR, BaseAutomation

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

        # ── Navigazione ────────────────────────────────────────────────────────
        log.info(f"🌐 Navigazione verso: {url}")
        await page.goto(url, wait_until="networkidle")
        try:
            await page.wait_for_load_state("networkidle", timeout=15000)
        except:
            log.info("Timeout attesa networkidle, continuo comunque...")
        await asyncio.sleep(2)
        
        # ── Polling: dashboard? → Nuovo progetto / editor? → prosegui ─────────
        log.info("In attesa del caricamento della pagina (editor o dashboard)...")
        for _ in range(30):
            try:
                # 1. Già nell'editor? C'è il textbox?
                for sel in self.PROMPT_SELECTORS:
                    if await page.locator(sel).first.count() > 0:
                        log.info(f"✅ Rilevato textbox dell'editor, procedo...")
                        break
                else:
                    # Nessun textbox trovato, continua a cercare
                    pass
                if await page.locator(self.PROMPT_SELECTORS[0]).first.count() > 0:
                    break
                
                # 2. Dashboard? C'è "Nuovo progetto"?
                found_btn = None
                for btn_text in NEW_PROJECT_BUTTON_TEXTS:
                    loc = page.locator(f"text={btn_text}").first
                    if await loc.count() > 0:
                        found_btn = loc
                        btn_text_found = btn_text
                        break
                if found_btn:
                    log.info(f"🖱️ Rilevata dashboard, clic su '{btn_text_found}'...")
                    await found_btn.click()
                    await asyncio.sleep(5)
                    break
            except Exception as e:
                log.debug(f"Polling error (navigazione in corso): {e}")
            await asyncio.sleep(0.5)
        
        log.info(f"✅ Pagina caricata: {page.url}")

        # ── Disabilita Agente se attivo (NUOVO) ────────────────────────────────
        await self._disable_agent_if_active(page)

        # ── Imposta conteggio immagini x4 (NUOVO) ─────────────────────────────
        await self._set_image_count_to_four(page)

        # ── Trova textbox prompt ───────────────────────────────────────────────
        try:
            prompt_locator, prompt_selector = await self._find_prompt_textbox(page, debug_dir)
            if prompt_locator is None:
                raise RuntimeError(f"Prompt field not found after {POST_GOTO_WAIT}s wait")

            # ── Digita prompt ──────────────────────────────────────────────────
            log.info(f"⌨️ KEYBOARD: CLICK su textbox {prompt_selector}")
            await prompt_locator.click(force=True)
            await asyncio.sleep(1)
            log.info("⌨️ KEYBOARD: PRESS Control+A")
            await page.keyboard.press("Control+A")
            log.info("⌨️ KEYBOARD: PRESS Backspace")
            await page.keyboard.press("Backspace")
            log.info(f"⌨️ KEYBOARD: TYPE '{full_prompt}'")
            await page.keyboard.type(full_prompt, delay=TYPE_DELAY_MS)
            await asyncio.sleep(1)
            log.info(f"✅ Prompt digitato: '{full_prompt}'")

            # ── Invio ad Agente ────────────────────────────────────────────────
            log.info("⌨️ KEYBOARD: PRESS Enter")
            capturer.can_capture = True
            log.info("✅ Network capture attivato PRIMA dell'Enter")
            await page.keyboard.press("Enter")
            await asyncio.sleep(POST_ENTER_WAIT)
            log.info(f"✅ Enter premuto, attesa di {POST_ENTER_WAIT}s conclusa")

            # ── Gestione Approvazione Agente (NUOVO) ───────────────────────────
            await self._handle_agent_approval(page, debug_dir)

            await asyncio.sleep(1)

            # Screenshot verifica
            debug_path = debug_dir / f"GEN_START_{int(time.time())}.png"
            await page.screenshot(path=str(debug_path))
            log.info(f"📸 Screenshot verifica: {debug_path}")

            # ── Attesa e cattura immagini ──────────────────────────────────────
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

    async def _disable_agent_if_active(self, page):
        """Disabilita l'agente se attivo, come da istruzioni utente."""
        log.info("⏳ Attesa iniziale di 12s prima di controllare l'Agente...")
        await asyncio.sleep(12)
        
        debug_dir = DOWNLOAD_DIR / "debug"
        await page.screenshot(path=str(debug_dir / f"AGENT_CHECK_START_{int(time.time())}.png"))
        
        # Debug: lista tutti i bottoni e controlla XPaths
        await self._debug_list_elements(page)
        for i, xpath in enumerate(AGENT_ACTIVE_INDICATOR_XPATHS):
            await self._debug_xpath(page, xpath, f"INDICATOR_{i}")
        for i, xpath in enumerate(AGENT_TOGGLE_XPATHS):
            await self._debug_xpath(page, xpath, f"TOGGLE_{i}")
        
        log.info("🕵️ Controllo se l'Agente è attivo...")
        try:
            # 1. Cerca indicatore tramite XPaths forniti
            indicator = None
            for xpath in AGENT_ACTIVE_INDICATOR_XPATHS:
                loc = page.locator(xpath).first
                if await loc.count() > 0:
                    log.info(f"✅ Trovato indicatore Agente via XPath: {xpath}")
                    indicator = loc
                    break
            
            # 2. Fallback se gli XPath falliscono: cerca per testo "Istruzioni agente"
            if indicator is None:
                log.info(f"🔍 Provo fallback per testo: '{AGENT_INSTRUCTIONS_BUTTON_TEXT}'")
                loc = page.locator(f'button:has-text("{AGENT_INSTRUCTIONS_BUTTON_TEXT}")').first
                if await loc.count() > 0:
                    indicator = loc
                    log.info("✅ Trovato indicatore Agente via testo 'Istruzioni agente'")

            if indicator is not None:
                log.info(f"⚠️ Agente rilevato come ATTIVO.")
                
                # 3. Premi l'indicatore dell'agente per aprire il pannello
                log.info(f"🖱️ CLICK: indicatore agente...")
                await indicator.click()
                await asyncio.sleep(3)
                await page.screenshot(path=str(debug_dir / f"AGENT_PANEL_OPENED_{int(time.time())}.png"))
                
                # Debug post-click: lista bottoni nel pannello
                log.info("🔍 [DEBUG] Bottoni dopo apertura pannello agente:")
                await self._debug_list_elements(page)

                # 4. Premi il tasto toggle dell'agente (prova vari XPaths)
                found_toggle = False
                for xpath in AGENT_TOGGLE_XPATHS:
                    toggle = page.locator(xpath).first
                    if await toggle.count() > 0:
                        log.info(f"🖱️ CLICK: tasto toggle agente ({xpath}) per disattivarlo...")
                        await toggle.click()
                        await asyncio.sleep(2)
                        await page.screenshot(path=str(debug_dir / f"AGENT_TOGGLE_CLICKED_{int(time.time())}.png"))
                        found_toggle = True
                        break
                
                if not found_toggle:
                    log.warning(f"⚠️ Nessun tasto toggle agnete trovato tra i selettori forniti nel pannello.")
                    # Prova fallback nel pannello: cerca bottoni con 'Agente' o 'Agent'
                    log.info("🔍 Cerco toggle nel pannello tramite testo...")
                    toggles = await page.locator('button:has-text("Agente"), button:has-text("Agent")').all()
                    for t in toggles:
                        if await t.is_visible():
                            log.info(f"🖱️ CLICK (fallback testo): toggle '{await t.inner_text()}'")
                            await t.click()
                            await asyncio.sleep(2)
                            found_toggle = True
                            break
                
                # 5. Chiudi il pannello per liberare il textbox prompt
                log.info("🖱️ Tentativo di CHIUSURA pannello agente...")
                close_btn = page.locator(AGENT_CLOSE_BUTTON_SELECTOR).first
                if await close_btn.count() > 0:
                    await close_btn.click()
                    await asyncio.sleep(1)
                    log.info("✅ Pannello agente chiuso.")
                else:
                    log.info("ℹ️ Tasto chiusura non trovato, provo tasto Escape.")
                    await page.keyboard.press("Escape")
                    await asyncio.sleep(1)

                log.info("✅ Procedura disattivazione Agente conclusa.")
            else:
                log.info("✅ Agente non attivo (indicatore non trovato).")
        except Exception as e:
            log.warning(f"⚠️ Errore durante il controllo/disattivazione Agente: {e}")
            await page.screenshot(path=str(debug_dir / f"AGENT_ERROR_{int(time.time())}.png"))

    async def _debug_xpath(self, page, xpath, label):
        """Debug per verificare la presenza di un XPath e loggare l'HTML."""
        try:
            # Rimuove prefisso xpath= se presente
            xp = xpath.replace("xpath=", "")
            found = await page.evaluate(f"""
                (xpath) => {{
                    const el = document.evaluate(xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
                    return el ? el.outerHTML : null;
                }}
            """, xp)
            if found:
                log.info(f"  [DEBUG] XPath {label} TROVATO: {found[:200]}...")
                return True
            else:
                log.info(f"  [DEBUG] XPath {label} NON TROVATO")
                return False
        except Exception as e:
            log.debug(f"  [DEBUG] Errore verifica XPath {label}: {e}")
            return False

    async def _debug_list_elements(self, page):
        """Lista tutti i bottoni e altri elementi interattivi per debug."""
        log.info("🔍 [DEBUG] Elenco bottoni visibili nella pagina:")
        try:
            buttons = await page.locator('button, div[role="button"]').all()
            for i, btn in enumerate(buttons):
                if await btn.is_visible():
                    text = (await btn.inner_text()).strip().replace("\n", " ")
                    role = await btn.get_attribute("role") or "button"
                    log.info(f"  [{i}] ROLE={role} TEXT='{text[:50]}'")
        except Exception as e:
            log.warning(f"  [DEBUG] Errore durante listing elementi: {e}")

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
        """Trova il textbox del prompt tra i selettori disponibili con log dettagliato."""
        log.info("🔍 Ricerca textbox prompt...")
        
        for selector in self.PROMPT_SELECTORS:
            try:
                loc = page.locator(selector).first
                count = await loc.count()
                log.info(f"  Selector '{selector}': count={count}")
                
                if count > 0:
                    tag = await loc.evaluate("el => el.tagName")
                    role = await loc.get_attribute("role")
                    inner = (await loc.inner_text())[:80] if await loc.inner_text() else "(vuoto)"
                    log.info(f"  ✅ TROVATO: tag={tag} role={role} text='{inner}'")
                    return loc, selector
            except Exception as e:
                log.warning(f"  ⚠️ Selector '{selector}' error: {e}")
                continue

        log.error("❌ NESSUN textbox trovato tra i selettori!")
        try:
            await page.screenshot(path=str(debug_dir / f"NO_TEXTBOX_{int(time.time())}.png"))
        except:
            pass
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