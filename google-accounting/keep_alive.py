import asyncio
import logging
import random
import sys
import argparse
from playwright.async_api import async_playwright
from config import get_profile_path, get_session_path

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
log = logging.getLogger("KeepAlive")

async def run_keep_alive(account: str = None, headless: bool = True):
    profile_path = get_profile_path(account)
    session_path = get_session_path(account)
    
    login_exists = session_path.exists() or (profile_path.exists() and any(profile_path.iterdir()))
    if not login_exists:
        log.error(f"Cannot run keep-alive: profile or session JSON not found for account '{account or 'default'}'. Please run login first.")
        return False

    log.info(f"Starting keep-alive run for account: {account or 'default'} (headless={headless})")
    
    async with async_playwright() as p:
        launch_args = [
            "--disable-blink-features=AutomationControlled",
        ]
        
        user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
        
        context = await p.chromium.launch_persistent_context(
            user_data_dir=str(profile_path),
            headless=headless,
            args=launch_args,
            channel="chrome",
            user_agent=user_agent,
            viewport={'width': 1920, 'height': 1080},
            device_scale_factor=1
        )
        
        try:
            page = await context.new_page()
            
            # Navigate to Google Drive / Vids
            urls = ["https://drive.google.com", "https://docs.google.com/videos/u/0/?usp=direct_url"]
            target_url = random.choice(urls)
            
            log.info(f"Navigating to {target_url}")
            await page.goto(target_url, wait_until="domcontentloaded", timeout=45000)
            
            # Simulate human behavior
            log.info("Simulating human movements to keep session warm...")
            
            # Scroll up and down
            for i in range(random.randint(2, 5)):
                scroll_y = random.randint(200, 700)
                log.info(f"Scrolling down by {scroll_y}px")
                await page.evaluate(f"window.scrollBy(0, {scroll_y})")
                await asyncio.sleep(random.uniform(1.0, 3.0))
                
                scroll_up = random.randint(-500, -100)
                log.info(f"Scrolling up by {abs(scroll_up)}px")
                await page.evaluate(f"window.scrollBy(0, {scroll_up})")
                await asyncio.sleep(random.uniform(0.5, 2.0))
                
            # Random mouse movements (if not headless, or just hover selectors in DOM)
            try:
                # Let's find visible divs or links and hover them to look organic
                elements = await page.query_selector_all("div, a, role=button")
                if elements:
                    sample_elements = random.sample(elements, min(len(elements), 3))
                    for el in sample_elements:
                        if await el.is_visible():
                            log.info("Hovering random element")
                            await el.hover(timeout=2000)
                            await asyncio.sleep(random.uniform(0.5, 1.5))
            except Exception as hover_err:
                log.debug(f"Hover simulation skipped: {hover_err}")
                
            # Mirror to legacy JSON so external tools can still check cookies if needed
            await context.storage_state(path=str(session_path))
            log.info("Successfully refreshed session state and saved mirror JSON")
            
            return True
            
        except Exception as e:
            log.exception(f"Keep alive run encountered an error: {e}")
            return False
        finally:
            await context.close()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Keep Google Session Alive")
    parser.add_argument("--account", type=str, help="Account name")
    parser.add_argument("--headed", action="store_true", help="Run in headed mode")
    args = parser.parse_args()
    
    asyncio.run(run_keep_alive(args.account, headless=not args.headed))
