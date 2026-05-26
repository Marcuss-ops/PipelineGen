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
        
        await page.locator('#content-library-rail-avatars-element').click()
        await asyncio.sleep(5)
        
        await page.locator('div[role="button"]:has-text("Cambia")').click()
        await asyncio.sleep(5)
        
        btns = await page.locator('div[role="button"]').all()
        for i, b in enumerate(btns):
            t = await b.inner_text()
            html = await b.evaluate("el => el.outerHTML")
            print(f'BTN {i}: {t} | HTML={html[:100]}')
            
    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
