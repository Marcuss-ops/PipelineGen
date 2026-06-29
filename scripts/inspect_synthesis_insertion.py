import json
import time
from playwright.sync_api import sync_playwright

def main():
    print("Starting deep synthesis insertion inspection...")
    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context("data/google_slides_session_profile", headless=True)
        sf = "data/google_slides_storage.json"
        with open(sf) as f:
            context.add_cookies(json.load(f)["cookies"])
        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(5)
        
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

        ta = page.locator('.image-synthesis textarea, textarea').first
        ta.fill("Cyberpunk futuristic city")
        time.sleep(1)

        btn = page.locator('.image-synthesis-creation-button').first
        btn.click(force=True)

        print("Waiting 22s for generation...")
        time.sleep(22)

        page.screenshot(path="tmp/inspect_step1_generated.png")
        print("Saved tmp/inspect_step1_generated.png")

        # Dump all elements inside .image-synthesis or side panel
        print("--- DUMPING SYNTHESIS PANEL CHILD ELEMENTS ---")
        panel = page.locator('.image-synthesis, .workspace-labs-side-panel, [role="complementary"]').first
        children = panel.locator('*').all()
        print(f"Found {len(children)} total elements in panel.")
        for idx, ch in enumerate(children):
            try:
                tag = ch.evaluate("el => el.tagName")
                txt = ch.inner_text().strip().replace("\n", " ")
                aria = ch.get_attribute("aria-label") or ""
                role = ch.get_attribute("role") or ""
                cls = ch.get_attribute("class") or ""
                src = ch.get_attribute("src") if tag == "IMG" else ""
                if tag in ["BUTTON", "IMG", "DIV", "SPAN"] and (txt or aria or role or src):
                    print(f"[{idx}] tag={tag} | cls='{cls[:30]}' | role='{role}' | txt='{txt[:30]}' | aria='{aria}' | src='{src[:30]}'")
            except Exception:
                pass

        # Try dragging the image onto slide canvas or double clicking
        imgs = page.locator('.image-synthesis img, img[src*="googleusercontent"]').all()
        if len(imgs) > 0:
            target_img = imgs[0]
            print("Hovering image...")
            target_img.hover(force=True)
            time.sleep(1)
            page.screenshot(path="tmp/inspect_step2_hover.png")

            print("Clicking image...")
            target_img.click(force=True)
            time.sleep(1)
            page.screenshot(path="tmp/inspect_step3_clicked.png")

            # Try drag and drop from image to canvas center (x=600, y=400)
            print("Attempting drag_to canvas...")
            try:
                target_img.drag_to(page.locator("body"), target_position={"x": 600, "y": 400})
                time.sleep(2)
            except Exception as e:
                print(f"Drag note: {e}")

            page.screenshot(path="tmp/inspect_step4_dragged.png")

        context.close()

if __name__ == "__main__":
    main()
