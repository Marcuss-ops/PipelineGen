import asyncio
import os
import subprocess
import sys
import time
from pathlib import Path
from playwright.async_api import async_playwright

# Aggiungi cartella google-accounting al python path per gli import
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

from automation.vids_images import GoogleVidsImagesMixin
from drive_client import auto_upload_to_drive
from config import DOWNLOAD_DIR, DEFAULT_IMAGES_DRIVE_FOLDER_ID, VIDS_PROJECT_ID as _cfg_vids_pid

class CDPBot(GoogleVidsImagesMixin):
    def __init__(self, page):
        self._external_page = True
        self.page = page
        self._sessions = {}

async def main():
    profile_path = "/home/pierone/snap/chromium/common/chromium"
    snap_executable = "/snap/bin/chromium"
    video_id = _cfg_vids_pid
    url = f"https://docs.google.com/videos/d/{video_id}/edit?scene=id.g57c7d542_0_0"
    prompt = "A beautiful cute fluffy orange cat wearing a tiny golden crown in a medieval library, oil painting style"
    drive_folder_id = DEFAULT_IMAGES_DRIVE_FOLDER_ID or ""
    
    print("="*70)
    print("AVVIO AUTOMAZIONE DIRETTA COMPLETA (SNAP CHROMIUM)")
    print(f"Profilo: {profile_path}")
    print(f"Progetto ID: {video_id}")
    print(f"Prompt: {prompt}")
    print(f"Drive Folder ID: {drive_folder_id}")
    print("="*70)
    
    # 1. Chiudiamo in modo sicuro qualsiasi Chromium attivo per rilasciare la sessione
    print("Chiusura di eventuali istanze attive di Chromium...")
    os.system("killall -9 chrome chromium-browser chromium 2>/dev/null")
    await asyncio.sleep(2)
    
    # Pulizia lock residui
    for lock_name in ["SingletonLock", "SingletonSocket", "SingletonCookie"]:
        lock_file = Path(profile_path) / lock_name
        if lock_file.exists() or lock_file.is_symlink():
            try:
                if lock_file.is_symlink():
                    lock_file.unlink()
                else:
                    os.unlink(str(lock_file))
            except Exception:
                pass

    # 2. Avviamo Chromium in modalità UMANA con debug port attiva sul DISPLAY :10
    print("Avvio di Chromium in modalità debug nativa...")
    env = os.environ.copy()
    env["DISPLAY"] = ":10"
    
    proc = subprocess.Popen([
        snap_executable,
        "--remote-debugging-port=9222",
        f"--user-data-dir={profile_path}",
        "--password-store=basic",
        "--profile-directory=Profile 1",
        "--no-first-run",
        "--start-maximized",
        url
    ], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    
    print("Attesa avvio browser (6 secondi)...")
    await asyncio.sleep(6)
    
    # 3. Ci connettiamo alla sessione attiva tramite Playwright CDP
    async with async_playwright() as p:
        print("Connessione in corso a Chromium tramite CDP...")
        try:
            browser = await p.chromium.connect_over_cdp("http://localhost:9222")
            context = browser.contexts[0]
            page = context.pages[0]
            print(f"Connesso con successo! URL attuale: {page.url}")
            
            print("Attesa caricamento dell'editor di Google Vids (10 secondi)...")
            await asyncio.sleep(10)
            
            bot = CDPBot(page)
            
            # STEP 1: Clicchiamo sul tab Immagini
            print("Step 1: Cliccando sul tab 'Immagini'...")
            immagini_tab_selectors = [
                '#content-library-rail-image-synthesis-element',
                'text="Immagine"',
                'text="Immagini"',
                'text="Images"',
                '[aria-label*="Immagine" i]',
                '[aria-label*="Images" i]',
            ]
            
            clicked = False
            for sel in immagini_tab_selectors:
                try:
                    btn = page.locator(sel).first
                    if await btn.count() > 0:
                        await btn.click(timeout=3000)
                        clicked = True
                        print(f"Tab aperto con successo via: {sel}")
                        break
                except Exception:
                    continue
            
            if not clicked:
                print("Impossibile aprire il tab automaticamente, prova ad aprirlo tu sul browser.")
                await asyncio.sleep(5)
                
            await asyncio.sleep(3)
            
            # STEP 2: Impostiamo il rapporto 16:9
            print("Step 2: Impostazione rapporto 16:9...")
            ratio_set = await bot._set_aspect_ratio_16_9(page)
            print(f"Risultato impostazione rapporto: {ratio_set}")
            await asyncio.sleep(2)
            
            # STEP 3: Inseriamo il prompt carattere per carattere (Human Typing)
            print("Step 3: Digitazione umana del prompt in corso...")
            try:
                ta = page.locator('textarea.docs-prompt-view-input, textarea.docs-image-synthesis-search-bar-input').first
                await ta.click(timeout=3000)
                await ta.focus()
                
                # Cancelliamo eventuale testo precedente
                await page.keyboard.press("Control+A")
                await page.keyboard.press("Backspace")
                await asyncio.sleep(0.5)
                
                # Digitazione reale lettera per lettera
                import random
                for char in prompt:
                    await page.keyboard.type(char)
                    await asyncio.sleep(random.randint(15, 45) / 1000.0)
                
                print("Prompt digitato interamente!")
                await asyncio.sleep(1.5)
            except Exception as e:
                print(f"Errore digitazione prompt: {e}")
                
            # STEP 4: Clicchiamo Genera/Crea
            print("Step 4: Avvio della generazione immagine (Click su Crea)...")
            try:
                gen_btn = page.locator('button.image-synthesis-creation-button.image-synthesis-begin-create, button:has-text("Create"), button:has-text("Crea"), button:has-text("Genera")').first
                await gen_btn.click(timeout=5000)
                print("[SUCCESS] Click su 'Crea' inviato con successo!")
            except Exception as e:
                print(f"Errore click genera: {e}")
                
            # STEP 5: Polling e scaricamento dell'immagine generata
            print("Step 5: In attesa della generazione dell'immagine e scaricamento...")
            final_path = await bot._poll_and_download_image(page, video_id, timeout_ms=300000)
            
            if final_path and Path(final_path).exists():
                print(f"\n[OK] Immagine generata e scaricata localmente in: {final_path}")
                
                # STEP 6: Caricamento automatico su Google Drive
                if drive_folder_id:
                    print(f"Caricamento dell'immagine su Google Drive (Folder ID: {drive_folder_id})...")
                    drive_res = await auto_upload_to_drive(str(final_path), drive_folder_id, "image")
                    if drive_res:
                        print(f"[SUCCESS] Immagine caricata su Drive! File ID: {drive_res.get('drive_file_id')}")
            else:
                print("\n[WARNING] Impossibile scaricare l'immagine o timeout scaduto.")
                
            print("\nBrowser mantenuto aperto sullo schermo per l'ispezione dell'utente.")
            # await context.close()
            
        except Exception as e:
            print(f"Errore generale durante l'automazione CDP: {e}")
            
if __name__ == "__main__":
    asyncio.run(main())
