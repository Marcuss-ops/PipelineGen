import asyncio
from playwright.async_api import async_playwright
import time

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        
        project_url = "https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit"
        print(f"Navigating to project: {project_url}")
        await page.goto(project_url, wait_until="domcontentloaded")
        
        print("Waiting for editor to stabilize (30s)...")
        await asyncio.sleep(30)
        await page.screenshot(path="google-accounting/debug_step1_loaded.png")
        
        # 1. Click Avatar Button
        avatar_xpath = "/html/body/div[4]/div/div/div[3]/div[2]/div[2]"
        print(f"Clicking Avatar button (XPath: {avatar_xpath})...")
        loc = page.locator(f"xpath={avatar_xpath}")
        if await loc.count() > 0:
            await loc.click()
            print("Avatar button clicked.")
        else:
            print("Avatar button NOT found by XPath. Searching by aria-label...")
            alt_loc = page.locator('div[aria-label="Avatar AI"], [role="button"]:has-text("Avatar AI")').first
            if await alt_loc.count() > 0:
                await alt_loc.click()
                print("Avatar button clicked via fallback.")
            else:
                print("CRITICAL: Avatar button not found.")
                return

        await asyncio.sleep(10)
        await page.screenshot(path="google-accounting/debug_step2_panel_open.png")
        
        # 2. Inspect for Textarea
        print("Searching for textarea or script input...")
        # Dump all textareas and contenteditables
        tas = await page.locator('textarea, [contenteditable="true"]').all()
        print(f"Found {len(tas)} potential inputs.")
        for i, ta in enumerate(tas):
            placeholder = await ta.get_attribute("placeholder") or ""
            aria = await ta.get_attribute("aria-label") or ""
            text = await ta.inner_text()
            print(f"Input {i}: Placeholder='{placeholder}', Aria='{aria}', Text='{text[:50]}'")
            
        # Try user XPath
        user_textarea_xpath = "/html/body/div[32]/div[4]/div[1]/div[3]/div[1]"
        print(f"Checking user XPath for textarea: {user_textarea_xpath}")
        u_loc = page.locator(f"xpath={user_textarea_xpath}")
        if await u_loc.count() > 0:
            print("SUCCESS: Textarea found by user XPath.")
            await u_loc.click()
            await u_loc.fill("Test script content")
            print("Textarea filled.")
        else:
            print("FAIL: Textarea NOT found by user XPath.")
            # Let's try to find it by looking for "script" text nearby
            print("Looking for 'Script' text in page...")
            body_text = await page.inner_text("body")
            if "script" in body_text.lower():
                print("'script' found in body text. Dumping nearby elements...")
            
        await page.screenshot(path="google-accounting/debug_step3_input_check.png")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
