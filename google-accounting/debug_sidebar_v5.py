import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        await page.goto('https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit', wait_until='domcontentloaded')
        await asyncio.sleep(20)
        
        print('Clicking Avatar button...')
        await page.locator('#content-library-rail-avatars-element').click()
        await asyncio.sleep(10)
        
        print('Sidebar after click:')
        sidebar = page.locator('.appsFlixScriptsSidebarAvatarMode')
        print(f'Sidebar visible: {await sidebar.is_visible()}')
        
        tas = await sidebar.locator('div[contenteditable="true"], textarea, [role="textbox"]').all()
        for i, ta in enumerate(tas):
            print(f'INPUT {i}: HTML={await ta.evaluate("el => el.outerHTML")}')
            
    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
