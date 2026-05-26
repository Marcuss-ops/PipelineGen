import asyncio
import sys
import os
from pathlib import Path

# Add paths
sys.path.append(os.getcwd())
sys.path.append(str(Path(os.getcwd()).parent))

from automation.vids import generate_avatar_v1

async def test():
    script = "Ciao, sono James. Benvenuti nel futuro della produzione video automatizzata con Google Vids."
    avatar_id = "James"
    video_id = "1QOY4nINvvf5kOB4uG50DrrpLa92KIqrhglvvyriptC4" 
    
    print(f"--- TESTING AI AVATAR GENERATION (FINAL ATTEMPT) ---")
    
    try:
        path = await generate_avatar_v1(
            video_id=video_id,
            script=script,
            avatar_id=avatar_id,
            account="favamassimo",
            headless=True
        )
        
        if path:
            print(f"\n✅ SUCCESS: Avatar video generated and saved at: {path}")
        else:
            print(f"\n⚠️ WARNING: Process finished but no video path returned.")
            
    except Exception as e:
        print(f"❌ ERROR: {e}")

if __name__ == "__main__":
    asyncio.run(test())
