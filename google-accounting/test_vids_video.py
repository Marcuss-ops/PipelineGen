#!/usr/bin/env python3
"""
Test: Generazione video AI con Google Vids (Veo).
Usa generate_video_ai_v2 in modalità headless.
"""
import asyncio
import sys
import logging
from pathlib import Path

# Setup logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s  %(levelname)-8s  %(name)s  %(message)s",
    datefmt="%H:%M:%S",
)

sys.path.insert(0, str(Path(__file__).parent))
from playwright_client import generate_video_ai_v2


async def main():
    prompt = "a monkey eating a cheeseburger in a tropical jungle, cinematic, 4k"
    account = "favamassimo"

    print(f"\n--- [TEST] Generazione Video AI con Google Vids ---")
    print(f"  Prompt : {prompt}")
    print(f"  Account: {account}")
    print(f"  Headless: True")
    print()

    result = await generate_video_ai_v2(
        video_id="new",
        prompt=prompt,
        account=account,
        headless=True,
    )

    if result:
        print(f"\nSUCCESS: Video generato >> {result}")
        p = Path(result)
        if p.exists():
            size = p.stat().st_size
            print(f"  Dimensione: {size / 1024 / 1024:.1f} MB")
    else:
        print("\nFAILURE: Nessun video generato.")


if __name__ == "__main__":
    asyncio.run(main())
