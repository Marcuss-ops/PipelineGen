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
PROFILE_DIR = DATA_DIR / "google_slides_profile"
STORAGE_FILE = DATA_DIR / "google_slides_storage.json"

def main():
    os.makedirs(PROFILE_DIR, exist_ok=True)
    os.makedirs(DATA_DIR, exist_ok=True)

    print("==========================================================")
    print("      PipelineGen Google Chrome Session Generator         ")
    print("==========================================================")
    print(f"Profile Directory : {PROFILE_DIR}")
    print(f"Storage File      : {STORAGE_FILE}")
    print("\nLaunching browser for login...\n")

    with sync_playwright() as p:
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

        page = context.new_page()
        print("Navigating to Google Slides login...")
        page.goto("https://slides.new", wait_until="networkidle")

        print("\n--> PLEASE LOG IN TO YOUR GOOGLE ACCOUNT IN THE BROWSER WINDOW <--")
        print("Waiting for successful login detection...\n")

        login_success = False
        while not login_success:
            time.sleep(2)
            for p_page in context.pages:
                try:
                    url = p_page.url
                    if "docs.google.com/presentation" in url or "slides.google.com" in url or "myaccount.google.com" in url:
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
                if not login_success:
                    print("\n[!] Browser window closed before completing login.")
                    sys.exit(1)

        # Save storage state
        time.sleep(2)
        context.storage_state(path=str(STORAGE_FILE))
        print("==========================================================")
        print(f" SUCCESS: Google Session exported to: {STORAGE_FILE}")
        print(" You can now run all automated image generation scripts headless!")
        print("==========================================================")
        context.close()

if __name__ == "__main__":
    main()
