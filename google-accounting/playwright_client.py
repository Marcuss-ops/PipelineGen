from automation.base import BaseAutomation, human_delay, human_scroll
from automation.flow import ImageFXFlowAutomation, STYLE_MAP, generate_flow_images
from automation.vids import GoogleVidsAutomation, generate_video_ai_v2, generate_avatar_v1, generate_character_video_v1, generate_vids_image_v1, generate_vids_image_v1_pooled, list_projects, sync_project

# ── Metodi Semplificati di Generazione ──────────────────────────────────────

async def generate_monkey_flow_image(prompt: str, style: str = "realistic", headless: bool = False) -> list:
    """Genera immagini tramite Google Flow usando l'account di default 'favamassimo'."""
    return await generate_flow_images(
        prompt=prompt,
        style=style,
        account="favamassimo",
        headless=headless
    )

async def generate_monkey_character_video(character_id: str, prompt: str = None, headless: bool = False) -> str:
    """Genera video basati su character reference usando l'account 'favamassimo'."""
    return await generate_character_video_v1(
        video_id="new",
        character_id=character_id,
        prompt=prompt,
        account="favamassimo",
        headless=headless
    )

async def generate_monkey_avatar_video(script: str, avatar_id: str = "James", headless: bool = False) -> str:
    """Genera video avatar (Talking Head) via Lip Sync usando l'account 'favamassimo'."""
    return await generate_avatar_v1(
        video_id="new",
        script=script,
        avatar_id=avatar_id,
        account="favamassimo",
        headless=headless
    )

__all__ = [
    "BaseAutomation",
    "GoogleVidsAutomation",
    "ImageFXFlowAutomation",
    "STYLE_MAP",
    "generate_flow_images",
    "generate_video_ai_v2",
    "generate_avatar_v1",
    "generate_character_video_v1",
    "generate_vids_image_v1",
    "generate_vids_image_v1_pooled",
    "human_delay",
    "human_scroll",
    "list_projects",
    "sync_project",
    "generate_monkey_flow_image",
    "generate_monkey_character_video",
    "generate_monkey_avatar_video",
]
