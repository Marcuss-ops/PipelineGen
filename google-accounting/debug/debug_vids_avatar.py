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
        await asyncio.sleep(20)
        
        print("Clicking 'Inserisci'...")
        # A volte è un pulsante con id docs-insert-menu o simile
        insert_btn = page.locator('.docs-primary-menu-item:has-text("Inserisci"), button:has-text("Inserisci")').first
        await insert_btn.click()
        await asyncio.sleep(3)
        
        print("Searching for 'Avatar AI'...")
        avatar_item = page.locator('.goog-menuitem:has-text("Avatar AI"), [role="menuitem"]:has-text("Avatar AI")').first
        if await avatar_item.count() > 0:
            print("Clicking 'Avatar AI'...")
            await avatar_item.click()
            await asyncio.sleep(8)
            await page.screenshot(path="google-accounting/debug_vids_avatar_panel.png")
            
            # Ispezione del pannello Avatar
            print("Inspecting Sidebar/Panel for script input...")
            # Spesso i pannelli laterali di Google hanno classi specifiche
            sidebars = await page.locator('.apps-docs-ai-sidebar, .vids-side-panel, div[role="complementary"]').all()
            for i, sb in enumerate(sidebars):
                text = await sb.inner_text()
                print(f"Sidebar {i} text: {text[:200]}")
            
            textareas = await page.locator('textarea').all()
            for i, ta in enumerate(textareas):
                placeholder = await ta.get_attribute("placeholder") or ""
                print(f"Textarea {i}: Placeholder='{placeholder}'")
                
            btns = await page.locator('button').all()
            for i, btn in enumerate(btns):
                btn_text = await btn.inner_text()
                if btn_text.strip():
                    print(f"Button {i}: '{btn_text.strip()}'")
        else:
            print("'Avatar AI' not found.")

    finally:
        await browser.close()
        await pw.stop()

if __name__ == "__main__":
    asyncio.run(run())
