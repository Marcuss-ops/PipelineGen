import argparse
import json
import os
import sys
import time
from playwright.sync_api import sync_playwright

def main():
    parser = argparse.ArgumentParser(description="Generate image via Google Slides AI (Nano Banana Pro / Gemini)")
    parser.add_argument("--prompt", required=True, help="Prompt for AI image generation")
    parser.add_argument("--output", required=True, help="Output PNG file path")
    parser.add_argument("--headful", action="store_true", help="Run browser in headful mode")
    args = parser.parse_args()

    os.makedirs(os.path.dirname(os.path.abspath(args.output)), exist_ok=True)

    print("Launching browser with session profile...")
    with sync_playwright() as p:
        sf = "data/google_slides_storage.json"
        storage_state_arg = sf if os.path.exists(sf) else None

        context = p.chromium.launch_persistent_context(
            "data/google_slides_session_profile",
            headless=not args.headful,
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
            ]
        )
        context.add_init_script("delete navigator.__proto__.webdriver")
        page = context.new_page()
        print("Navigating to slides.new...")
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)

        try:
            print("Clicking insert-generated-image card...")
            btn = page.locator('button.insert-generated-image, [data-view-id="insert-generated-image"], div:has-text("Nano Banana Pro")').last
            btn.click(force=True)
            time.sleep(2)
        except Exception as e:
            os.makedirs("tmp", exist_ok=True)
            page.screenshot(path="tmp/failure_screenshot.png")
            print("Saved failure screenshot to tmp/failure_screenshot.png")
            raise e

        print(f"Entering AI image prompt: '{args.prompt}'...")
        ta = page.locator('.image-synthesis textarea, textarea').first
        ta.fill(args.prompt)
        time.sleep(1)

        print("Selecting 16:9 aspect ratio...")
        try:
            prop_btn = page.locator('[aria-label="Proporzioni"], .image-synthesis [aria-label*="Proporzi"]').first
            if prop_btn.is_visible():
                prop_btn.click(force=True)
                time.sleep(0.5)
                opt_169 = page.locator('*:has-text("16:9")').last
                opt_169.click(force=True)
                time.sleep(0.5)
        except Exception as e:
            print(f"Note selecting proportions: {e}")

        print("Submitting prompt...")
        create_btn = page.locator('.image-synthesis-creation-button, button[aria-label="Crea"]').first
        create_btn.click(force=True)

        print("Waiting 22 seconds for AI generation...")
        time.sleep(22)

        # Extract direct image URL from generated element
        print("Locating generated AI images...")
        imgs = page.locator('.docs-content-library-image-generation-item img, img[src*="googleusercontent"]').all()
        print(f"Found {len(imgs)} candidate image elements.")
        saved_direct = False
        if len(imgs) > 0:
            for idx, img in enumerate(imgs):
                src = img.get_attribute("src") or ""
                if "googleusercontent" in src or "blob:" in src:
                    print(f"Fetching direct AI image artwork from URL...")
                    response = page.request.get(src)
                    if response.status == 200:
                        image_bytes = response.body()
                        with open(args.output, "wb") as f:
                            f.write(image_bytes)
                        print(f"Success! Real AI image exported to: {args.output} ({len(image_bytes)} bytes)")
                        saved_direct = True
                        break

        if not saved_direct:
            print("Fallback: Triggering download via File menu...")
            file_menu = page.locator("#docs-file-menu")
            file_menu.click()
            download_item = page.locator('.apps-menuitem:has-text("Scarica"), .apps-menuitem:has-text("Download")').first
            download_item.hover()
            time.sleep(0.5)
            png_item = page.locator('.apps-menuitem:has-text("PNG")').first
            with page.expect_download() as download_info:
                png_item.click()
            download = download_info.value
            download.save_as(args.output)
            print(f"Success! Slide exported to: {args.output}")

        context.close()

if __name__ == "__main__":
    main()
