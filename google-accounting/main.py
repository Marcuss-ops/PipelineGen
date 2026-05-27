import logging
import uuid
import time
import asyncio
from contextlib import asynccontextmanager
from typing import Literal, Optional
from pathlib import Path

from fastapi import FastAPI, HTTPException, BackgroundTasks
from pydantic import BaseModel

import scheduler as sched
from storage import ensure_dirs, list_downloaded_videos, list_downloaded_images
from playwright_client import (
    generate_video_ai_v2,
    generate_flow_images
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

_jobs: dict[str, dict] = {}


def _now_ts() -> float:
    return time.time()


def _new_job(job_id: str, **fields):
    _jobs[job_id] = {
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
    return job


@asynccontextmanager
async def lifespan(app: FastAPI):
    ensure_dirs()
    sched.start()
    yield
    sched.stop()


app = FastAPI(title="Google Automation Hub", lifespan=lifespan)


class DownloadRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None


class GenerateRequest(BaseModel):
    video_id: str
    prompt: str
    headless: bool = True
    account: Optional[str] = None

class AvatarRequest(BaseModel):
    video_id: str = "new"
    script: str
    avatar_id: Optional[str] = None  # e.g., "professional_male", "friendly_female"
    headless: bool = True
    account: Optional[str] = None


class FlowImageRequest(BaseModel):
    prompt: str
    project_id: Optional[str] = None
    style: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None


class SyncRequest(BaseModel):
    video_id: str
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = True
    account: Optional[str] = None


async def _enqueue_download(req, background_tasks: BackgroundTasks):
    job_id = str(uuid.uuid4())
    account = req.account or "favamassimo"
    _new_job(job_id, video_id=req.video_id, account=account, file_type=req.file_type)

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
    return {"status": "ok", "timestamp": time.time()}


@app.get("/list")
async def list_projects_endpoint(account: Optional[str] = None):
    return await list_projects(account=account or "favamassimo")


@app.post("/download")
async def download_endpoint(req: DownloadRequest, background_tasks: BackgroundTasks):
    return await _enqueue_download(req, background_tasks)


@app.post("/sync")
async def sync_endpoint(req: SyncRequest, background_tasks: BackgroundTasks):
    return await _enqueue_download(req, background_tasks)


@app.post("/generate-video")
async def generate_video_ai_endpoint(req: GenerateRequest, background_tasks: BackgroundTasks):
    """Generates AI video via Google Vids."""
    job_id = f"video-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    _new_job(job_id, prompt=req.prompt, video_id=req.video_id, account=account)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="opening_vids", last_log="Opening Google Vids")
        try:
            _update_job(job_id, progress=35, current_step="generating", last_log="Submitting AI request")
            video_id = await generate_video_ai_v2(
                req.prompt, 
                video_id=req.video_id, 
                account=account, 
                headless=req.headless
            )
            if video_id:
                _update_job(job_id, status="done", progress=100, current_step="completed", video_id=video_id, last_log="Video generation completed")
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Generation failed, no video ID returned.")
        except Exception as e:
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-flow-images")
async def generate_flow_images_endpoint(req: FlowImageRequest, background_tasks: BackgroundTasks):
    """Generates images via Google Labs ImageFX Flow."""
    job_id = f"flow-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    _new_job(job_id, prompt=req.prompt, account=account, project_id=req.project_id, style=req.style, mode="flow")

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="opening_flow", last_log="Opening Google Flow")
        try:
            _update_job(job_id, progress=35, current_step="generating", last_log="Submitting Flow prompt")
            paths = await generate_flow_images(
                req.prompt, 
                project_id=req.project_id, 
                style=req.style, 
                account=account, 
                headless=req.headless
            )
            if paths:
                _update_job(job_id, status="done", progress=100, current_step="completed", files=paths, last_log="Flow generation completed")
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Generation failed or no images captured.", last_log="Generation failed or no images captured.")
        except Exception as e:
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.get("/status/{job_id}")
async def get_status(job_id: str):
    """Check the status of a background job."""
    job = _jobs.get(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return job
