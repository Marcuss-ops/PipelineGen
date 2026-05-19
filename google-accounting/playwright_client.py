import asyncio
import logging
import time
import subprocess
import random
from pathlib import Path
from playwright.async_api import async_playwright, Browser, BrowserContext, Page
from config import get_session_path, DOWNLOAD_DIR, GOOGLE_VIDS_BASE_URL

log = logging.getLogger("AutomationEngine")

async def human_delay(min_ms=500, max_ms=2500):
    """Simula una pausa umana casuale."""
    await asyncio.sleep(random.uniform(min_ms, max_ms) / 1000)

async def human_scroll(page: Page):
    """Simula uno scrolling casuale."""
    try:
        for _ in range(random.randint(1, 3)):
            await page.mouse.wheel(0, random.randint(100, 400))
            await human_delay(300, 800)
            await page.mouse.wheel(0, random.randint(-400, -100))
            await human_delay(300, 800)
    except: pass

class BaseAutomation:
    def __init__(self, account: str = None, headless: bool = True):
        self.account = account
        self.headless = headless
        self.browser: Browser = None
        self.context: BrowserContext = None
        self.session_path = get_session_path(account)
        
        if not self.session_path.exists():
            raise FileNotFoundError(f"Sessione non trovata per account '{account or 'default'}' in {self.session_path}. Eseguire prima il login.")

    async def __aenter__(self):
        self.playwright = await async_playwright().start()
        self.browser = await self.playwright.chromium.launch(
            headless=self.headless,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--start-maximized"
            ]
        )
        self.context = await self.browser.new_context(
            storage_state=str(self.session_path),
            user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
            viewport={"width": 1920, "height": 1080}
        )
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.context:
            await self.context.close()
        if self.browser:
            await self.browser.close()
        await self.playwright.stop()

class GoogleVidsAutomation(BaseAutomation):
    """Engine per l'automazione di Google Vids."""

    async def list_projects(self) -> list[dict]:
        """List all Google Vids projects from the home page."""
        page = await self.context.new_page()
        await page.goto("https://vids.google.com", wait_until="networkidle")
        await asyncio.sleep(5)
        
        projects = []
        # Cerchiamo i contenitori dei progetti
        items = await page.query_selector_all('div[role="gridcell"]')
        for item in items:
            name = await item.get_attribute("aria-label")
            if not name:
                # Prova a prendere il testo interno se aria-label manca
                name_el = await item.query_selector('div[id*="name"]')
                if name_el:
                    name = await name_el.inner_text()
            
            if name:
                link = await item.query_selector('a')
                if link:
                    href = await link.get_attribute("href")
                    # Formato tipico: https://docs.google.com/videos/d/ID/edit
                    if href and "/d/" in href:
                        parts = href.split("/")
                        vid_id = parts[parts.index("d") + 1]
                        projects.append({"name": name, "id": vid_id, "url": href})
        
        await page.close()
        return projects

    async def _get_page(self, video_id: str) -> Page:
        page = await self.context.new_page()
        url = f"{GOOGLE_VIDS_BASE_URL}/{video_id}/edit"
        await human_delay(1000, 3000)
        await page.goto(url, wait_until="domcontentloaded")
        await asyncio.sleep(8)
        await human_scroll(page)
        return page

    async def generate_video(self, video_id: str, prompt: str, zoom_centered: bool = True) -> Path:
        dest_dir = DOWNLOAD_DIR / "videos"
        dest_dir.mkdir(parents=True, exist_ok=True)
        
        page = await self._get_page(video_id)
        try:
            await human_delay(1500, 4000)
            await page.click('#content-library-rail-video-generation-element')
            await human_delay(1000, 2500)
            
            input_sel = 'textarea.javascriptMaterialdesignGm3WizTextFieldOutlined-text-field__input'
            await page.wait_for_selector(input_sel)
            
            # Digitazione umana
            await page.click(input_sel)
            await page.type(input_sel, prompt, delay=random.randint(50, 150))
            await human_delay(800, 2000)
            
            log.info(f"Lancio generazione video: {prompt[:50]}...")
            await page.click('button.videoGenCreationViewGenerateButton')
            
            thumb_sel = 'video.appsDocsAiGenerativeaiVideoUiSidebarWizSuccessfulvideogenerationthumbnailVideoGenerationThumbnail'
            await page.wait_for_selector(thumb_sel, timeout=240000)
            
            video_el = page.locator(thumb_sel).first
            await video_el.hover()
            await human_delay(2000, 5000)
            
            video_src = await video_el.get_attribute("src")
            if not video_src:
                raise RuntimeError("URL video non trovato nell'attributo src.")

            async with page.expect_download(timeout=120000) as download_info:
                await page.evaluate(f"window.location.href = '{video_src}'")
            
            download = await download_info.value
            raw_path = dest_dir / f"raw_{int(time.time())}.mp4"
            await download.save_as(str(raw_path))
            
            final_path = dest_dir / f"VIDEO_YT_{int(time.time())}.mp4"
            vf_filter = "crop=in_w*0.9:in_h*0.9:(in_w-out_w)/2:(in_h-out_h)/2,scale=1920:1080" if zoom_centered else "scale=1920:1080"
            
            cmd = [
                'ffmpeg', '-y', '-i', str(raw_path),
                '-vf', vf_filter,
                '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-preset', 'fast', '-crf', '20',
                '-c:a', 'aac', '-b:a', '192k', str(final_path)
            ]
            
            if subprocess.run(cmd, capture_output=True).returncode == 0:
                raw_path.unlink()
                return final_path
            return raw_path
        except Exception as e:
            log.error(f"Errore generazione video: {e}")
            return None
        finally:
            await page.close()

