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

def import_local_chrome_session():
    import shutil
    home = Path.home()
    chrome_profile = home / ".config" / "google-chrome"
    if not chrome_profile.exists():
        return False
    
    print("Found local Google Chrome profile. Copying session cookies and storage...")
    try:
        target_default = PROFILE_DIR / "Default"
        os.makedirs(target_default, exist_ok=True)
        
        local_state_src = chrome_profile / "Local State"
        if local_state_src.exists():
            shutil.copy(local_state_src, PROFILE_DIR / "Local State")
            
        cookies_src = chrome_profile / "Default" / "Cookies"
        if cookies_src.exists():
            shutil.copy(cookies_src, target_default / "Cookies")
            
        local_storage_src = chrome_profile / "Default" / "Local Storage"
        if local_storage_src.exists():
            target_storage = target_default / "Local Storage"
            if target_storage.exists():
                shutil.rmtree(target_storage)
            shutil.copytree(local_storage_src, target_storage)
            
        print("Local Chrome session imported successfully!")
        return True
    except Exception as e:
        print(f"Warning importing local Chrome session: {e}")
        return False

def main():
    os.makedirs(PROFILE_DIR, exist_ok=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    import_local_chrome_session()

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
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
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
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
            )

        context.add_init_script("delete navigator.__proto__.webdriver")
        page = context.new_page()
        print("Navigating to Google Slides login...")
        try:
            page.goto("https://slides.new", wait_until="domcontentloaded")
        except Exception as e:
            print(f"Navigation note ({e}), continuing to monitor login...")

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
                    from urllib.parse import urlparse
                    parsed = urlparse(url)
                    netloc = parsed.netloc.lower()
                    path = parsed.path.lower()
                    
                    is_google_domain = any(domain in netloc for domain in ["docs.google.com", "slides.google.com", "drive.google.com", "myaccount.google.com"])
                    is_clean_google = ("google.com" in netloc and "signin" not in netloc and "accounts" not in netloc)
                    
                    if (is_google_domain or is_clean_google) and "signin" not in path and "servicelogin" not in path and "signin" not in netloc and "accounts" not in netloc:
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
                print(f" WARNING: Browser closed before login detection succeeded.")
                print(" Google Session might not be authenticated.")
                print("==========================================================")
                sys.exit(1)

        # Save storage state on completion
        try:
            context.storage_state(path=str(STORAGE_FILE))
        except Exception:
            pass
        print("==========================================================")
        print(f" SUCCESS: Google Session exported to: {STORAGE_FILE}")
        print(" You can now run all automated image generation scripts headless!")
        print("==========================================================")
        try:
            context.close()
        except Exception:
            pass

if __name__ == "__main__":
    main()
