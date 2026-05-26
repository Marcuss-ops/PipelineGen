"""
Engine principale per l'automazione Google Flow.

BASATO SULLA VERSIONE STABILE FUNZIONANTE (commit c10c6f5).
L'unica differenza è la suddivisione in moduli + log estesi.
"""
import asyncio
import logging
import sys
import time
from pathlib import Path

from ..base import DOWNLOAD_DIR, BaseAutomation

from .capture import ImageCapturer
from .config import (
    GENERATE_BUTTON_SELECTORS,
    IMAGE_COUNT_FOUR_SELECTOR,
    IMAGE_COUNT_TOGGLE_SELECTOR,
    IMAGE_WAIT_TIMEOUT,
    MAX_IMAGES,
    NEW_PROJECT_BUTTON_TEXTS,
    POST_CLICK_WAIT,
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
    2. Attende caricamento (15s sleep — stabile, testato)
    3. Trova textbox prompt (div[role="textbox"])
    4. Digita prompt con delay umano
    5. Preme Enter → invia all'Agente (che genera le immagini)
    6. Imposta conteggio immagini a x4
    7. Clicca pulsante "Crea" (appare dopo generazione Agente)
    8. Cattura immagini via network + DOM polling
    9. Salva su disco + DB
    """

    PROMPT_SELECTORS = PROMPT_SELECTORS
    GENERATE_BUTTON_SELECTORS = GENERATE_BUTTON_SELECTORS

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
        await page.goto(url)
        await asyncio.sleep(POST_GOTO_WAIT)
        log.info(f"✅ Navigazione completata, attesa di {POST_GOTO_WAIT}s conclusa")

        # ── Trova textbox prompt ───────────────────────────────────────────────
        try:
            prompt_locator, prompt_selector = await self._find_prompt_textbox(page, debug_dir)
            if prompt_locator is None:
                raise RuntimeError(f"Prompt field not found after {POST_GOTO_WAIT}s wait")

            # ── Digita prompt ──────────────────────────────────────────────────
            log.info(f"⌨️ Digito prompt nel textbox (selector: {prompt_selector})...")
            await prompt_locator.click()
            await asyncio.sleep(1)
            await page.keyboard.press("Control+A")
            await page.keyboard.press("Backspace")
            await page.keyboard.type(full_prompt, delay=TYPE_DELAY_MS)
            await asyncio.sleep(1)
            log.info(f"✅ Prompt digitato: '{full_prompt}'")

            # ── Invio ad Agente ────────────────────────────────────────────────
            log.info("⏎ Invio prompt all'Agente (Enter)...")
            await page.keyboard.press("Enter")
            await asyncio.sleep(POST_ENTER_WAIT)
            log.info(f"✅ Enter premuto, attesa di {POST_ENTER_WAIT}s conclusa")

            # ── Imposta conteggio immagini a x4 ────────────────────────────────
            await self._set_image_count_to_4(page)

            await asyncio.sleep(1)

            # ── Trova e clicca pulsante Genera/Crea ────────────────────────────
            await self._click_generate_button(page)

            # ── Attiva network capture ─────────────────────────────────────────
            capturer.can_capture = True
            log.info("✅ Network capture attivato (can_capture=True)")

            # Screenshot verifica
            debug_path = debug_dir / f"GEN_START_{int(time.time())}.png"
            await page.screenshot(path=str(debug_path))
            log.info(f"📸 Screenshot verifica: {debug_path}")

            # ── Attesa e cattura immagini ──────────────────────────────────────
            log.info(f"⏳ In attesa delle immagini ({IMAGE_WAIT_TIMEOUT}s)...")
            
            # 1. Polling DOM immagini
            await capturer.poll_dom_for_images()

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

    async def _set_image_count_to_4(self, page):
        """Imposta il numero di immagini a 4 (massimo) con retry e log dettagliato."""
        log.info("🔢 Impostazione conteggio immagini a x4...")
        
        for attempt in range(3):
            try:
                toggle = page.locator(IMAGE_COUNT_TOGGLE_SELECTOR).first
                toggle_count = await toggle.count()
                log.info(f"  Tentativo {attempt+1}: toggle count={toggle_count}")
                
                if toggle_count == 0:
                    log.info("  Toggle conteggio non trovato, probabilmente già a 4")
                    break
                    
                current_text = await toggle.inner_text()
                log.info(f"  Testo attuale: '{current_text.strip()}'")
                
                if "x4" in current_text:
                    log.info("  ✅ Già impostato a x4")
                    break

                log.info(f"  Apro menu conteggio...")
                await toggle.click()
                await asyncio.sleep(2)

                four_btn = page.locator(IMAGE_COUNT_FOUR_SELECTOR).first
                four_count = await four_btn.count()
                log.info(f"  Pulsante x4: count={four_count}")
                
                if four_count > 0:
                    await four_btn.click(force=True)
                    await asyncio.sleep(2)
                    
                    new_text = await toggle.inner_text()
                    log.info(f"  Dopo click: testo='{new_text.strip()}'")
                    
                    if "x4" in new_text:
                        log.info("  ✅ Conteggio impostato a x4")
                        break
                    else:
                        log.warning(f"  ⚠️ Click x4 non生效, riprovo...")
                else:
                    log.warning("  ⚠️ Pulsante x4 non trovato, Escape...")
                    await page.keyboard.press("Escape")
                    await asyncio.sleep(1)
                    
            except Exception as e:
                log.debug(f"Errore impostazione conteggio: {e}")

    async def _click_generate_button(self, page):
        """Trova e clicca il pulsante Genera/Crea con log dettagliato."""
        log.info("🔍 Ricerca pulsante Genera/Crea...")
        
        found = False
        for selector in self.GENERATE_BUTTON_SELECTORS:
            try:
                loc = page.locator(selector).first
                count = await loc.count()
                log.info(f"  Selector '{selector}': count={count}")
                
                if count > 0:
                    tag = await loc.evaluate("el => el.tagName")
                    text = (await loc.inner_text())[:60] if await loc.inner_text() else ""
                    visible = await loc.is_visible()
                    box = await loc.bounding_box()
                    log.info(f"  ✅ TROVATO: tag={tag} visible={visible} text='{text.strip()}'")
                    if box:
                        log.info(f"     box=({box['x']:.0f},{box['y']:.0f}) {box['width']:.0f}x{box['height']:.0f}")
                    
                    await loc.click(force=True)
                    log.info(f"  ✅ Clickato con force=True: {selector}")
                    found = True
                    await asyncio.sleep(POST_CLICK_WAIT)
                    break
            except Exception as e:
                log.warning(f"  ⚠️ Selector '{selector}' error: {e}")
                continue

        if not found:
            log.warning("⚠️ Nessun pulsante Genera/Crea trovato. Confido nell'Enter già premuto.")


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