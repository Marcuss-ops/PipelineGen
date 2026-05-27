"""Debug script: apre Vids in HEADED mode, esplora gli avatar disponibili,
scarica le immagini di riferimento e testa upload con setInputFiles."""
import asyncio
import json
import time
from pathlib import Path
from playwright.async_api import async_playwright

SESSION = "google-accounting/sessions/favamassimo.json"
OUTPUT = Path("google-accounting/debug/avatars_downloaded")
VIDS_URL = "https://docs.google.com/videos/d/1iprXbBW73jSfPpP87k6Q3KJTBj9lxFkIOgoDUOC0yGY/edit"


async def run():
    OUTPUT.mkdir(parents=True, exist_ok=True)

    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=False)  # HEADED per debug
    context = await browser.new_context(
        storage_state=SESSION,
        viewport={"width": 1920, "height": 1080},
        user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    )
    page = await context.new_page()

    try:
        # 1. Navigate to Vids project
        print("=== Apertura progetto Vids ===")
        await page.goto(VIDS_URL, wait_until="domcontentloaded")
        await asyncio.sleep(35)
        await page.screenshot(path=str(OUTPUT / "01_loaded.png"))
        print("Screenshot: 01_loaded.png")

        # 2. Dismiss dialogs
        for _ in range(3):
            await page.keyboard.press("Escape")
            await asyncio.sleep(0.5)
        await page.screenshot(path=str(OUTPUT / "02_dialogs_dismissed.png"))
        print("Screenshot: 02_dialogs_dismissed.png")

        # 3. Click Avatar button
        print("\n=== Click pulsante Avatar ===")
        avatar_btn = page.locator('#content-library-rail-avatars-element').first
        if await avatar_btn.count() > 0:
            await avatar_btn.click(timeout=15000)
            print("Avatar button clicked!")
        else:
            # Fallback: try other selectors
            for sel in ['div[aria-label="Avatar AI"]', 'button:has-text("Avatar AI")', '[role="button"]:has-text("Avatar")']:
                loc = page.locator(sel).first
                if await loc.count() > 0:
                    await loc.click(timeout=10000)
                    print(f"Avatar button clicked via: {sel}")
                    break
            else:
                print("AVATAR BUTTON NOT FOUND!")
                await page.screenshot(path=str(OUTPUT / "03_error_no_avatar_btn.png"))
                return

        await asyncio.sleep(5)
        await page.screenshot(path=str(OUTPUT / "03_avatar_panel_open.png"))
        print("Screenshot: 03_avatar_panel_open.png")

        # 4. Click "Cambia" to see available avatars
        print("\n=== Click Cambia avatar ===")
        change_btn = page.locator('div[role="button"]:has-text("Cambia")').first
        if await change_btn.count() > 0:
            await change_btn.click()
            await asyncio.sleep(5)
            await page.screenshot(path=str(OUTPUT / "04_avatar_selection_open.png"))
            print("Screenshot: 04_avatar_selection_open.png")
        else:
            print("NO 'Cambia' button found!")
            return

        # 5. Find all avatar radio buttons / options
        print("\n=== Ricerca avatar disponibili ===")
        avatar_options = await page.locator('div[role="radio"]').all()
        print(f"Trovati {len(avatar_options)} avatar radio buttons")

        avatar_data = []
        for i, opt in enumerate(avatar_options):
            try:
                aria_label = await opt.get_attribute("aria-label") or ""
                text = (await opt.inner_text()).strip()
                print(f"  Avatar {i}: aria='{aria_label}' text='{text[:60]}'")

                # Try to find the avatar's image element inside this option
                img = opt.locator("img").first
                if await img.count() > 0:
                    img_src = await img.get_attribute("src") or ""
                    print(f"    image src: {img_src[:120]}")

                    if img_src:
                        # Extract avatar ID from aria-label or text
                        avatar_id = aria_label or text
                        ext = img_src.split(".")[-1].split("?")[0] if "?" in img_src else img_src.split(".")[-1]
                        if ext not in ("jpg", "jpeg", "png", "webp"):
                            ext = "png"

                        # Download the image
                        import urllib.request
                        img_path = OUTPUT / f"avatar_{i}_{avatar_id.replace(' ','_')}.{ext}"
                        try:
                            req = urllib.request.Request(img_src, headers={
                                "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
                                "Accept": "image/webp,image/apng,image/*,*/*;q=0.8",
                            })
                            with urllib.request.urlopen(req, timeout=15) as resp:
                                img_path.write_bytes(resp.read())
                            print(f"    DOWNLOADED -> {img_path} ({img_path.stat().st_size} bytes)")
                            avatar_data.append({
                                "id": avatar_id,
                                "text": text,
                                "file": str(img_path),
                                "size": img_path.stat().st_size,
                            })
                        except Exception as e:
                            print(f"    Download FAILED: {e}")
                else:
                    print("    No img found inside this option")

                # Click it to see details (but don't select it permanently)
                if i == 0:
                    await opt.click()
                    await asyncio.sleep(3)
                    await page.screenshot(path=str(OUTPUT / f"05_avatar_{i}_selected.png"))
            except Exception as e:
                print(f"  Error on avatar {i}: {e}")

        # Save metadata
        if avatar_data:
            meta = {"avatars": avatar_data, "timestamp": int(time.time())}
            meta_path = OUTPUT / "avatars_metadata.json"
            meta_path.write_text(json.dumps(meta, indent=2))
            print(f"\nMetadata salvati in {meta_path}")

        # 6. Trova l'input file nascosto per testare setInputFiles
        print("\n=== Ricerca input[type=file] per upload ===")
        file_inputs = await page.locator('input[type="file"]').all()
        print(f"Trovati {len(file_inputs)} input[type=file]")

        for i, fi in enumerate(file_inputs):
            try:
                attrs = await fi.evaluate("""el => ({
                    id: el.id,
                    accept: el.accept,
                    class: el.className,
                    style: el.style.display,
                    parent: el.parentElement ? el.parentElement.className : 'none',
                    outer: el.outerHTML.substring(0, 200)
                })""")
                print(f"  Input {i}: {json.dumps(attrs, indent=4)}")
            except Exception as e:
                print(f"  Input {i}: error={e}")

        # 7. Test upload con setInputFiles
        print("\n=== Test upload immagine con setInputFiles ===")
        test_img = OUTPUT / "avatar_0_.png"
        if test_img.exists() and file_inputs:
            target = file_inputs[0]
            try:
                await target.set_input_files(str(test_img))
                print("setInputFiles RIUSCITO!")
                await asyncio.sleep(5)
                await page.screenshot(path=str(OUTPUT / "06_after_upload.png"))
            except Exception as e:
                print(f"setInputFiles fallito: {e}")
        else:
            print(f"Test image exists: {test_img.exists()}, file inputs: {len(file_inputs)}")

        # 8. Dump DOM completo della sidebar avatar
        print("\n=== Dump DOM avatar sidebar ===")
        sidebar = page.locator('div[aria-label="Avatar AI"]').first
        if await sidebar.count() > 0:
            html = await sidebar.inner_html()
            (OUTPUT / "avatar_sidebar_dom.html").write_text(html[:50000])
            print(f"DOM salvato ({len(html)} chars)")

        print("\n=== DEBUG COMPLETATO ===")
        print(f"Output in: {OUTPUT}")
        input("Premi INVIO per chiudere il browser...")

    finally:
        await browser.close()
        await pw.stop()


if __name__ == "__main__":
    asyncio.run(run())
