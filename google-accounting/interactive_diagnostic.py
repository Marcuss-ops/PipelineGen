"""
Strumento di diagnostica interattiva per Google Flow.
Permette di esplorare la pagina, vedere gli elementi e testare selettori in tempo reale.
"""
import asyncio
import logging
import os
import sys
from pathlib import Path
from playwright.async_api import async_playwright

# Aggiungi il path del progetto per importare i moduli locali
sys.path.append(os.getcwd())

from playwright_client import get_context_options
from automation.flow.config import log

logging.basicConfig(level=logging.INFO)

async def dump_elements(page):
    """Estrae e stampa elementi interessanti dalla pagina."""
    print("\n" + "="*50)
    print("🔍 DIAGNOSTICA PAGINA")
    print("="*50)
    
    # 1. Bottoni
    btns = await page.locator('button').all()
    print(f"\n🔘 BOTTONI VISIBILI ({len(btns)}):")
    for i, b in enumerate(btns):
        if await b.is_visible():
            text = (await b.inner_text()).strip().replace("\n", " ")[:50]
            aria = await b.get_attribute("aria-label") or ""
            print(f"  [{i}] '{text}' (aria: '{aria}')")

    # 2. Input / Textareas
    inps = await page.locator('input, textarea, div[role="textbox"], div[contenteditable="true"]').all()
    print(f"\n⌨️ INPUT/TEXTBOX VISIBILI ({len(inps)}):")
    for i, inp in enumerate(inps):
        if await inp.is_visible():
            tag = await inp.evaluate("el => el.tagName")
            placeholder = await inp.get_attribute("placeholder") or ""
            role = await inp.get_attribute("role") or ""
            print(f"  [{i}] {tag} (placeholder: '{placeholder}', role: '{role}')")

    # 3. Immagini
    imgs = await page.locator('img').all()
    print(f"\n🖼️ IMMAGINI VISIBILI ({len(imgs)}):")
    for i, img in enumerate(imgs[:10]):
        if await img.is_visible():
            src = await img.get_attribute("src") or ""
            print(f"  [{i}] src: {src[:80]}...")

    # 4. Screenshot
    path = Path("debug_diagnostic.png")
    await page.screenshot(path=str(path))
    print(f"\n📸 Screenshot salvato in: {path.absolute()}")
    print("="*50 + "\n")

async def interactive_loop(page):
    while True:
        await dump_elements(page)
        print("Comandi disponibili:")
        print("  c [index] - Clicca bottone per indice")
        print("  t [index] [text] - Scrivi testo in input per indice")
        print("  s [selector] - Clicca elemento per selettore CSS")
        print("  r - Refresh diagnostica")
        print("  q - Esci")
        
        cmd = input("\n👉 Inserisci comando: ").strip().split(" ", 2)
        if not cmd: continue
        
        try:
            action = cmd[0].lower()
            if action == 'q': break
            elif action == 'r': continue
            elif action == 'c':
                idx = int(cmd[1])
                btns = await page.locator('button').all()
                visible_btns = [b for b in btns if await b.is_visible()]
                await visible_btns[idx].click()
                print(f"✅ Cliccato bottone {idx}")
                await asyncio.sleep(2)
            elif action == 't':
                idx = int(cmd[1])
                text = cmd[2]
                inps = await page.locator('input, textarea, div[role="textbox"], div[contenteditable="true"]').all()
                visible_inps = [inp for inp in inps if await inp.is_visible()]
                target = visible_inps[idx]
                await target.click()
                await page.keyboard.press("Control+A")
                await page.keyboard.press("Backspace")
                await page.keyboard.type(text)
                await page.keyboard.press("Enter")
                print(f"✅ Scritto '{text}' in input {idx}")
                await asyncio.sleep(2)
            elif action == 's':
                sel = cmd[1]
                await page.locator(sel).first.click()
                print(f"✅ Cliccato selettore '{sel}'")
                await asyncio.sleep(2)
        except Exception as e:
            print(f"❌ Errore: {e}")

async def main():
    account = input("Account da usare (es. favamassimo): ") or "favamassimo"
    headless = input("Headless? (y/n, default n): ").lower() == 'y'
    
    async with async_playwright() as p:
        opts = get_context_options(account)
        browser = await p.chromium.launch(headless=headless)
        context = await browser.new_context(**opts)
        page = await context.new_page()
        
        print(f"🌐 Navigazione verso Google Flow...")
        await page.goto("https://labs.google/fx/it/tools/flow", wait_until="networkidle")
        
        await interactive_loop(page)
        await browser.close()

if __name__ == "__main__":
    asyncio.run(main())
