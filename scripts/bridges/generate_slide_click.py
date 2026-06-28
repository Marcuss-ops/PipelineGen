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

        # Dismiss any Gemini / getting started modal popups that intercept clicks
        time.sleep(1)
        try:
            page.keyboard.press("Escape")
            time.sleep(0.5)
            close_btn = page.locator('.goog-modalpopup [aria-label="Chiudi"], .goog-modalpopup [aria-label="Close"], .goog-modalpopup-close').first
            if close_btn.is_visible():
                print("Dismissing Gemini / Getting Started modal popup...")
                close_btn.click()
        except Exception:
            pass

        # Trigger AI Image Generation via Gemini / Labs side panel or Insert menu
        print("Opening AI Image generation panel (Gemini / Nano Banana Pro)...")
        ai_panel_opened = False

        # Try clicking toolbar AI image button first
        try:
            ai_btn = page.locator('[aria-label*="Crea immagine"], [aria-label*="Create image"], [aria-label*="visualize"], [title*="Crea immagine"]').first
            if ai_btn.is_visible():
                ai_btn.click()
                ai_panel_opened = True
        except Exception:
            pass

        # If not opened via toolbar, use Insert menu: Inserisci -> Immagine -> Crea immagine...
        if not ai_panel_opened:
            try:
                insert_menu = page.locator("#docs-insert-menu")
                insert_menu.click()
                time.sleep(0.5)
                img_menu_item = page.locator('.apps-menuitem:has-text("Immagine"), .apps-menuitem:has-text("Image")').first
                img_menu_item.hover()
                time.sleep(0.5)
                ai_img_item = page.locator('.apps-menuitem:has-text("Gemini"), .apps-menuitem:has-text("IA"), .apps-menuitem:has-text("Crea")').first
                ai_img_item.click()
                ai_panel_opened = True
            except Exception as e:
                print(f"Note opening AI menu: {e}")

        time.sleep(2)

        # Enter prompt in AI generator panel textarea/input
        print(f"Entering AI image prompt: '{args.prompt}'...")
        prompt_input = page.locator('textarea[placeholder*="Crea"], textarea[placeholder*="Create"], textarea[aria-label*="prompt"], textarea').first
        if prompt_input.is_visible():
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

            print("Waiting for AI image generation to complete (approx 10-15s)...")
            time.sleep(12)

            # Click on generated image thumbnail in side panel to insert into slide
            print("Inserting generated AI image onto slide canvas...")
            gen_img = page.locator('.side-panel img, .workspace-labs-side-panel img, [role="button"] img').first
            if gen_img.is_visible():
                gen_img.click()
                time.sleep(2)
        else:
            print("AI prompt input not found directly. Typing prompt into slide...")
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
