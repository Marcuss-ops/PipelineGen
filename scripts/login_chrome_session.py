#!/usr/bin/env python3
"""
Dedicated Chrome Google Session Login & Storage Saver for PipelineGen.
Run this script to log in once interactively and persist your session cookies.
"""
import os
import sys
import time
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parent.parent
DATA_DIR = ROOT / "data"
PROFILE_DIR = DATA_DIR / "google_slides_session_profile"
STORAGE_FILE = DATA_DIR / "google_slides_storage.json"

def main():
    os.makedirs(PROFILE_DIR, exist_ok=True)
    os.makedirs(DATA_DIR, exist_ok=True)

    # Clean up stale chromium lock files to prevent Error code 32 (ProcessSingleton lock)
    for lock_name in ["lockfile", "SingletonLock", "SingletonCookie", "SingletonSocket"]:
        lock_path = PROFILE_DIR / lock_name
        if lock_path.exists():
            try:
                os.remove(lock_path)
            except Exception:
                pass

    print("==========================================================")
    print("      PipelineGen Google Chrome Session Generator         ")
    print("==========================================================")
    print(f"Profile Directory : {PROFILE_DIR}")
    print(f"Storage File      : {STORAGE_FILE}")
    print("\nLaunching browser for login...\n")

    with sync_playwright() as p:
        try:
            context = p.chromium.launch_persistent_context(
                user_data_dir=str(PROFILE_DIR),
                headless=False,
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
                args=[
                    "--disable-blink-features=AutomationControlled",
                    "--no-sandbox",
                    "--disable-setuid-sandbox",
                ],
                no_viewport=True,
            )
        except Exception as e:
            print(f"Persistent context launch warning ({e}). Launching clean browser instance...")
            browser = p.chromium.launch(
                headless=False,
                args=["--disable-blink-features=AutomationControlled", "--no-sandbox"],
            )
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
            )

        page = context.new_page()
        print("Navigating to Google Slides login...")
        page.goto("https://slides.new", wait_until="networkidle")

        print("\n--> PLEASE LOG IN TO YOUR GOOGLE ACCOUNT IN THE BROWSER WINDOW <--")
        print("Waiting for successful login detection...\n")

        login_success = False
        while not login_success:
            time.sleep(1)
            # Continuously dump storage state so cookies are preserved even if closed early
            try:
                context.storage_state(path=str(STORAGE_FILE))
            except Exception:
                pass

            for p_page in context.pages:
                try:
                    url = p_page.url
                    url_lower = url.lower()
                    if ("docs.google.com" in url_lower or "slides.google.com" in url_lower or "drive.google.com" in url_lower or "myaccount.google.com" in url_lower or ("google.com" in url_lower and "signin" not in url_lower and "servicelogin" not in url_lower)):
                        print(f"SUCCESS! Login detected on tab: {url}")
                        login_success = True
                        break
                except Exception:
                    pass
            
            # Check if user closed browser
            all_closed = True
            for p_page in context.pages:
                try:
                    if not p_page.is_closed():
                        all_closed = False
                        break
                except Exception:
                    pass
            if all_closed:
                # Save storage state right before exit
                try:
                    context.storage_state(path=str(STORAGE_FILE))
                except Exception:
                    pass
                print("==========================================================")
                print(f" SUCCESS: Google Session exported to: {STORAGE_FILE}")
                print(" You can now run all automated image generation scripts headless!")
                print("==========================================================")
                sys.exit(0)

        # Save storage state on completion
        try:
            context.storage_state(path=str(STORAGE_FILE))
        except Exception:
            pass
        print("==========================================================")
        print(f" SUCCESS: Google Session exported to: {STORAGE_FILE}")
        print(" You can now run all automated image generation scripts headless!")
        print("==========================================================")
        context.close()

if __name__ == "__main__":
    main()
