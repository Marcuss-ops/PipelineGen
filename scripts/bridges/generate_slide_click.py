#!/usr/bin/env python3
import argparse
import os
import sys
import time
from playwright.sync_api import sync_playwright

def main():
    parser = argparse.ArgumentParser(description="Generate Google Slides image via Playwright click automation")
    parser.add_argument("--prompt", required=True, help="Text to insert into the slide")
    parser.add_argument("--output", required=True, help="Output PNG file path")
    parser.add_argument("--profile-dir", default="data/google_slides_session_profile", help="Path to persistent browser profile")
    parser.add_argument("--headful", action="store_true", help="Run browser in headful mode (needed for first login)")
    parser.add_argument("--use-system-chrome", action="store_true", help="Use active system Chrome profile and installation")
    args = parser.parse_args()

    profile_dir = args.profile_dir
    channel = None
    if args.use_system_chrome:
        local_app_data = os.environ.get("LOCALAPPDATA", "")
        if local_app_data:
            profile_dir = os.path.join(local_app_data, "Google", "Chrome", "User Data")
            channel = "chrome"

    # Ensure profile directory exists
    os.makedirs(profile_dir, exist_ok=True)

    with sync_playwright() as p:
        # Open browser with persistent context
        print(f"Launching browser with profile: {profile_dir} ...")
        storage_path = "data/google_slides_storage.json"
        kwargs = {
            "user_data_dir": profile_dir,
            "headless": not args.headful,
            "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            "args": [
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
                "--password-store=basic",
                "--use-mock-keychain",
            ],
            "no_viewport": True,
        }
        if channel:
            kwargs["channel"] = channel
        context = p.chromium.launch_persistent_context(**kwargs)

        if os.path.exists(storage_path):
            try:
                import json
                with open(storage_path, "r", encoding="utf-8") as sf:
                    sdata = json.load(sf)
                    if "cookies" in sdata:
                        context.add_cookies(sdata["cookies"])
            except Exception as e:
                print(f"Cookie load note: {e}")

        page = context.new_page()
        
        # Navigate to create new slide
        print("Navigating to slides.new ...")
        page.goto("https://slides.new", wait_until="domcontentloaded")

        # Check if we are on login screen
        if "signin" in page.url or "login" in page.url:
            print("Google Login required! Please log in inside the browser window.")
            if not args.headful:
                print("Error: Browser is headless. Re-run with --headful flag to log in manually.")
                context.close()
                sys.exit(1)
            
            # Wait for user to log in and reach Google Slides page
            print("Waiting for you to complete sign-in...")
            login_success = False
            while not login_success:
                time.sleep(2)
                for p_page in context.pages:
                    try:
                        if "docs.google.com/presentation" in p_page.url or "slides.google.com" in p_page.url:
                            print(f"Login successful! Detected on tab: {p_page.url}")
                            page = p_page
                            login_success = True
                            try:
                                context.storage_state(path="data/google_slides_storage.json")
                            except Exception:
                                pass
                            break
                    except Exception:
                        pass
                if not login_success:
                    # Check if all pages are closed
                    all_closed = True
                    for p_page in context.pages:
                        try:
                            if not p_page.is_closed():
                                all_closed = False
                                break
                        except Exception:
                            pass
                    if all_closed:
                        print("Error: Browser window closed before login completed.")
                        sys.exit(1)

        # Wait for presentation to load and interface to be active
        print("Waiting for presentation to load...")
        page.wait_for_selector("#docs-file-menu", timeout=30000)

        # Trigger AI Image Generation via Nano Banana Pro modal card or side panel
        print("Opening AI Image generation panel (Nano Banana Pro / Gemini)...")
        panel_opened = False
        try:
            card = page.locator('div:has-text("Nano Banana Pro"), div:has-text("studio-quality visuals"), div:has-text("Images")').last
            card.wait_for(state="visible", timeout=3000)
            card.click()
            panel_opened = True
            time.sleep(2)
        except Exception:
            pass

        if not panel_opened:
            # Dismiss any blocking popups if Nano Banana Pro card was not present
            try:
                page.keyboard.press("Escape")
                time.sleep(0.5)
            except Exception:
                pass

            try:
                labs_btn = page.locator('#workspace-labs-button, [aria-label*="Gemini"], [aria-label*="visualize"], [title*="Gemini"]').first
                labs_btn.wait_for(state="visible", timeout=3000)
                labs_btn.click()
                panel_opened = True
                time.sleep(2)
            except Exception:
                pass

        if not panel_opened:
            try:
                insert_menu = page.locator("#docs-insert-menu")
                insert_menu.click()
                time.sleep(0.5)
                img_item = page.locator('.apps-menuitem:has-text("Immagine"), .apps-menuitem:has-text("Image")').first
                img_item.hover()
                time.sleep(0.5)
                ai_item = page.locator('.apps-menuitem:has-text("Gemini"), .apps-menuitem:has-text("IA"), .apps-menuitem:has-text("Crea")').first
                ai_item.click()
                time.sleep(2)
            except Exception as e:
                print(f"Note opening insert menu AI: {e}")

        # Enter prompt in AI generator panel textarea/input
        print(f"Entering AI image prompt: '{args.prompt}'...")
        try:
            prompt_input = page.locator('textarea[placeholder*="Descrivi la tua idea"], textarea[placeholder*="Describe your idea"], textarea').first
            prompt_input.wait_for(state="visible", timeout=10000)
            prompt_input.click()
            prompt_input.fill(args.prompt)
            time.sleep(0.5)

            # Select 16:9 aspect ratio if available
            print("Selecting 16:9 aspect ratio...")
            try:
                aspect_btn = page.locator('button:has-text("16:9"), [aria-label*="16:9"], div:has-text("16:9"), span:has-text("16:9")').first
                if aspect_btn.is_visible():
                    aspect_btn.click()
                    time.sleep(0.5)
            except Exception as e:
                print(f"Note selecting 16:9 aspect ratio: {e}")

            # Click Create / Genera button or press Enter
            print("Submitting prompt (Clicking Create/Genera & pressing Enter)...")
            try:
                create_btn = page.locator('button:has-text("Crea"), button:has-text("Create"), button:has-text("Genera")').last
                if create_btn.is_visible():
                    create_btn.click()
                else:
                    prompt_input.press("Enter")
            except Exception:
                prompt_input.press("Enter")

            print("Waiting for AI image generation to complete (15s)...")
            time.sleep(15)

            # Click on generated image thumbnail to insert onto slide canvas
            print("Inserting generated AI image onto slide canvas...")
            gen_imgs = page.locator('[role="button"] img, .goog-modalpopup img, div img').all()
            for img in gen_imgs:
                try:
                    src = img.get_attribute("src") or ""
                    if "blob:" in src or "googleusercontent" in src or "data:image" in src:
                        img.click()
                        time.sleep(2)
                        break
                except Exception:
                    pass
        except Exception as e:
            print(f"AI prompt input wait note ({e}). Typing prompt into slide...")
            page.keyboard.type(args.prompt)

        time.sleep(1)

        # Trigger download via menus: File -> Download -> PNG image
        print("Opening File menu...")
        # File menu is typically id="docs-file-menu"
        file_menu = page.locator("#docs-file-menu")
        file_menu.click()

        print("Hovering over Download/Scarica menu...")
        # Download option has class docs-icon-img-container or text matching Download / Scarica
        download_item = page.locator('.apps-menuitem:has-text("Scarica"), .apps-menuitem:has-text("Download")').first
        download_item.hover()
        time.sleep(0.5)

        print("Clicking PNG image option...")
        # PNG option contains text "PNG"
        png_item = page.locator('.apps-menuitem:has-text("PNG")').first
        
        # Start waiting for download before clicking
        with page.expect_download() as download_info:
            png_item.click()
        
        download = download_info.value
        print("Download started. Saving file...")
        download.save_as(args.output)
        print(f"Success! Slide exported to: {args.output}")

        # Optional: delete presentation to avoid cluttering Drive
        # Click File -> Move to trash / Sposta nel cestino
        try:
            print("Cleaning up presentation (moving to trash)...")
            file_menu.click()
            trash_item = page.locator('.apps-menuitem:has-text("cestino"), .apps-menuitem:has-text("trash")').first
            trash_item.click()
            # Confirm trash popup if visible
            confirm_btn = page.locator('.docs-butterbar-action:has-text("Cestino"), .docs-butterbar-action:has-text("Trash")').first
            if confirm_btn.is_visible():
                confirm_btn.click()
            time.sleep(1)
        except Exception as e:
            print(f"Cleanup warning: {e}")

        context.close()

if __name__ == "__main__":
    main()
