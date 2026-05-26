import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        
        project_url = "https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit"
        print(f"Navigating to project: {project_url}")
        await page.goto(project_url, wait_until="domcontentloaded")
        await asyncio.sleep(30)
        
        # Dump the ENTIRE page body to find where the avatar button went
        print("Dumping entire page body to find Avatar button...")
        html = await page.evaluate("document.body.innerHTML")
        with open("google-accounting/full_page_dump.html", "w") as f:
            f.write(html)
        print("Full page HTML dumped to full_page_dump.html")
        
        # Look for Avatar button
        btns = await page.locator('div, button, [role="button"]').all()
        for i, btn in enumerate(btns):
            text = (await btn.inner_text()).strip()
            aria = await btn.get_attribute("aria-label") or ""
            if "Avatar" in text or "Avatar" in aria:
                print(f"Potential Avatar btn found [{i}]: Text='{text}', Aria='{aria}'")
                await btn.click()
                await asyncio.sleep(10)
                break
        
        await page.screenshot(path="google-accounting/debug_post_avatar_click.png")
        
        # Look for sidebar again
        sidebar = page.locator('.appsFlixScriptsSidebarAvatarMode')
        if await sidebar.count() > 0:
            print("Sidebar found.")
        else:
            print("Sidebar NOT found. Dumping current DOM...")
            html = await page.evaluate("document.body.innerHTML")
            with open("google-accounting/post_click_dump.html", "w") as f:
                f.write(html)

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
