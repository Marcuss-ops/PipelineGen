import json
import logging
import uuid
import time
import asyncio
import math
import random
import re
from datetime import datetime, timezone
from contextlib import asynccontextmanager
from typing import Literal, Optional
from pathlib import Path

import httpx
import yaml
from fastapi import FastAPI, HTTPException, BackgroundTasks, Query
from models import DownloadRequest, GenerateRequest, CharacterVideoRequest, AvatarRequest, VidsImageRequest, SyncRequest

import scheduler as sched
from storage import ensure_dirs, list_downloaded_videos, list_downloaded_images
from style_presets import STYLE_PRESETS, require_valid_style, compose_styled_prompt
from job_manager import new_job, update_job, get_job, get_all_jobs, delete_job, cleanup_expired_jobs

from playwright_client import (
    generate_video_ai_v2,
    generate_avatar_v1,
    generate_character_video_v1,
    generate_vids_image_v1,
    generate_vids_image_v1_pooled,
)
from script_routes import router as script_router

from playwright_client import list_projects, sync_project
from drive_client import auto_upload_to_drive

Path("logs").mkdir(parents=True, exist_ok=True)
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    handlers=[
        logging.StreamHandler(),
        logging.FileHandler("logs/sync.log"),
    ],
)
log = logging.getLogger(__name__)
STYLE_NAMES = tuple(STYLE_PRESETS.keys())



# ---------------------------------------------------------------------------
# FastAPI app
# ---------------------------------------------------------------------------
@asynccontextmanager
async def lifespan(app: FastAPI):
    from session_pool import pool as session_pool_instance
    ensure_dirs()
    sched.start()
    await session_pool_instance.start()
    log.info("Session pool initialized — warm contexts ready")
    cleanup_task = asyncio.create_task(cleanup_expired_jobs())
    asyncio.create_task(session_pool_instance.warmup_account("favamassimo"))
    yield
    cleanup_task.cancel()
    await session_pool_instance.stop()
    sched.stop()


app = FastAPI(title="Google Automation Hub", lifespan=lifespan)
app.include_router(script_router)


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------
async def _enqueue_download(req, background_tasks: BackgroundTasks):
    job_id = str(uuid.uuid4())
    account = req.account or "favamassimo"
    new_job(job_id, video_id=req.video_id, account=account, file_type=req.file_type,
             callback_url=getattr(req, 'callback_url', None))

    async def _run():
        update_job(job_id, status="running", progress=5, current_step="listing_exported_files", last_log="Starting download sync")
        try:
            files = await sync_project(req.video_id, file_type=req.file_type, account=account, headless=req.headless)
            if files:
                update_job(job_id, status="done", progress=100, current_step="completed", files=files, last_log=f"Downloaded {len(files)} files")
            else:
                update_job(job_id, status="done", progress=100, current_step="completed", files=[], last_log="No files to download or already synced")
        except Exception as e:
            log.exception("Sync failed")
            update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.get("/health")
async def health():
    running = sum(1 for j in get_all_jobs().values() if j.get("status") == "running")
    return {"status": "ok", "timestamp": time.time(), "active_jobs": running, "total_jobs": len(get_all_jobs())}


@app.get("/list")
async def list_projects_endpoint(account: Optional[str] = None):
    return await list_projects(account=account or "favamassimo")


@app.get("/styles")
async def list_style_presets():
    return {"styles": STYLE_PRESETS}


@app.post("/download")
async def download_endpoint(req: DownloadRequest, background_tasks: BackgroundTasks):
    return await _enqueue_download(req, background_tasks)


@app.post("/sync")
async def sync_endpoint(req: SyncRequest, background_tasks: BackgroundTasks):
    return await _enqueue_download(req, background_tasks)


