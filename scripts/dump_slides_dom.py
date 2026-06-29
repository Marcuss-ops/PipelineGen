import json
import time
from playwright.sync_api import sync_playwright

def main():
    print("Launching test browser...")
    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context("data/google_slides_session_profile", headless=True)
        sf = "data/google_slides_storage.json"
        with open(sf) as f:
            context.add_cookies(json.load(f)["cookies"])
        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)
        
        print("Locating Nano Banana Pro card...")
        cards = page.locator('div:has-text("Nano Banana Pro")').all()
        if len(cards) > 0:
            for c in cards:
                try:
                    if c.is_visible():
                        c.click(force=True)
                        time.sleep(2)
                        break
                except Exception:
                    pass

        print("Locating prompt textarea...")
        tas = page.locator("textarea").all()
        if len(tas) > 0:
            ta = tas[0]
            prompt = "Cyberpunk neon futuristic city at night, 8k resolution cinematic masterpiece"
            print(f"Filling prompt: '{prompt}'...")
            ta.fill(prompt)
            time.sleep(1)

            # Look for 16:9 aspect ratio buttons or pills
            print("Selecting 16:9 aspect ratio...")
            aspect_els = page.locator('*:has-text("16:9")').all()
            for el in aspect_els:
                try:
                    txt = el.inner_text().strip()
                    if txt == "16:9":
                        print("Found exact '16:9' element, clicking...")
                        el.click(force=True)
                        time.sleep(0.5)
                        break
                except Exception:
                    pass

            print("Submitting prompt...")
            ta.press("Enter")
            btns = page.locator('button:has-text("Crea"), button:has-text("Create"), button:has-text("Genera")').all()
            if len(btns) > 0:
                try:
                    btns[-1].click(force=True)
                except Exception:
                    pass

            print("Waiting 20 seconds for AI image generation...")
            time.sleep(20)

            # Insert image onto slide canvas: click generated thumbnails in side panel
            print("Locating generated AI images in panel...")
            # Look for images inside side panel or dialog
            gen_imgs = page.locator('img[src*="googleusercontent"], img[src*="blob:"], [role="button"] img').all()
            print(f"Found {len(gen_imgs)} candidate generated image elements.")
            if len(gen_imgs) > 0:
                # Click the first generated thumbnail to insert onto slide canvas
                for g_img in gen_imgs:
                    try:
                        print("Clicking generated AI image thumbnail...")
                        g_img.click(force=True)
                        time.sleep(1)
                        # Press Enter or click to confirm insertion if needed
                        g_img.dblclick(force=True)
                        time.sleep(2)
                        break
                    except Exception as e:
                        print(f"Error clicking thumbnail: {e}")

            time.sleep(3)

            # Download slide PNG
            print("Downloading slide PNG...")
            file_menu = page.locator("#docs-file-menu")
            file_menu.click()
            time.sleep(1)
            download_item = page.locator('.apps-menuitem:has-text("Scarica"), .apps-menuitem:has-text("Download")').first
            download_item.hover()
            time.sleep(1)
            png_item = page.locator('.apps-menuitem:has-text("PNG")').first
            with page.expect_download() as download_info:
                png_item.click()
            download = download_info.value
            download.save_as("tmp/REAL_AI_CYBERPUNK_169.png")
            print("SUCCESS! Saved real 16:9 AI image to tmp/REAL_AI_CYBERPUNK_169.png")

        context.close()

if __name__ == "__main__":
    main()
