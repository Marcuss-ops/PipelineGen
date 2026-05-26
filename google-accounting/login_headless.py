#!/usr/bin/env python3
"""
Login headless per Google Flow.
Usa Playwright in modalità headless + input testuale per il login.
Mostra screenshot via file e chiede credenziali via terminale.
"""
import asyncio
import sys
import argparse
from pathlib import Path
from playwright.async_api import async_playwright
from config import get_session_path

async def main():
    parser = argparse.ArgumentParser(description="Google Login Headless")
    parser.add_argument("--account", type=str, help="Account name for the session")
    args = parser.parse_args()

    session_path = get_session_path(args.account)
    session_path.parent.mkdir(parents=True, exist_ok=True)
    screenshot_dir = Path("/tmp/ga_login")
    screenshot_dir.mkdir(parents=True, exist_ok=True)

    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        context = await browser.new_context(
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
            viewport={"width": 1920, "height": 1080}
        )
        page = await context.new_page()

        # 1. Vai a Flow — il CTA reindirizza a login
        print("\n[1/5] Apro Google Flow...")
        await page.goto("https://labs.google/fx/it/tools/flow", wait_until="domcontentloaded", timeout=30000)
        await asyncio.sleep(3)

        # Click CTA
        cta = page.locator('button:has-text("Create with Google Flow")').first
        if await cta.count() > 0:
            await cta.click()
            await asyncio.sleep(5)
            await page.wait_for_load_state("networkidle", timeout=30000)

        await page.screenshot(path=str(screenshot_dir / "01_login_page.png"))
        print(f"   Screenshot salvato: {screenshot_dir / '01_login_page.png'}")
        print(f"   URL attuale: {page.url}")

        # 2. Se siamo sulla pagina di login Google
        if "accounts.google.com" in page.url or "signin" in page.url:
            print("\n[2/5] Pagina di login Google rilevata.")
            
            # Cerca campo email
            email_input = page.locator('input[type="email"]').first
            if await email_input.count() > 0:
                print("   Inserisci EMAIL e premi Invio (oppure lascia vuoto per saltare):")
                email = sys.stdin.readline().strip()
                if email:
                    await email_input.fill(email)
                    await email_input.press("Enter")
                    await asyncio.sleep(5)
                    await page.wait_for_load_state("networkidle", timeout=30000)
                    await page.screenshot(path=str(screenshot_dir / "02_after_email.png"))
                    print(f"   Screenshot: {screenshot_dir / '02_after_email.png'}")
                    print(f"   URL: {page.url}")

            # Cerca campo password
            password_input = page.locator('input[type="password"]').first
            if await password_input.count() > 0:
                print("\n   Inserisci PASSWORD e premi Invio (oppure lascia vuoto per saltare):")
                password = sys.stdin.readline().strip()
                if password:
                    await password_input.fill(password)
                    await password_input.press("Enter")
                    await asyncio.sleep(8)
                    await page.wait_for_load_state("networkidle", timeout=30000)
                    await page.screenshot(path=str(screenshot_dir / "03_after_password.png"))
                    print(f"   Screenshot: {screenshot_dir / '03_after_password.png'}")
                    print(f"   URL: {page.url}")

            # Potrebbe chiedere 2FA o conferma
            await asyncio.sleep(5)
            await page.screenshot(path=str(screenshot_dir / "04_after_auth.png"))
            print(f"\n   Screenshot finale: {screenshot_dir / '04_after_auth.png'}")
            print(f"   URL finale: {page.url}")

        # 3. Naviga a Flow workspace
        print("\n[3/5] Ritorno a Flow...")
        await page.goto("https://labs.google/fx/it/tools/flow", wait_until="domcontentloaded", timeout=30000)
        await asyncio.sleep(3)

        # Click CTA di nuovo
        cta = page.locator('button:has-text("Create with Google Flow")').first
        if await cta.count() > 0 and await cta.is_visible():
            await cta.click()
            await asyncio.sleep(5)
            await page.wait_for_load_state("networkidle", timeout=30000)

        await page.screenshot(path=str(screenshot_dir / "05_flow_workspace.png"))
        print(f"   Screenshot workspace: {screenshot_dir / '05_flow_workspace.png'}")
        print(f"   URL workspace: {page.url}")

        # 4. Se siamo entrati nel workspace, salva la sessione
        if "accounts.google.com" not in page.url and "signin" not in page.url:
            await context.storage_state(path=str(session_path))
            size = session_path.stat().st_size
            print(f"\n[4/5] ✅ Sessione salvata: {session_path} ({size} byte)")
        else:
            print(f"\n[4/5] ❌ Non autenticato. URL: {page.url}")
            print("   Servono credenziali o 2FA. Puoi:")
            print("   1. Rilanciare con 'xvfb-run python3 login.py --account favamassimo' per login visivo")
            print("   2. Oppure usare NVIDIA che funziona già senza login")

        await browser.close()

    print("\n[5/5] Fatto.")

if __name__ == "__main__":
    asyncio.run(main())