STYLE_MAP = {
    "realistic": "extremely detailed, realistic, 8k, photorealistic, cinematic lighting",
    "cartoon": "cartoon style, 2d, colorful, high quality animation, vibrant",
    "medieval": "medieval style, fantasy, historical, detailed oil painting, epic atmosphere",
    "cyberpunk": "cyberpunk aesthetic, neon lights, futuristic, dark atmosphere, high tech",
    "watercolor": "watercolor painting style, soft colors, artistic, fluid textures",
    "3d-render": "3d render, octane render, unreal engine 5 style, volumetric lighting, masterpiece",
    "sketch": "hand-drawn sketch, pencil drawing, monochrome, detailed lines, artistic",
    "cinematic": "cinematic lighting, movie shot, 35mm lens, highly detailed, dramatic",
}

class ImageFXFlowAutomation(BaseAutomation):
    """Engine per l'automazione di Google Labs ImageFX Flow."""

    async def generate_images(self, prompt: str, project_id: str = None, style: str = None) -> list[Path]:
        dest_dir = DOWNLOAD_DIR / "images" / (project_id or "general")
        dest_dir.mkdir(parents=True, exist_ok=True)
        
        # Applichiamo lo stile se presente
        if style and style.lower() in STYLE_MAP:
            full_prompt = f"{prompt}, {STYLE_MAP[style.lower()]}"
            log.info(f"Stile '{style}' applicato al prompt.")
        else:
            full_prompt = prompt
        
        if project_id:
            url = f"https://labs.google/fx/it/tools/flow/project/{project_id}"
        else:
            url = "https://labs.google/fx/it/tools/flow"
            
        page = await self.context.new_page()
        await human_delay(1000, 3000)
        log.info(f"Navigazione verso: {url}")
        await page.goto(url, wait_until="networkidle")
        await asyncio.sleep(5)
        await human_scroll(page)

        new_saved_paths = []
        can_capture = False

        async def handle_response(response):
            nonlocal new_saved_paths
            if not can_capture: return
            
            content_type = response.headers.get("content-type", "")
            if "image/" in content_type:
                try:
                    body = await response.body()
                    if len(body) > 200000:
                        timestamp = int(time.time())
                        path = dest_dir / f"FLOW_IMG_{timestamp}_{len(new_saved_paths)}.jpg"
                        path.write_bytes(body)
                        new_saved_paths.append(path)
                        log.info(f"Nuova immagine catturata: {path.name}")
                except: pass

        page.on("response", handle_response)

        try:
            prompt_xpath = 'xpath=/html/body/div[2]/div[1]/div[5]/div/div/div[1]/div'
            await page.wait_for_selector(prompt_xpath)
            
            await human_delay(2000, 5000)
            await page.click(prompt_xpath)
            await human_delay(500, 1500)
            
            await page.type(prompt_xpath, full_prompt, delay=random.randint(40, 180))
            await human_delay(1000, 3000)
            
            await page.keyboard.press("Enter")
            
            await asyncio.sleep(5)
            can_capture = True
            
            wait_time = 60
            log.info(f"In attesa della generazione ({wait_time}s)...")
            await asyncio.sleep(wait_time)
            
            return new_saved_paths
        except Exception as e:
            log.error(f"Errore generazione ImageFX Flow: {e}")
            return []
        finally:
            await page.close()

    async def sync_project(self, video_id: str, file_type: str = "all") -> list[Path]:
        paths = []
        page = await self._get_page(video_id)
        try:
            log.info(f"Syncing project {video_id}...")
            return paths
        finally:
            await page.close()

# Helper per mantenere compatibilità e pulizia
async def list_projects(account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        return await engine.list_projects()

async def sync_project(video_id: str, file_type: str = "all", account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        return await engine.sync_project(video_id, file_type)

async def download_video(video_id: str, account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        return await engine.generate_video(video_id, "Regenerate and download")

async def generate_video_ai_v2(video_id: str, prompt: str, account: str = None, headless: bool = True):
    async with GoogleVidsAutomation(account=account, headless=headless) as engine:
        result = await engine.generate_video(video_id, prompt)
        return str(result) if result else None

async def generate_flow_images(prompt: str, project_id: str = None, style: str = None, account: str = None, headless: bool = True):
    async with ImageFXFlowAutomation(account=account, headless=headless) as engine:
        results = await engine.generate_images(prompt, project_id, style)
        return [str(p) for p in results]
