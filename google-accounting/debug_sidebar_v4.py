import asyncio
from playwright.async_api import async_playwright

async def run():
    pw = await async_playwright().start()
    browser = await pw.chromium.launch(headless=True)
    try:
        context = await browser.new_context(storage_state='google-accounting/sessions/favamassimo.json')
        page = await context.new_page()
        
        project_url = "https://docs.google.com/videos/d/1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4/edit"
        await page.goto(project_url, wait_until="domcontentloaded")
        await asyncio.sleep(20)
        
        # Click Avatar (usando xpath fornito, verificato nel dump)
        # Il dump mostrava "Avatar" in diversi punti.
        # Proviamo a cliccare il pulsante con aria-label "Avatar AI"
        print("Clicking Avatar AI button...")
        btn = page.locator('[aria-label="Avatar AI"]').first
        await btn.click()
        await asyncio.sleep(10)
        
        # Dopo il click, il pannello Avatar dovrebbe aprirsi
        # DUMP di TUTTI i textbox nel corpo
        print("Dumping all inputs...")
        inputs = await page.locator('[role="textbox"], textarea, [contenteditable="true"]').all()
        for i, inp in enumerate(inputs):
            print(f"Input {i}: {await inp.evaluate('el => el.outerHTML')}")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
