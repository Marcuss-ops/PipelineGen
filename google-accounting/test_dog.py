import asyncio
import logging
from playwright_client import generate_flow_images

logging.basicConfig(level=logging.INFO)

async def main():
    prompt = "a dog playing football"
    style = "realistic"
    account = "favamassimo"
    
    print(f"Starting generation for: {prompt} (style: {style})")
    paths = await generate_flow_images(
        prompt=prompt,
        style=style,
        account=account,
        headless=True
    )
    
    if paths:
        print(f"SUCCESS: Generated {len(paths)} images:")
        for p in paths:
            print(f"  - {p}")
    else:
        print("FAILURE: No images generated.")

if __name__ == "__main__":
    asyncio.run(main())
