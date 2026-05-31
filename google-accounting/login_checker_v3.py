import asyncio
from playwright.async_api import async_playwright
import os

async def check():
    target = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/google-accounting/profiles/favamassimo"
    print(f"Checking login status with basic password store...")
    async with async_playwright() as p:
        context = await p.chromium.launch_persistent_context(
            target, 
            headless=True, 
            args=[
                "--no-sandbox", 
                "--profile-directory=Profile 1",
                "--password-store=basic"
            ]
        )
        page = await context.new_page()
        # Go to vids home
        await page.goto("https://vids.google.com", wait_until="domcontentloaded", timeout=60000)
        await asyncio.sleep(10)
        print(f"URL: {page.url}")
        print(f"Title: {await page.title()}")
        await page.screenshot(path="login_status_v3.png")
        await context.close()

if __name__ == "__main__":
    asyncio.run(check())
