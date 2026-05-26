import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        print("Navigating to Google Vids Home...")
        await page.goto("https://docs.google.com/videos/u/0/?usp=direct_url", wait_until="networkidle")
        await asyncio.sleep(10)
        await page.screenshot(path="google-accounting/debug_vids_home.png")
        
        print("Dumping all button and link text...")
        elements = await page.locator('button, a, div[role="button"], div[aria-label]').all()
        for i, el in enumerate(elements):
            text = (await el.inner_text()).strip()
            aria = await el.get_attribute("aria-label") or ""
            role = await el.get_attribute("role") or ""
            if text or aria:
                print(f"Element {i}: Text='{text[:50]}', Aria='{aria[:50]}', Role='{role}'")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
