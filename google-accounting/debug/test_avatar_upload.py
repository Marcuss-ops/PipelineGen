"""Test completo: upload Alex.png → prompt → genera → scarica video."""
import asyncio, sys, re, subprocess, time
from pathlib import Path
sys.path.insert(0, str(Path(__file__).parent.parent))
from playwright.async_api import async_playwright

SESSION = str(Path(__file__).parent.parent / "sessions" / "favamassimo.json")
AVATAR_IMG = str(Path(__file__).parent.parent / "data/avatars/Alex.png")
DOWNLOAD_DIR = Path(__file__).parent.parent / "data" / "google_vids" / "videos"
DOWNLOAD_DIR.mkdir(parents=True, exist_ok=True)
VIDS_CREATE_URL = "https://docs.google.com/videos/create"


async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    ctx = await browser.new_context(
        storage_state=SESSION,
        viewport={"width": 1920, "height": 1080},
        user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
    )
    page = await ctx.new_page()

    try:
        print("[1] Create Vids project...")
        await page.goto(VIDS_CREATE_URL, wait_until="domcontentloaded")
        await asyncio.sleep(5)
        await page.wait_for_url(re.compile(r"/videos/d/"), timeout=30000)
        vid = re.search(r"/videos/d/([^/?#]+)", page.url).group(1)
        print(f"  Project: {vid}")
        await asyncio.sleep(10)

        # Dismiss dialogs
        for _ in range(3):
            await page.keyboard.press("Escape")
            await asyncio.sleep(0.5)
        await asyncio.sleep(3)

        # Click Veo rail - might open Getting Started dialog
        print("[2] Click Veo generation rail...")
        for attempt in range(3):
            veo = page.locator('#content-library-rail-video-generation-element').first
            if await veo.count() == 0:
                # Fallback to aria label
                veo = page.get_by_label("Genera un video clip AI", exact=True).first
            await veo.click(force=True, timeout=10000)
            await asyncio.sleep(4)

            # Check if Getting Started dialog appeared - if so, click Veo 3.1 button
            gs_btn = page.locator('button.videogen, button:has-text("Veo 3.1")').first
            if await gs_btn.count() > 0 and await gs_btn.is_visible():
                print("  Getting Started dialog detected, clicking Veo 3.1...")
                await gs_btn.click()
                await asyncio.sleep(3)

            # After clicking, check if the sidebar with textarea opened
            ta_check = page.locator('textarea').first
            if await ta_check.count() > 0:
                print("  Veo panel opened successfully")
                break

            await page.keyboard.press("Escape")
            await asyncio.sleep(2)

        # Scan interactive elements to see what's available
        print("\n--- Available buttons after opening Veo ---")
        btns = await page.locator('button, div[role="button"]').all()
        for i, btn in enumerate(btns):
            try:
                text = (await btn.inner_text()).strip()[:80]
                aria = (await btn.get_attribute("aria-label") or "")[:60]
                visible = await btn.is_visible()
                enabled = await btn.is_enabled()
                if text or aria:
                    print(f"  [{i}] visible={visible} enabled={enabled} text='{text}' aria='{aria}'")
            except:
                pass

        # Upload Alex.png via file input
        print("\n[3] Upload Alex.png as reference image...")
        fi = page.locator('input[type="file"][accept*="image"]').first
        await fi.set_input_files(AVATAR_IMG)
        print("  Upload SUCCESS!")
        await asyncio.sleep(5)

        # Fill prompt
        print("[4] Fill prompt...")
        ta = page.locator('textarea').first
        prompt = "un uomo che parla alla camera in modo professionale, sfondo ufficio moderno"
        await ta.click()
        await ta.fill("")
        await page.keyboard.type(prompt, delay=30)
        print(f"  Prompt: {prompt}")
        await asyncio.sleep(2)

        # Scan again after filling prompt
        print("\n--- Buttons after filling prompt ---")
        btns = await page.locator('button, div[role="button"]').all()
        for i, btn in enumerate(btns):
            try:
                text = (await btn.inner_text()).strip()[:80]
                aria = (await btn.get_attribute("aria-label") or "")[:60]
                visible = await btn.is_visible()
                enabled = await btn.is_enabled()
                if text or aria:
                    print(f"  [{i}] visible={visible} enabled={enabled} text='{text}' aria='{aria}'")
            except:
                pass

        # Click Generate using the videoGenCreationViewGenerateButton class from vids_video.py
        print("\n[5] Click Generate button...")
        gen_selectors = [
            'button.videoGenCreationViewGenerateButton',
            'button[data-view-id="videoGenCreationViewGenerateButton"]',
            'button:has-text("Genera"):not([data-view-id*="getting-started"])',
            'div[role="button"]:has-text("Genera")',
        ]
        gen_btn = None
        for sel in gen_selectors:
            loc = page.locator(sel).first
            if await loc.count() > 0 and await loc.is_visible():
                gen_btn = loc
                print(f"  Found via: {sel}")
                break
        if gen_btn:
            await gen_btn.click()
            print("  Generate clicked! Waiting up to 90s...")
        else:
            print("  Generate button not found!")
            await page.screenshot(path=str(DOWNLOAD_DIR / "no_gen_btn.png"))
            return

        # Wait and poll for video preview
        video_src = None
        for i in range(18):
            await asyncio.sleep(5)
            # Find video elements with src
            videos = page.locator("video")
            count = await videos.count()
            for j in range(count):
                try:
                    src = await videos.nth(j).evaluate("el => el.currentSrc || el.src || ''")
                    if src and "blob" not in src and src.strip():
                        video_src = src
                        print(f"  Video found at poll {i*5}s: {src[:100]}")
                        break
                except:
                    pass
            if video_src:
                break
            print(f"  Poll {i+1}: no video yet... ({i*5}s)")
            await page.screenshot(path=str(DOWNLOAD_DIR / f"poll_{i}.png"))

        if not video_src:
            print("  No video generated within timeout!")
            await page.screenshot(path=str(DOWNLOAD_DIR / "timeout.png"))
            return

        # Download
        print(f"\n[6] Downloading video...")
        from urllib import request
        headers = {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
            "Referer": page.url,
        }
        req = request.Request(video_src, headers=headers)
        raw_path = DOWNLOAD_DIR / f"test_avatar_alex_raw.mp4"
        with request.urlopen(req, timeout=120) as resp:
            raw_path.write_bytes(resp.read())
        print(f"  Downloaded: {raw_path} ({raw_path.stat().st_size} bytes)")

        # Convert to standard format
        final_path = DOWNLOAD_DIR / f"test_avatar_alex.mp4"
        cmd = ['ffmpeg', '-y', '-i', str(raw_path),
               '-vf', 'scale=1920:1080', '-c:v', 'libx264', '-pix_fmt', 'yuv420p',
               '-c:a', 'aac', str(final_path)]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        if proc.returncode == 0:
            raw_path.unlink()
            print(f"  Final video: {final_path}")
        else:
            print(f"  FFmpeg error: {proc.stderr[:200]}")
            print(f"  Raw: {raw_path}")

    except Exception as e:
        print(f"ERROR: {e}")
        import traceback; traceback.print_exc()
        await page.screenshot(path=str(DOWNLOAD_DIR / "error.png"))
    finally:
        await browser.close()
        await pw.stop()

    print("\n=== DONE ===")

if __name__ == "__main__":
    asyncio.run(run())
