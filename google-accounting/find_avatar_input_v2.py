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
        
        # Cerca TUTTI i div con contenteditable nella sidebar
        print("Searching for contenteditables in sidebar...")
        sidebar = page.locator('.appsFlixScriptsSidebarAvatarMode')
        ceds = await sidebar.locator('div[contenteditable]').all()
        print(f"Found {len(ceds)} contenteditables.")
        for i, ce in enumerate(ceds):
            html = await ce.evaluate("el => el.outerHTML")
            print(f"CE {i}: HTML={html[:200]}")
            
        # Proviamo a scendere in profondità per trovare l'input dello script
        # Spesso è dentro un container come appsFlixScriptsSidebarBody
        body = sidebar.locator('.appsFlixScriptsSidebarBody')
        if await body.count() > 0:
            print("Body found.")
            # Cerchiamo qualsiasi cosa abbia aria-label che contenga "script" o "copione"
            script_inps = await body.locator('[aria-label*="script"], [aria-label*="copione"], [role="textbox"]').all()
            for i, si in enumerate(script_inps):
                label = await si.get_attribute("aria-label")
                print(f"Script Input {i}: Aria-Label='{label}'")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
