import logging
import uuid
import time
import asyncio
from contextlib import asynccontextmanager
from typing import Literal, Optional

from fastapi import FastAPI, HTTPException, BackgroundTasks
from pydantic import BaseModel

import scheduler as sched
from storage import ensure_dirs, list_downloaded_videos, list_downloaded_images
from playwright_client import (
    generate_video_ai_v2, 
    generate_flow_images
)

# NOTE: These will be implemented in playwright_client.py shortly
from playwright_client import list_projects, sync_project

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


class BulkDownloadRequest(BaseModel):
    video_ids: list[str]
    file_type: Literal["video", "image", "all"] = "all"
    headless: bool = False
    account: Optional[str] = None


class GenerateRequest(BaseModel):
    video_id: str
    prompt: str
    headless: bool = True
    account: Optional[str] = None

class FlowImageRequest(BaseModel):
    prompt: str
    project_id: Optional[str] = None
    style: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None


# ── Endpoints ─────────────────────────────────────────────────────────────────

@app.post("/generate-vids-video")
async def generate_vids_video_endpoint(req: GenerateRequest, background_tasks: BackgroundTasks):
    """Generates and downloads a video via Google Vids."""
    job_id = f"vids-{str(uuid.uuid4())[:8]}"
    _jobs[job_id] = {
        "status": "pending", 
        "video_id": req.video_id, 
        "prompt": req.prompt,
        "account": req.account,
        "created_at": time.time()
    }

    async def _run():
        _jobs[job_id]["status"] = "running"
        try:
            video_path = await generate_video_ai_v2(req.video_id, req.prompt, account=req.account, headless=req.headless)
            if video_path:
                _jobs[job_id]["status"] = "done"
                _jobs[job_id]["file_path"] = video_path
            else:
                _jobs[job_id]["status"] = "failed"
                _jobs[job_id]["error"] = "Generation/Download failed."
        except Exception as e:
            _jobs[job_id]["status"] = "failed"
            _jobs[job_id]["error"] = str(e)

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}

@app.post("/generate-flow-images")
async def generate_flow_images_endpoint(req: FlowImageRequest, background_tasks: BackgroundTasks):
    """Generates images via Google Labs ImageFX Flow."""
    job_id = f"flow-{str(uuid.uuid4())[:8]}"
    _jobs[job_id] = {
        "status": "pending", 
        "prompt": req.prompt,
        "account": req.account,
        "created_at": time.time()
    }

    async def _run():
        _jobs[job_id]["status"] = "running"
        try:
            paths = await generate_flow_images(
                req.prompt, 
                project_id=req.project_id, 
                style=req.style, 
                account=req.account, 
                headless=req.headless
            )
            if paths:
                _jobs[job_id]["status"] = "done"
                _jobs[job_id]["files"] = paths
            else:
                _jobs[job_id]["status"] = "failed"
                _jobs[job_id]["error"] = "Generation failed or no images captured."
        except Exception as e:
            _jobs[job_id]["status"] = "failed"
            _jobs[job_id]["error"] = str(e)

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.get("/list")
async def get_projects(account: Optional[str] = None, headless: bool = True):
    """List all Google Vids projects."""
    try:
        projects = await list_projects(account=account, headless=headless)
        return {"projects": projects, "count": len(projects)}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/downloads")
async def get_local_downloads():
    """List files already downloaded locally."""
    return {
        "videos": list_downloaded_videos(),
        "images": list_downloaded_images(),
    }


@app.post("/download")
async def download(req: DownloadRequest, background_tasks: BackgroundTasks):
    """Download video and/or images from a Google Vids project."""
    job_id = str(uuid.uuid4())
    _jobs[job_id] = {"status": "pending", "video_id": req.video_id, "account": req.account}

    async def _run():
        _jobs[job_id]["status"] = "running"
        try:
            paths = await sync_project(req.video_id, req.file_type, account=req.account, headless=req.headless)
            _jobs[job_id]["status"] = "done"
            _jobs[job_id]["files"] = [str(p) for p in paths]
        except Exception as e:
            _jobs[job_id]["status"] = "failed"
            _jobs[job_id]["error"] = str(e)
            log.error("Download failed for %s: %s", req.video_id, e)

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.get("/status/{job_id}")
async def get_status(job_id: str):
    """Check the status of a background job."""
    job = _jobs.get(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job not found")
    return job
