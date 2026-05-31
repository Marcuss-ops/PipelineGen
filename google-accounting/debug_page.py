import asyncio
from playwright.async_api import async_playwright
from pathlib import Path

async def debug():
    profile_path = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/google-accounting/profiles/favamassimo"
    async with async_playwright() as p:
        context = await p.chromium.launch_persistent_context(
            user_data_dir=profile_path,
            headless=True,
        )
        page = await context.new_page()
        await page.goto("https://docs.google.com/videos/d/1Q4i4Tz4Ft8se_usT6QGoRa6pQfHddkOQZ8mqQH5InCw/edit", wait_until="domcontentloaded")
        await asyncio.sleep(10)
        print(f"URL: {page.url}")
        print(f"Title: {await page.title()}")
        content = await page.content()
        with open("debug_content.html", "w") as f:
            f.write(content)
        await page.screenshot(path="debug_screenshot.png")
        print(f"Screenshot size: {Path('debug_screenshot.png').stat().st_size}")
        await context.close()

if __name__ == "__main__":
    asyncio.run(debug())
