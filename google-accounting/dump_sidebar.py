import asyncio
from playwright.async_api import async_playwright

async def main():
    async with async_playwright() as p:
        browser = await p.chromium.connect_over_cdp("http://localhost:9222")
        context = browser.contexts[0]
        page = context.pages[0]
        print("Connected to page:", page.url)
        
        # Dump the sidebar outer HTML
        sidebar = page.locator("aside, div.appsDocsUiSidebarEl.genAiImageSidebarEl").first
        if await sidebar.count() > 0:
            html = await sidebar.inner_html()
            with open("google-accounting/debug_content.html", "w") as f:
                f.write(html)
            print("Wrote debug HTML to google-accounting/debug_content.html")
        else:
            print("Sidebar not found!")
            # Dump whole page
            html = await page.content()
            with open("google-accounting/debug_content.html", "w") as f:
                f.write(html)

if __name__ == "__main__":
    asyncio.run(main())
