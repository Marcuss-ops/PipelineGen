import asyncio
import logging
import sqlite3
import json
from pathlib import Path

# Setup logging
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s: %(message)s")
log = logging.getLogger("TestAll")

from playwright_client import (
    generate_monkey_flow_image, 
    generate_monkey_character_video,
    generate_monkey_avatar_video,
    generate_video_ai_v2
)

async def test_character_video():
    log.info("--- [TEST] Character Video Generation ---")
    try:
        video_path = await generate_monkey_character_video(character_id="alex", prompt="smiling at the camera", headless=False)
        if video_path:
            log.info(f"SUCCESS: Generated Character video at: {video_path}")
        else:
            log.info("FAILURE: No Character video generated (returned None).")
    except Exception as e:
        log.exception(f"Exception in Character Video: {e}")

async def test_vids_video():
    log.info("--- [TEST] Google Vids AI Video Generation ---")
    try:
        # generate_video_ai_v2(video_id, prompt, account, headless)
        video_id = await generate_video_ai_v2(video_id="new", prompt="A short clip of a professional presenter speaking", account="favamassimo", headless=False)
        if video_id:
            log.info(f"SUCCESS: Generated Google Vids video ID/path: {video_id}")
        else:
            log.info("FAILURE: No Vids video generated.")
    except Exception as e:
        log.exception(f"Exception in Vids video generation: {e}")

async def main():
    # Only test Google Vids (Video AI + Character Video)
    await test_vids_video()
    await test_character_video()

if __name__ == "__main__":
    asyncio.run(main())
