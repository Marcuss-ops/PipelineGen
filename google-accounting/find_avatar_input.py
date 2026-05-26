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
        
        # Ispezione Sidebar
        sidebar = page.locator('.appsFlixScriptsSidebarAvatarMode')
        if await sidebar.count() > 0:
            print("Sidebar found.")
            # Cerchiamo l'input specifico per lo script
            # Nel DOM dump vedevo "Inserisci un copione per questa scena"
            inputs = await sidebar.locator('div[contenteditable="true"], textarea, [role="textbox"], .docs-gm3-text-field-input').all()
            for i, inp in enumerate(inputs):
                html = await inp.evaluate("el => el.outerHTML")
                text = await inp.inner_text()
                print(f"INPUT {i}: Text='{text}' | HTML={html[:300]}")
        else:
            print("Sidebar NOT found.")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
