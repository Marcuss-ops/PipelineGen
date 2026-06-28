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
    parser.add_argument("--profile-dir", default="data/google_slides_profile", help="Path to persistent browser profile")
    parser.add_argument("--headful", action="store_true", help="Run browser in headful mode (needed for first login)")
    parser.add_argument("--screenshot-dir", default="tmp/slides_screenshots", help="Directory to save step screenshots")
    args = parser.parse_args()

    # Ensure directories exist
    os.makedirs(args.profile_dir, exist_ok=True)
    os.makedirs(args.screenshot_dir, exist_ok=True)

    with sync_playwright() as p:
        # Open browser with persistent context
        print(f"Launching browser with profile: {args.profile_dir} ...")
        context = p.chromium.launch_persistent_context(
            user_data_dir=args.profile_dir,
            channel="chrome",
            headless=not args.headful,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
            ],
            no_viewport=True
        )

        page = context.new_page()
        
        # Navigate to create new slide
        print("Navigating to slides.new ...")
        page.goto("https://slides.new", wait_until="networkidle")
        page.screenshot(path=os.path.join(args.screenshot_dir, "1_navigated.png"))

        # Check if we are on login screen
        if "signin" in page.url or "login" in page.url:
            print("Google Login required! Taking screenshot...")
            page.screenshot(path=os.path.join(args.screenshot_dir, "login_required.png"))
            if not args.headful:
                print("Error: Browser is headless. Re-run with --headful flag to log in manually.")
                context.close()
                sys.exit(1)
            
            # Wait for user to log in and reach Google Slides page
            print("Waiting for you to complete sign-in...")
            while "docs.google.com/presentation" not in page.url:
                time.sleep(2)
                if page.is_closed():
                    print("Error: Browser window closed before login completed.")
                    sys.exit(1)
            print("Login successful!")

        # Wait for presentation to load and interface to be active
        print("Waiting for presentation to load...")
        page.wait_for_selector("#docs-file-menu", timeout=30000)
        page.screenshot(path=os.path.join(args.screenshot_dir, "2_loaded.png"))

        # Let's locate the default title textbox and click it
        print("Locating title box...")
        # In Google Slides, the default layouts have textboxes. Let's look for editables.
        # Alternatively, we can insert a new text box or click on existing one.
        # Let's try to click on the main title placeholder:
        # Title placeholder often matches aria-label / role / text / class
        title_box = page.locator('text="Fai clic per aggiungere un titolo"').first
        if not title_box.is_visible():
            title_box = page.locator('text="Click to add title"').first

        if title_box.is_visible():
            print("Found title placeholder box. Clicking and typing...")
            title_box.click()
            page.keyboard.type(args.prompt)
        else:
            print("Title placeholder box not visible. Inserting a textbox via shortcut or typing...")
            # If no placeholder, we can select all and type or create textbox
            page.keyboard.press("Control+a")
            page.keyboard.type(args.prompt)

        time.sleep(1)
        page.screenshot(path=os.path.join(args.screenshot_dir, "3_typed.png"))

        # Trigger download via menus: File -> Download -> PNG image
        print("Opening File menu...")
        # File menu is typically id="docs-file-menu"
        file_menu = page.locator("#docs-file-menu")
        file_menu.click()
        page.screenshot(path=os.path.join(args.screenshot_dir, "4_file_menu.png"))

        print("Hovering over Download/Scarica menu...")
        # Download option has class docs-icon-img-container or text matching Download / Scarica
        download_item = page.locator('.apps-menuitem:has-text("Scarica"), .apps-menuitem:has-text("Download")').first
        download_item.hover()
        time.sleep(0.5)
        page.screenshot(path=os.path.join(args.screenshot_dir, "5_download_hover.png"))

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
        page.screenshot(path=os.path.join(args.screenshot_dir, "6_success.png"))

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
