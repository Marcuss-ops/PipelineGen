import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        await page.goto('https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit', wait_until='domcontentloaded')
        await asyncio.sleep(30)
        
        # Click Avatar
        await page.locator('xpath=/html/body/div[4]/div/div/div[3]/div[2]/div[2]').click()
        await asyncio.sleep(10)
        
        # Cerca elementi che contengono "Avatar" o "Script" o "Genera"
        elements = await page.locator('div, span, button, [role="button"]').all()
        print(f"Total elements scanned: {len(elements)}")
        for i, el in enumerate(elements):
            try:
                text = (await el.inner_text()).strip()
                if "Avatar" in text or "Script" in text or "Genera" in text or "Crea" in text or "Preview" in text:
                    html = await el.evaluate("el => el.outerHTML")
                    print(f"[{i}] Text: '{text[:100].replace(chr(10), ' ')}' | HTML: {html[:250]}")
            except: continue

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
