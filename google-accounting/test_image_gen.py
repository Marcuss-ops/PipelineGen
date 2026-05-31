import asyncio
import sys
from automation.vids import generate_vids_image_v1

async def main():
    prompt = "A beautiful futuristic cyberpunk city with neon lights, ultra realistic, highly detailed"
    if len(sys.argv) > 1:
        prompt = sys.argv[1]
        
    print("="*60)
    print(f"TESTING GOOGLE VIDS IMAGE GENERATION")
    print(f"Prompt: {prompt}")
    print("="*60)
    
    try:
        # Run in headless=False so you can see it working, or headless=True
        # Let's default to headless=True to avoid X11 errors in background tasks
        result = await generate_vids_image_v1(
            video_id="new",
            prompt=prompt,
            account="favamassimo",
            headless=False
        )
        print("\n" + "="*60)
        print(f"SUCCESS! Image generated successfully.")
        print(f"File path: {result}")
        print("="*60)
    except Exception as e:
        print("\n" + "="*60)
        print(f"ERROR: Image generation failed!")
        print(f"Reason: {e}")
        print("="*60)

if __name__ == "__main__":
    asyncio.run(main())
