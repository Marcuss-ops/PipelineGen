#!/usr/bin/env python3
import os
import json
import sys
from playwright.sync_api import sync_playwright

MASTER_STORAGE = "data/google_slides_storage.json"
PROFILE_DIR = "data/google_slides_session_profile"

def main():
    print("==================================================================")
    print(" Google Slides Login Helper")
    print("==================================================================")
    print("Avvio del browser in corso...")
    
    with sync_playwright() as p:
        os.makedirs(PROFILE_DIR, exist_ok=True)
        context = p.chromium.launch_persistent_context(
            PROFILE_DIR,
            headless=False,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
            ],
        )
        page = context.new_page()
        print("Navigazione su https://slides.new...")
        page.goto("https://slides.new")
        
        print("\n--> EFFETTUA IL LOGIN nella finestra del browser che si è aperta.")
        print("--> Una volta completato il login e caricata la pagina di Google Slides,")
        print("    torna qui sul terminale e premi INVIO per salvare la sessione.")
        
        try:
            input()
        except KeyboardInterrupt:
            print("\nOperazione annullata.")
            context.close()
            sys.exit(1)
            
        storage = context.storage_state()
        
        # Save both to MASTER_STORAGE and profile storage
        with open(MASTER_STORAGE, "w") as f:
            json.dump(storage, f, indent=2)
            
        profile_storage_path = f"{MASTER_STORAGE}.profile_0"
        with open(profile_storage_path, "w") as f:
            json.dump(storage, f, indent=2)
            
        print("==================================================================")
        print(f"✅ Sessione salvata con successo!")
        print(f"   - {MASTER_STORAGE}")
        print(f"   - {profile_storage_path}")
        print("==================================================================")
        context.close()

if __name__ == "__main__":
    main()
