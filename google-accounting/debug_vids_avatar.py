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
        await asyncio.sleep(5)
        
        # Prova a cliccare su un video esistente se presente, altrimenti nuovo
        print("Looking for any project link...")
        projects = await page.locator('a[href*="/videos/d/"]').all()
        if projects:
            print(f"Found {len(projects)} projects. Clicking the first one...")
            await projects[0].click()
        else:
            print("No projects found. Clicking 'Inizia un nuovo video'...")
            # Spesso è un div con aria-label o testo
            btn = page.locator('div[aria-label="Inizia un nuovo video"], [role="button"]:has-text("Inizia un nuovo video")').first
            await btn.click()
            
        print("Waiting for editor to load...")
        # Aspettiamo che l'URL cambi in /videos/d/
        for _ in range(20):
            await asyncio.sleep(1)
            if "/videos/d/" in page.url:
                print(f"Editor loaded: {page.url}")
                break
        else:
            print(f"Timeout waiting for editor. Current URL: {page.url}")
            await page.screenshot(path="google-accounting/debug_vids_home_after_click.png")
            return

        await asyncio.sleep(10) # Caricamento pesante dell'editor
        
        # Ispezione top bar per "Inserisci"
        print("Searching for 'Inserisci' in the top menu bar...")
        # Google Docs/Vids menus are often in a specific container
        menus = await page.locator('.docs-menubar .docs-primary-menu-item, button').all()
        for i, m in enumerate(menus):
            text = await m.inner_text()
            if text: print(f"Menu {i}: '{text.strip()}'")
            if "Inserisci" in text or "Insert" in text:
                print(f"Target menu found: {text.strip()}. Clicking...")
                await m.click()
                await asyncio.sleep(2)
                break
        
        await page.screenshot(path="google-accounting/debug_vids_editor.png")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
