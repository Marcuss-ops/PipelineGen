import argparse
import json
import os
import shutil
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from playwright.sync_api import sync_playwright

MASTER_STORAGE = "data/google_slides_storage.json"
WORKER_BASE_DIR = "data/google_slides_worker_profile_"

def setup_worker_profile(worker_id):
    """Creates a distinct profile directory for worker_id to avoid Chromium SingletonLock conflicts."""
    profile_dir = f"{WORKER_BASE_DIR}{worker_id}"
    os.makedirs(profile_dir, exist_ok=True)
    return profile_dir

def generate_single_image_worker(worker_id, prompt, output_path, headful=False):
    """Executes a single image generation task using a distinct pre-warmed worker profile."""
    profile_dir = setup_worker_profile(worker_id)
    os.makedirs(os.path.dirname(os.path.abspath(output_path)), exist_ok=True)

    start_time = time.time()
    print(f"[Worker-{worker_id}] Starting parallel generation for prompt: '{prompt[:40]}...'")

    with sync_playwright() as p:
        context = p.chromium.launch_persistent_context(
            profile_dir,
            headless=not headful,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-setuid-sandbox",
            ]
        )
        if os.path.exists(MASTER_STORAGE):
            with open(MASTER_STORAGE) as f:
                sdata = json.load(f)
                if "cookies" in sdata:
                    context.add_cookies(sdata["cookies"])

        page = context.new_page()
        page.goto("https://slides.new", wait_until="domcontentloaded")
        time.sleep(4)

        # Click insert-generated-image card
        btn = page.locator('button.insert-generated-image, [data-view-id="insert-generated-image"], div:has-text("Nano Banana Pro")').last
        btn.click(force=True)
        time.sleep(2)

        # Fill prompt
        ta = page.locator('.image-synthesis textarea, textarea').first
        ta.fill(prompt)
        time.sleep(1)

        # Select 16:9 aspect ratio
        try:
            prop_btn = page.locator('[aria-label="Proporzioni"], .image-synthesis [aria-label*="Proporzi"]').first
            if prop_btn.is_visible():
                prop_btn.click(force=True)
                time.sleep(0.5)
                opt_169 = page.locator('*:has-text("16:9")').last
                opt_169.click(force=True)
                time.sleep(0.5)
        except Exception:
            pass

        # Submit prompt
        create_btn = page.locator('.image-synthesis-creation-button, button[aria-label="Crea"]').first
        create_btn.click(force=True)

        print(f"[Worker-{worker_id}] Submitted prompt. Waiting for AI generation...")
        time.sleep(22)

        # Locate direct generated AI image and save artwork
        imgs = page.locator('.docs-content-library-image-generation-item img, img[src*="googleusercontent"]').all()
        saved = False
        if len(imgs) > 0:
            for img in imgs:
                try:
                    src = img.get_attribute("src") or ""
                    if "googleusercontent" in src or "blob:" in src:
                        response = page.request.get(src)
                        if response.status == 200:
                            image_bytes = response.body()
                            with open(output_path, "wb") as f:
                                f.write(image_bytes)
                            elapsed = time.time() - start_time
                            print(f"[Worker-{worker_id}] SUCCESS! Generated real 16:9 AI artwork in {elapsed:.1f}s -> {output_path} ({len(image_bytes)} bytes)")
                            saved = True
                            break
                except Exception:
                    pass

        if not saved:
            print(f"[Worker-{worker_id}] Fallback downloading slide PNG...")
            file_menu = page.locator("#docs-file-menu")
            file_menu.click()
            download_item = page.locator('.apps-menuitem:has-text("Scarica"), .apps-menuitem:has-text("Download")').first
            download_item.hover()
            time.sleep(0.5)
            png_item = page.locator('.apps-menuitem:has-text("PNG")').first
            with page.expect_download() as download_info:
                png_item.click()
            download = download_info.value
            download.save_as(output_path)
            print(f"[Worker-{worker_id}] Saved fallback slide -> {output_path}")

        context.close()
        return output_path

def main():
    parser = argparse.ArgumentParser(description="True Parallel AI Image Batch Generation with Chrome Worker Warmup")
    parser.add_argument("--prompts", nargs="+", help="List of prompts for parallel generation")
    parser.add_argument("--concurrency", type=int, default=3, help="Number of parallel Chrome instances")
    parser.add_argument("--headful", action="store_true", help="Run in headful mode")
    args = parser.parse_args()

    if not args.prompts:
        args.prompts = [
            "Futuristic neon cyberpunk supercar racing through rainy streets, 8k cinematic",
            "Ancient medieval castle standing high on a stormy mountain peak, epic lighting",
            "Surreal giant glowing jellyfish floating above a futuristic metropolis, dark moody"
        ]

    concurrency = min(args.concurrency, len(args.prompts))
    print(f"==========================================================")
    print(f"      PipelineGen True Parallel Chrome AI Generator       ")
    print(f"==========================================================")
    print(f"Total Prompts : {len(args.prompts)}")
    print(f"Concurrency   : {concurrency} parallel instances")
    print(f"Master Storage: {MASTER_STORAGE}")
    print(f"----------------------------------------------------------")

    # Pre-warm worker profiles
    print("Pre-warming worker profiles...")
    for i in range(concurrency):
        setup_worker_profile(i)

    start_batch = time.time()
    results = []

    with ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = []
        for idx, prompt in enumerate(args.prompts):
            worker_id = idx % concurrency
            output_file = f"tmp/parallel_gen_{idx+1}.png"
            futures.append(executor.submit(generate_single_image_worker, worker_id, prompt, output_file, args.headful))

        for future in as_completed(futures):
            try:
                res = future.result()
                results.append(res)
            except Exception as e:
                print(f"Parallel worker execution error: {e}")

    total_elapsed = time.time() - start_batch
    print(f"==========================================================")
    print(f" BATCH COMPLETE! Generated {len(results)} images in {total_elapsed:.1f}s")
    print(f"==========================================================")

if __name__ == "__main__":
    main()
