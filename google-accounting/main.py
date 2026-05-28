import logging
import uuid
import time
import asyncio
import math
import random
from collections import OrderedDict
from contextlib import asynccontextmanager
from typing import Literal, Optional
from pathlib import Path

import httpx
from fastapi import FastAPI, HTTPException, BackgroundTasks, Query
from pydantic import BaseModel

import scheduler as sched
from storage import ensure_dirs, list_downloaded_videos, list_downloaded_images
from playwright_client import (
    generate_video_ai_v2,
    generate_avatar_v1,
    generate_flow_images,
    generate_character_video_v1
)

from playwright_client import list_projects, sync_project

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

# ---------------------------------------------------------------------------
# Job Store — in-memory with TTL and max size
# ---------------------------------------------------------------------------
MAX_JOBS = 1000
JOB_TTL_SECONDS = 24 * 3600  # 24 hours

_jobs: OrderedDict[str, dict] = OrderedDict()


def _now_ts() -> float:
    return time.time()


def _new_job(job_id: str, **fields):
    # Evict oldest if at capacity
    while len(_jobs) >= MAX_JOBS:
        _jobs.popitem(last=False)
    _jobs[job_id] = {
        "job_id": job_id,
        "status": "pending",
        "progress": 0,
        "current_step": "queued",
        "attempts": 1,
        "last_log": "",
        "created_at": _now_ts(),
        "updated_at": _now_ts(),
        **fields,
    }


def _update_job(job_id: str, **fields):
    job = _jobs.setdefault(job_id, {})
    job.update(fields)
    job["updated_at"] = _now_ts()
    # Fire webhook on terminal states
    if fields.get("status") in ("done", "failed"):
        callback_url = job.get("callback_url")
        if callback_url:
            asyncio.create_task(_fire_webhook(callback_url, job))
    return job


# ---------------------------------------------------------------------------
# Webhook helper
# ---------------------------------------------------------------------------
async def _fire_webhook(url: str, payload: dict):
    """POST job result to callback URL. Fire-and-forget with retry."""
    # Sanitize payload — remove non-serializable items
    safe_payload = {k: v for k, v in payload.items() if isinstance(v, (str, int, float, bool, list, dict, type(None)))}
    for attempt in range(3):
        try:
            async with httpx.AsyncClient(timeout=10) as client:
                resp = await client.post(url, json=safe_payload)
                log.info("Webhook delivered to %s — status %d (attempt %d)", url, resp.status_code, attempt + 1)
                if resp.status_code < 500:
                    return  # Success or client error — don't retry
        except Exception as e:
            log.warning("Webhook to %s failed (attempt %d): %s", url, attempt + 1, e)
        if attempt < 2:
            await asyncio.sleep(2 ** attempt)
    log.error("Webhook to %s failed after 3 attempts", url)


# ---------------------------------------------------------------------------
# Auto-Upload to Drive helper
# ---------------------------------------------------------------------------
async def _auto_upload_to_drive(file_path: str, drive_folder_id: str, media_type: str = "video"):
    """Upload a generated file to Google Drive folder."""
    try:
        from reupload_drive_assets import upload_file_to_drive
        p = Path(file_path)
        if not p.exists():
            log.warning("Auto-upload skipped: file not found %s", file_path)
            return None
        
        mime_map = {
            "video": "video/mp4",
            "image": "image/jpeg",
        }
        ext = p.suffix.lower()
        if ext == ".png":
            mimetype = "image/png"
        elif ext == ".gif":
            mimetype = "image/gif"
        elif ext == ".mp4":
            mimetype = "video/mp4"
        else:
            mimetype = mime_map.get(media_type, "application/octet-stream")

        drive_id, drive_link = upload_file_to_drive(drive_folder_id, p, p.name, mimetype)
        log.info("Auto-uploaded %s to Drive folder %s → %s", p.name, drive_folder_id, drive_link)
        return {"drive_file_id": drive_id, "drive_link": drive_link}
    except Exception as e:
        log.error("Auto-upload failed for %s: %s", file_path, e)
        return None


# ---------------------------------------------------------------------------
# Job Cleanup background task
# ---------------------------------------------------------------------------
async def _cleanup_expired_jobs():
    """Periodically removes completed/failed jobs older than JOB_TTL_SECONDS."""
    while True:
        await asyncio.sleep(1800)  # Every 30 minutes
        now = _now_ts()
        expired_ids = [
            jid for jid, job in _jobs.items()
            if job.get("status") in ("done", "failed")
            and (now - job.get("created_at", now)) > JOB_TTL_SECONDS
        ]
        for jid in expired_ids:
            _jobs.pop(jid, None)
        if expired_ids:
            log.info("Job cleanup: removed %d expired jobs", len(expired_ids))


# ---------------------------------------------------------------------------
# FastAPI app
# ---------------------------------------------------------------------------
@asynccontextmanager
async def lifespan(app: FastAPI):
    ensure_dirs()
    sched.start()
    cleanup_task = asyncio.create_task(_cleanup_expired_jobs())
    yield
    cleanup_task.cancel()
    sched.stop()


app = FastAPI(title="Google Automation Hub", lifespan=lifespan)


# ---------------------------------------------------------------------------
# Request Models
# ---------------------------------------------------------------------------
class DownloadRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None


class GenerateRequest(BaseModel):
    video_id: str
    prompt: str
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class CharacterVideoRequest(BaseModel):
    video_id: str = "new"
    character_id: str
    prompt: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class AvatarRequest(BaseModel):
    video_id: str = "new"
    script: str
    avatar_id: Optional[str] = None  # e.g., "professional_male", "friendly_female"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class FlowImageRequest(BaseModel):
    prompt: str
    num_images: Optional[int] = 4
    project_id: Optional[str] = None
    style: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class SyncRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------
