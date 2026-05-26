import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        print("Navigating to existing Vids project...")
        await page.goto("https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit", wait_until="domcontentloaded")
        await asyncio.sleep(30)
        
        # Click Avatar
        avatar_btn = page.locator('div[aria-label="Avatar AI"], button:has-text("Avatar AI")').first
        if await avatar_btn.count() > 0:
            print("Clicking Avatar button...")
            await avatar_btn.click()
            await asyncio.sleep(10)
        
        # Take screenshot of the state
        print("Taking screenshot of sidebar state...")
        await page.screenshot(path="google-accounting/debug_sidebar_state.png")
        
        # Dump the sidebar DOM completely
        sidebar = page.locator('.appsFlixScriptsSidebarAvatarMode')
        if await sidebar.count() > 0:
            html = await sidebar.evaluate("el => el.innerHTML")
            with open("google-accounting/sidebar_dump.html", "w") as f:
                f.write(html)
            print("Sidebar HTML dumped to sidebar_dump.html")
            
            # Look for ANY editable elements
            editables = await sidebar.locator('[contenteditable], textarea, input, [role="textbox"]').all()
            print(f"Found {len(editables)} potential editable elements.")
            for i, el in enumerate(editables):
                html_el = await el.evaluate("el => el.outerHTML")
                print(f"Editable {i}: {html_el[:150]}")
        else:
            print("Sidebar NOT found.")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
