import asyncio
import sys
import os
from pathlib import Path

# Aggiunge il percorso per trovare automation e storage
sys.path.append(os.getcwd())
sys.path.append(str(Path(os.getcwd()).parent))

from automation.flow import generate_flow_images

async def test():
    prompt = "a futuristic medieval knight with a glowing sword"
    project_id = "c5abbf0f-286e-4b0d-b092-cec7ea5a339c"
    style = "cinematic watercolor"
    
    print(f"--- FINAL VALIDATION: 4-IMAGE FLOW ---")
    print(f"Prompt: {prompt}")
    print(f"Style: {style}")
    
    try:
        paths = await generate_flow_images(
            prompt=prompt,
            project_id=project_id,
            style=style,
            account="favamassimo",
            headless=True
        )
        
        print(f"\n--- Results ---")
        print(f"Total images captured: {len(paths)}")
        if len(paths) >= 4:
            print("✅ SUCCESS: 4 or more images generated and captured!")
        else:
            print(f"⚠️ WARNING: Only {len(paths)} images captured. Check logs for UI selection status.")
            
        for i, p in enumerate(paths):
            print(f" {i+1}. {p}")
            
    except Exception as e:
        print(f"❌ ERROR: {e}")

if __name__ == "__main__":
    asyncio.run(test())