async def _enqueue_download(req, background_tasks: BackgroundTasks):
    job_id = str(uuid.uuid4())
    account = req.account or "favamassimo"
    _new_job(job_id, video_id=req.video_id, account=account, file_type=req.file_type,
             callback_url=getattr(req, 'callback_url', None))

    async def _run():
        _update_job(job_id, status="running", progress=5, current_step="listing_exported_files", last_log="Starting download sync")
        try:
            files = await sync_project(req.video_id, file_type=req.file_type, account=account, headless=req.headless)
            if files:
                _update_job(job_id, status="done", progress=100, current_step="completed", files=files, last_log=f"Downloaded {len(files)} files")
            else:
                _update_job(job_id, status="done", progress=100, current_step="completed", files=[], last_log="No files to download or already synced")
        except Exception as e:
            log.exception("Sync failed")
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.get("/health")
async def health():
    running = sum(1 for j in _jobs.values() if j.get("status") == "running")
    return {"status": "ok", "timestamp": time.time(), "active_jobs": running, "total_jobs": len(_jobs)}


@app.get("/list")
async def list_projects_endpoint(account: Optional[str] = None):
    return await list_projects(account=account or "favamassimo")


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
    _new_job(job_id, character_id=req.character_id, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="processing", last_log="Starting character video generation")
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
                    drive_result = await _auto_upload_to_drive(file_path, req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                _update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Character video generation completed", **result_fields)
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Generation failed, no file path returned.")
        except Exception as e:
            log.exception("Character video generation failed")
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-avatar-video")
async def generate_avatar_video_endpoint(req: AvatarRequest, background_tasks: BackgroundTasks):
    """Generates AI Talking Head (Avatar) video via Google Vids using the Lip Sync feature."""
    job_id = f"avatar-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    avatar_id = req.avatar_id or "James"
    _new_job(job_id, script=req.script, avatar_id=avatar_id, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="processing", last_log="Starting avatar video generation")
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
                    drive_result = await _auto_upload_to_drive(str(file_path), req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                _update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Avatar generation completed", **result_fields)
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Generation failed, no file path returned.")
        except Exception as e:
            log.exception("Avatar video generation failed")
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-video")
async def generate_video_ai_endpoint(req: GenerateRequest, background_tasks: BackgroundTasks):
    """Generates AI video via Google Vids."""
    job_id = f"video-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    _new_job(job_id, prompt=req.prompt, video_id=req.video_id, account=account,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="opening_vids", last_log="Opening Google Vids")
        try:
            _update_job(job_id, progress=35, current_step="generating", last_log="Submitting AI request")
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
                    drive_result = await _auto_upload_to_drive(str(video_id), req.drive_folder_id, "video")
                    if drive_result:
                        result_fields.update(drive_result)
                _update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Video generation completed", **result_fields)
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Generation failed, no video ID returned.")
        except Exception as e:
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-flow-images")
async def generate_flow_images_endpoint(req: FlowImageRequest, background_tasks: BackgroundTasks):
    """Generates images via Google Labs ImageFX Flow with parallel instances."""
    job_id = f"flow-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    num_images = req.num_images or 4
    num_instances = math.ceil(num_images / 4)
    
    _new_job(job_id, prompt=req.prompt, account=account, project_id=req.project_id, 
             style=req.style, mode="flow", num_images=num_images, instances=num_instances,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run_instance(instance_idx: int):
        # Ritardo casuale per evitare di sembrare un bot sincronizzato
        delay = random.uniform(2, 10) * instance_idx
        log.info(f"Instance {instance_idx} waiting {delay:.2f}s before starting...")
        await asyncio.sleep(delay)
        
        return await generate_flow_images(
            req.prompt, 
            project_id=req.project_id, 
            style=req.style, 
            account=account, 
            headless=req.headless
        )

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="starting_instances", 
                   last_log=f"Starting {num_instances} parallel instances")
        try:
            tasks = [_run_instance(i) for i in range(num_instances)]
            results = await asyncio.gather(*tasks, return_exceptions=True)
            
            all_paths = []
            errors = []
            for i, res in enumerate(results):
                if isinstance(res, Exception):
                    log.error(f"Instance {i} failed: {res}")
                    errors.append(str(res))
                else:
                    all_paths.extend(res)
            
            if all_paths:
                result_fields = {"files": all_paths, "errors": errors if errors else None}
                # Auto-upload each image to Drive
                if req.drive_folder_id:
                    drive_results = []
                    for img_path in all_paths:
                        dr = await _auto_upload_to_drive(img_path, req.drive_folder_id, "image")
                        if dr:
                            drive_results.append(dr)
                    if drive_results:
                        result_fields["drive_uploads"] = drive_results
                _update_job(job_id, status="done", progress=100, current_step="completed",
                           last_log=f"Flow generation completed: {len(all_paths)} images generated", **result_fields)
            else:
                error_msg = f"Generation failed. Errors: {', '.join(errors)}" if errors else "No images captured."
                _update_job(job_id, status="failed", current_step="failed", error=error_msg, last_log=error_msg)
        except Exception as e:
            log.exception("Flow generation orchestrator failed")
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


# ---------------------------------------------------------------------------
# Job Management Endpoints
# ---------------------------------------------------------------------------
@app.get("/status/{job_id}")
async def get_status(job_id: str):
    """Check the status of a single background job."""
    job = _jobs.get(job_id)
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
    jobs = list(_jobs.values())
    
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
    job = _jobs.pop(job_id, None)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return {"job_id": job_id, "status": "cancelled", "detail": "Job removed from store"}
