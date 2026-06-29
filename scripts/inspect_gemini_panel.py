import json
import time
from playwright.sync_api import sync_playwright

def main():
    print("Starting inspection script...")
    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context("data/google_slides_session_profile", headless=True)
        sf = "data/google_slides_storage.json"
        with open(sf) as f:
            context.add_cookies(json.load(f)["cookies"])
        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)
        
        print("Clicking Nano Banana Pro card...")
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

        tas = page.locator("textarea").all()
        if len(tas) > 0:
            ta = tas[0]
            prompt = "Futuristic neon car"
            ta.fill(prompt)
            time.sleep(1)

            # Look for 16:9 aspect ratio
            aspect_els = page.locator('*:has-text("16:9")').all()
            for el in aspect_els:
                try:
                    txt = el.inner_text().strip()
                    if txt == "16:9":
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

            print("Waiting 22 seconds for AI generation...")
            time.sleep(22)
            page.screenshot(path="tmp/debug_panel_after_gen.png")

            # Print all buttons, images, and text inside the panel
            print("--- DUMPING PANEL INTERACTIVE ELEMENTS ---")
            elements = page.locator('[role="button"], button, img, div[tabindex]').all()
            for idx, el in enumerate(elements):
                try:
                    txt = el.inner_text().strip().replace("\n", " ")
                    aria = el.get_attribute("aria-label") or ""
                    title = el.get_attribute("title") or ""
                    tag = el.evaluate("el => el.tagName")
                    src = el.get_attribute("src") if tag == "IMG" else ""
                    if txt or aria or title or src:
                        print(f"[{idx}] tag={tag} | txt='{txt[:40]}' | aria='{aria}' | title='{title}' | src='{src[:30]}'")
                except Exception:
                    pass

        context.close()

if __name__ == "__main__":
    main()
