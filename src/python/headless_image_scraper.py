import sys
import urllib.parse
from playwright.sync_api import sync_playwright

def search_image(query):
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        try:
            q = urllib.parse.quote(query)
            
            # 1. Try DuckDuckGo
            url = f"https://duckduckgo.com/?q={q}&t=h_&iar=images&iax=images&ia=images"
            page.goto(url, timeout=15000)
            page.wait_for_timeout(2000)
            
            img_src = page.evaluate('''() => {
                const imgs = Array.from(document.querySelectorAll('img'));
                for(let img of imgs) {
                    if (img.src && img.src.includes('external-content.duckduckgo.com')) {
                        return img.src;
                    }
                }
                return null;
            }''')
            
            if img_src:
                print(img_src)
                return
                
            # 2. Try Bing as fallback
            url = f"https://www.bing.com/images/search?q={q}"
            page.goto(url, timeout=15000)
            page.wait_for_timeout(2000)
            
            img_src = page.evaluate('''() => {
                const imgs = Array.from(document.querySelectorAll('img.mimg'));
                for(let img of imgs) {
                    let src = img.src || img.getAttribute('data-src');
                    if (src && src.startsWith('http')) {
                        return src;
                    }
                }
                return null;
            }''')
            
            print(img_src or "")
            
        except Exception as e:
            # We don't print errors to stdout because stdout is read by Go
            pass
        finally:
            browser.close()

if __name__ == "__main__":
    if len(sys.argv) > 1:
        search_image(sys.argv[1])
