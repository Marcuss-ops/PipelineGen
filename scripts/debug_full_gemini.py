import json
import time
from playwright.sync_api import sync_playwright

def main():
    print("Starting full gemini DOM debug...")
    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context("data/google_slides_session_profile", headless=True)
        sf = "data/google_slides_storage.json"
        with open(sf) as f:
            context.add_cookies(json.load(f)["cookies"])
        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)
        
        print("Clicking insert-generated-image button...")
        btn = page.locator('button.insert-generated-image, [data-view-id="insert-generated-image"]').first
        btn.click(force=True)
        time.sleep(3)

        print("Locating textarea...")
        ta = page.locator('textarea').first
        prompt = "Futuristic neon cyberpunk supercar racing at night, 8k cinematic"
        print(f"Filling prompt: '{prompt}'...")
        ta.fill(prompt)
        time.sleep(1)

        print("Submitting prompt with Enter and image-synthesis-creation-button...")
        ta.press("Enter")
        create_btn = page.locator('.image-synthesis-creation-button').first
        if create_btn.is_visible():
            create_btn.click(force=True)

        print("Waiting 22 seconds for generation...")
        time.sleep(22)

        # Save HTML to file to inspect exact DOM structure of generated images
        with open("tmp/full_page_debug.html", "w", encoding="utf-8") as hf:
            hf.write(page.content())
        print("Saved tmp/full_page_debug.html")

        context.close()

if __name__ == "__main__":
    main()
