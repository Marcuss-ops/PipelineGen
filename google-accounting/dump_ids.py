import asyncio
from playwright.async_api import async_playwright

async def run():
    profile_path = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/google-accounting/profiles/favamassimo"
    async with async_playwright() as p:
        context = await p.chromium.launch_persistent_context(
            user_data_dir="/home/pierone/snap/chromium/common/chromium",
            headless=True,
            args=['--profile-directory="Profile 1"']
        )
        page = await context.new_page()
        await page.goto("https://docs.google.com/videos/d/1Q4i4Tz4Ft8se_usT6QGoRa6pQfHddkOQZ8mqQH5InCw/edit", wait_until="domcontentloaded")
        await asyncio.sleep(15)
        
        ids = await page.evaluate("() => Array.from(document.querySelectorAll('[id]')).map(el => el.id)")
        print("Found IDs:", [id for id in ids if 'rail' in id or 'synthesis' in id or 'gen' in id])
        
        await context.close()

if __name__ == "__main__":
    asyncio.run(run())