@app.post("/generate-character-video")
async def generate_character_video_endpoint(req: CharacterVideoRequest, background_tasks: BackgroundTasks):
    """Generates AI video using a character's reference image."""
    job_id = f"char-video-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    new_job(job_id, character_id=req.character_id, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        update_job(job_id, status="running", progress=10, current_step="processing", last_log="Starting character video generation")
        try:
            file_path = await generate_character_video_v1(
                video_id=req.video_id,
                character_id=req.character_id,
                prompt=req.prompt,
                account=account,
                headless=req.headless
            )
            if file_path:
                result_fields = {"file_path": file_path}
                # Auto-upload to Drive
                if req.drive_folder_id:
                    drive_result = await auto_upload_to_drive(file_path, req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Character video generation completed", **result_fields)
            else:
                update_job(job_id, status="failed", current_step="failed", error="Generation failed, no file path returned.")
        except Exception as e:
            log.exception("Character video generation failed")
            update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-avatar-video")
async def generate_avatar_video_endpoint(req: AvatarRequest, background_tasks: BackgroundTasks):
    """Generates AI Talking Head (Avatar) video via Google Vids using the Lip Sync feature."""
    job_id = f"avatar-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    avatar_id = req.avatar_id or "James"
    new_job(job_id, script=req.script, avatar_id=avatar_id, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        update_job(job_id, status="running", progress=10, current_step="processing", last_log="Starting avatar video generation")
        try:
            file_path = await generate_avatar_v1(
                video_id=req.video_id,
                script=req.script,
                avatar_id=avatar_id,
                account=account,
                headless=req.headless
            )
            if file_path:
                result_fields = {"file_path": str(file_path)}
                # Auto-upload to Drive
                if req.drive_folder_id:
                    drive_result = await auto_upload_to_drive(str(file_path), req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Avatar generation completed", **result_fields)
            else:
                update_job(job_id, status="failed", current_step="failed", error="Generation failed, no file path returned.")
        except Exception as e:
            log.exception("Avatar video generation failed")
            update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-video")
async def generate_video_ai_endpoint(req: GenerateRequest, background_tasks: BackgroundTasks):
    """Generates AI video via Google Vids."""
    job_id = f"video-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    new_job(job_id, prompt=req.prompt, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        update_job(job_id, status="running", progress=10, current_step="opening_vids", last_log="Opening Google Vids")
        try:
            update_job(job_id, progress=35, current_step="generating", last_log="Submitting AI request")
            video_id = await generate_video_ai_v2(
                video_id=req.video_id,
                prompt=req.prompt, 
                account=account, 
                headless=req.headless
            )
            if video_id:
                result_fields = {"video_id": video_id, "file_path": str(video_id)}
                # Auto-upload to Drive
                if req.drive_folder_id and Path(str(video_id)).exists():
                    drive_result = await auto_upload_to_drive(str(video_id), req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Video generation completed", **result_fields)
            else:
                update_job(job_id, status="failed", current_step="failed", error="Generation failed, no video ID returned.")
        except Exception as e:
            update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-vids-images")
async def generate_vids_image_endpoint(req: VidsImageRequest, background_tasks: BackgroundTasks):
    """Genera immagini via Google Vids Image Synthesis."""
    job_id = f"vids-img-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    style = require_valid_style(req.style)
    styled_prompt = compose_styled_prompt(req.prompt, req.style)
    new_job(job_id, prompt=req.prompt, video_id=req.video_id, account=account,
             style=style,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        update_job(job_id, status="running", progress=10, current_step="opening_vids", last_log="Opening Google Vids for image generation")
        try:
            update_job(job_id, progress=35, current_step="generating_image", last_log=f"Generating {req.num_images} image(s) via Vids Image Synthesis")
            files = []
            
            # Use gather to generate concurrently using pooled tabs/pages
            tasks = []
            for i in range(req.num_images or 1):
                tasks.append(
                    generate_vids_image_v1_pooled(
                        video_id=req.video_id,
                        prompt=styled_prompt,
                        account=account,
                    )
                )
            
            results = await asyncio.gather(*tasks, return_exceptions=True)
            for res in results:
                if isinstance(res, Exception):
                    log.error(f"Image generation sub-task failed: {res}")
                elif res:
                    files.append(str(res))
            
            if files:
                result_fields = {
                    "files": files,
                    "styled_prompt": styled_prompt,
                }
                # Auto-upload to Drive
                if req.drive_folder_id:
                    drive_results = []
                    for img_path in files:
                        dr = await auto_upload_to_drive(img_path, req.drive_folder_id, "image")
                        if dr:
                            drive_results.append(dr)
                    if drive_results:
                        result_fields["drive_uploads"] = drive_results
                update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log=f"Vids image generation completed: {len(files)} generated", **result_fields)
            else:
                update_job(job_id, status="failed", current_step="failed", error="Image generation failed, no files returned.")
        except Exception as e:
            log.exception("Vids image generation failed")
            update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


# ---------------------------------------------------------------------------
# Job Management Endpoints
# ---------------------------------------------------------------------------
@app.get("/status/{job_id}")
async def get_status(job_id: str):
    """Check the status of a single background job."""
    job = get_job(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return job


@app.get("/jobs")
async def list_jobs(
    status: Optional[str] = Query(None, description="Filter by status: pending, running, done, failed"),
    limit: int = Query(50, ge=1, le=200, description="Max results"),
    offset: int = Query(0, ge=0, description="Offset for pagination"),
):
    """List all jobs with optional filtering and pagination."""
    jobs = list(get_all_jobs().values())
    
    # Filter by status
    if status:
        jobs = [j for j in jobs if j.get("status") == status]
    
    # Sort by created_at descending (newest first)
    jobs.sort(key=lambda j: j.get("created_at", 0), reverse=True)
    
    total = len(jobs)
    paginated = jobs[offset:offset + limit]
    
    return {
        "total": total,
        "offset": offset,
        "limit": limit,
        "jobs": paginated,
    }


@app.delete("/jobs/{job_id}")
async def cancel_job(job_id: str):
    """Cancel/remove a job from the store."""
    if delete_job(job_id):
        return {"job_id": job_id, "status": "cancelled", "detail": "Job removed from store"}
    raise HTTPException(status_code=404, detail="Job not found")


@app.post("/keep-alive")
async def trigger_keep_alive(
    account: Optional[str] = Query(None, description="Account name for keep-alive"),
    headed: bool = Query(False, description="Run browser in headed mode")
):
    """Trigger the keep-alive human-like automated simulation manually."""
    from keep_alive import run_keep_alive
    success = await run_keep_alive(account=account or "favamassimo", headless=not headed)
    if success:
        return {"status": "success", "message": "Keep-alive simulation completed successfully."}
    else:
        raise HTTPException(status_code=500, detail="Keep-alive simulation failed. Check logs.")
