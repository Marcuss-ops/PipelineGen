import asyncio
import logging
import sys
import argparse
from pathlib import Path
from playwright.async_api import async_playwright
from config import get_session_path, get_profile_path

async def main():
    parser = argparse.ArgumentParser(description="Google Login Automation")
    parser.add_argument("--account", type=str, help="Account name for the session")
    args = parser.parse_args()

    profile_path = get_profile_path(args.account)
    session_path = get_session_path(args.account)

    print("\n" + "="*60)
    print(f"PROCEDURA DI LOGIN MANUALE PER ACCOUNT: {args.account or 'default'}")
    print("="*60)
    print("1. Si aprirà una finestra di Chrome.")
    print("2. Effettua il login al tuo account Google.")
    print("3. Vai sulla home di Google Vids o Google Labs.")
    print("4. UNA VOLTA ENTRATO, TORNA QUI E PREMI [INVIO] PER SALVARE.")
    print("="*60 + "\n")

    async with async_playwright() as p:
        launch_args = [
            "--disable-blink-features=AutomationControlled",
            "--no-sandbox",
            "--disable-setuid-sandbox",
        ]
        
        context = await p.chromium.launch_persistent_context(
            user_data_dir=str(profile_path),
            headless=False, 
            args=launch_args,
            channel="chrome",
            user_agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
        )
        
        # Import legacy cookies if profile is empty but session JSON exists
        if session_path.exists() and not any(profile_path.iterdir()):
            try:
                import json as _json
                state = _json.loads(session_path.read_text(encoding="utf-8"))
                cookies = state.get("cookies", [])
                if cookies:
                    await context.add_cookies(cookies)
                    print(f"Imported {len(cookies)} cookies from legacy session JSON")
            except Exception as e:
                print(f"Warning: could not import legacy cookies: {e}")

        
        page = await context.new_page()
        await page.goto("https://vids.google.com")

        # Aspettiamo l'input dell'utente nel terminale
        await asyncio.get_event_loop().run_in_executor(None, sys.stdin.readline)

        # Salviamo lo stato anche in formato JSON legacy per retrocompatibilità
        await context.storage_state(path=str(session_path))
        print(f"\nProfilo Chrome salvato in: {profile_path}")
        print(f"Sessione specchio JSON salvata in: {session_path}")
        
        await context.close()

if __name__ == "__main__":
    asyncio.run(main())
