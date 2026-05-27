import asyncio
import re
import os
import time
from pathlib import Path
from playwright.async_api import async_playwright
import sys

# Add project root to sys.path
sys.path.append(os.getcwd())
sys.path.append(os.path.join(os.getcwd(), "google-accounting"))

from storage import get_character, save_project_id
from drive_client import download_file

async def run_test():
    async with async_playwright() as pw:
        # Launch browser
        browser = await pw.chromium.launch(headless=True)
        context = await browser.new_context(
            storage_state='google-accounting/sessions/favamassimo.json',
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
        )
        page = await context.new_page()
        
        char_id = 'alex'
        char = get_character(char_id)
        image_drive_id = char['image_drive_id']
        
        print(f"Downloading reference image for {char_id}...")
        temp_img_path = await download_file(image_drive_id, f"ref_{char_id}.png", "image")
        
        video_id = '1iprXbBW73jSfPpP87k6Q3KJTBj9lxFkIOgoDUOC0yGY'
        url = f"https://docs.google.com/videos/u/0/d/{video_id}/edit"
        
        print(f"Navigating to {url}...")
        await page.goto(url, wait_until="domcontentloaded")
        await asyncio.sleep(10)
        
        print(f"Current URL: {page.url}")
        if "accounts.google.com" in page.url:
            print("Redirected to login. Selecting account...")
            account_btn = page.locator('[data-email*="favamassimo"], [role="button"]:has-text("favamassimo")').first
            if await account_btn.count() > 0:
                await account_btn.click()
                await asyncio.sleep(5)
                print(f"URL after click: {page.url}")
        
        if "docs.google.com/videos" in page.url:
            print("SUCCESS: Entered Vids Editor.")
            
            # Dismiss any dialog
            await page.keyboard.press("Escape")
            
            # Click Generation Rail
            print("Opening generation rail...")
            try:
                await page.locator('#content-library-rail-video-generation-element').click(timeout=10000)
            except:
                await page.get_by_label("Genera un video clip AI", exact=True).click(timeout=10000)
            
            await asyncio.sleep(5)
            
            # Upload Reference Image
            print("Searching for upload button...")
            upload_btn_xpath = "/html/body/div[6]/div/div[2]/div/div[2]/span/div/div/div/div[1]/div/div[2]/div[4]/div[2]"
            
            # Fallback selectors
            selectors = [
                f"xpath={upload_btn_xpath}",
                "div[aria-label='Ingredienti'] + div div[role='button']",
                "div:has-text('Ingredienti') + div div[role='button']",
                "div[role='button']:has-text('Aggiungi')",
                ".apps-docs-ai-sidebar div[role='button']" # Try any button in sidebar if specific fails
            ]
            
            upload_btn = None
            for sel in selectors:
                loc = page.locator(sel).first
                if await loc.count() > 0:
                    print(f"Found upload button with selector: {sel}")
                    upload_btn = loc
                    break
            
            if not upload_btn:
                print("CRITICAL: Upload button not found. Dumping nearby HTML...")
                ing = page.locator(":has-text('Ingredienti')").last
                if await ing.count() > 0:
                    html = await ing.evaluate("el => el.parentElement.outerHTML")
                    print(f"HTML near Ingredienti: {html[:1000]}")
                raise RuntimeError("Upload button not found")

            print("Uploading reference image...")
            async with page.expect_file_chooser() as fc_info:
                await upload_btn.click()
            file_chooser = await fc_info.value
            await file_chooser.set_files(temp_img_path)
            print("Reference image uploaded.")
            
            await asyncio.sleep(5)
            
            # Fill Prompt
            prompt = "alex smiling at the camera, high quality"
            print(f"Filling prompt: {prompt}")
            input_loc = page.locator('textarea, [contenteditable="true"], div[role="textbox"]').first
            await input_loc.fill(prompt)
            
            # Click Generate
            print("Clicking Generate...")
            await page.locator('button:has-text("Genera"), button:has-text("Generate")').first.click()
            
            print("Generation triggered! Waiting for preview (30s)...")
            await asyncio.sleep(30)
            await page.screenshot(path="google-accounting/debug/test_gen_result.png")
            print("Screenshot saved to google-accounting/debug/test_gen_result.png")
            
        else:
            print(f"FAILED: Stuck at {page.url}")
            await page.screenshot(path="google-accounting/debug/test_gen_failed.png")

        await browser.close()

if __name__ == "__main__":
    asyncio.run(run_test())
