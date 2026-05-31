import asyncio
from playwright.async_api import async_playwright
from pathlib import Path
import os

async def login(account: str = "favamassimo"):
    """Opens a headed browser to allow manual login, saving directly to the clean profile dir."""
    profile_dir = Path(f"profiles/{account}")
    profile_dir.mkdir(parents=True, exist_ok=True)
    
    print("="*60)
    print(f" AVVIO LOGIN MANUALE PER: {account}")
    print(f" Cartella Profilo: {profile_dir.absolute()}")
    print("="*60)
    print("\n1. Si aprirà una finestra di Chromium.")
    print("2. EFFETTUA IL LOGIN al tuo account Google.")
    print("3. QUANDO HAI FINITO e vedi Google Vids, TORNA QUI.")
    print("4. PREMI [INVIO] IN QUESTO TERMINALE PER SALVARE E CHIUDERE.")
    print("\nPROFILO PULITO: Niente più dati vecchi o Snap. Solo Playwright puro.\n")
    
    async with async_playwright() as p:
        context = await p.chromium.launch_persistent_context(
            user_data_dir=str(profile_dir.absolute()),
            headless=False,
            channel="chrome",
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--start-maximized",
                "--password-store=basic",
                "--profile-directory=Profile 1",
            ],
            no_viewport=True,
            viewport=None,
            locale="en-US"
        )
        
        page = await context.new_page()
        await page.goto("https://vids.google.com", wait_until="networkidle")
        
        print("Attesa del login da parte dell'utente...")
        print("Effettua l'accesso nella finestra di Chrome aperta sul tuo schermo.")
        
        while True:
            await asyncio.sleep(3)
            url = page.url
            try:
                # Se l'utente è su Google Vids ed è loggato (il pulsante Sign in / Accedi non c'è più)
                signin_btn = page.locator('a:has-text("Sign in"), a:has-text("Accedi"), button:has-text("Sign in"), button:has-text("Accedi")').first
                btn_count = await signin_btn.count()
                
                # Se siamo su Vids e non c'è il pulsante Accedi
                if ("docs.google.com" in url or "vids.google.com" in url) and btn_count == 0:
                    # Facciamo un'ulteriore verifica attendendo qualche secondo per stabilità
                    print("Rilevato login completato con successo!")
                    await asyncio.sleep(3)
                    break
            except Exception as e:
                pass
        
        print("Salvataggio sessione e chiusura in corso...")
        await context.close()
        print("\nREGISTRAZIONE COMPLETATA! Ora puoi avviare il server.")

if __name__ == "__main__":
    import sys
    account = sys.argv[1] if len(sys.argv) > 1 else "favamassimo"
    asyncio.run(login(account))
