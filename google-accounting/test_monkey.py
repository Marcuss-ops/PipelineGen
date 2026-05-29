import asyncio
import logging
from playwright_client import (
    generate_monkey_flow_image, 
    generate_monkey_character_video,
    generate_monkey_avatar_video
)

logging.basicConfig(level=logging.INFO)

async def main():
    prompt = "a small cute yellow cat sleeping"
    style = "realistic"
    
    print(f"--- [TEST] Generazione Immagine con metodo semplificato ---")
    paths = await generate_monkey_flow_image(
        prompt=prompt,
        style=style,
        headless=False
    )
    
    if paths:
        print(f"SUCCESS: Generated {len(paths)} images:")
        for p in paths:
            print(f"  - {p}")
    else:
        print("FAILURE: No images generated.")

    # Esempio d'uso per i video (facile da decommentare ed eseguire):
    # print(f"\n--- [TEST] Esempio Generazione Video Character ---")
    # video_path = await generate_monkey_character_video(
    #     character_id="alex",
    #     prompt="smiling at the camera",
    #     headless=False
    # )
    # print(f"Video generato: {video_path}")

if __name__ == "__main__":
    asyncio.run(main())
