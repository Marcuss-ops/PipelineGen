import json
import time
from playwright.sync_api import sync_playwright

def main():
    print("Starting direct high-res AI image extraction test...")
    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context("data/google_slides_session_profile", headless=True)
        sf = "data/google_slides_storage.json"
        with open(sf) as f:
            context.add_cookies(json.load(f)["cookies"])
        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)
        
        print("Clicking insert-generated-image card...")
        btn = page.locator('button.insert-generated-image, [data-view-id="insert-generated-image"], div:has-text("Nano Banana Pro")').last
        btn.click(force=True)
        time.sleep(2)

        print("Filling prompt...")
        ta = page.locator('.image-synthesis textarea, textarea').first
        prompt = "Futuristic neon cyberpunk supercar racing at night, 8k cinematic masterpiece"
        ta.fill(prompt)
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
        if len(imgs) > 0:
            for idx, img in enumerate(imgs):
                src = img.get_attribute("src") or ""
                if "googleusercontent" in src or "blob:" in src:
                    print(f"Found direct AI image URL: {src[:80]}...")
                    # Fetch direct image content using browser authenticated context
                    response = page.request.get(src)
                    if response.status == 200:
                        image_bytes = response.body()
                        filename = f"tmp/DIRECT_REAL_AI_IMAGE_{idx}.png"
                        with open(filename, "wb") as f:
                            f.write(image_bytes)
                        print(f"SUCCESS! Downloaded direct real AI image ({len(image_bytes)} bytes) to {filename}")
                        break

        context.close()

if __name__ == "__main__":
    main()
