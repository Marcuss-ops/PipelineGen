"""FastAPI routes for the original source-text driven script pipeline."""

from typing import Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from script_pipeline import SourceTextPipelineRequest, run_source_text_pipeline


router = APIRouter(tags=["script-generation"])


class SourceTextPipelineBody(BaseModel):
    source_text: Optional[str] = None
    source_txt_path: Optional[str] = None
    script_style: str = Field(default="deep_dive")
    visual_style: str = Field(default="realistic")
    language: str = Field(default="EN")
    scene_count: int = Field(default=8, ge=1, le=24)
    output_name: Optional[str] = None
    project_id: Optional[str] = None
    video_id: str = Field(default="new")
    account: Optional[str] = None
    headless: bool = True
    drive_folder_id: Optional[str] = None
    images_drive_folder_id: Optional[str] = None


@router.post("/generate-from-txt")
async def generate_from_txt(body: SourceTextPipelineBody):
    if not body.source_text and not body.source_txt_path:
        raise HTTPException(status_code=400, detail="Provide source_text or source_txt_path")

    req = SourceTextPipelineRequest(
        source_text=body.source_text,
        source_txt_path=body.source_txt_path,
        script_style=body.script_style,
        visual_style=body.visual_style,
        language=body.language,
        scene_count=body.scene_count,
        output_name=body.output_name,
        project_id=body.project_id,
        video_id=body.video_id,
        account=body.account,
        headless=body.headless,
        drive_folder_id=body.drive_folder_id,
        images_drive_folder_id=body.images_drive_folder_id,
    )
    try:
        result = await run_source_text_pipeline(req)
        return {"status": "ok", **result}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc)) from exc
