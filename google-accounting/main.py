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

class CharacterVideoRequest(BaseModel):
    video_id: str = "new"
    character_id: str
    prompt: Optional[str] = None
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
    num_images: Optional[int] = 4
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


@app.post("/generate-character-video")
async def generate_character_video_endpoint(req: CharacterVideoRequest, background_tasks: BackgroundTasks):
    """Generates AI video using a character's reference image."""
    job_id = f"char-video-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    _new_job(job_id, character_id=req.character_id, video_id=req.video_id, account=account)

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
                _update_job(job_id, status="done", progress=100, current_step="completed", file_path=file_path, last_log="Character video generation completed")
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
    _new_job(job_id, script=req.script, avatar_id=avatar_id, video_id=req.video_id, account=account)

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
                _update_job(job_id, status="done", progress=100, current_step="completed", file_path=str(file_path), last_log="Avatar generation completed")
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


import math
import random

@app.post("/generate-flow-images")
async def generate_flow_images_endpoint(req: FlowImageRequest, background_tasks: BackgroundTasks):
    """Generates images via Google Labs ImageFX Flow with parallel instances."""
    job_id = f"flow-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    num_images = req.num_images or 4
    num_instances = math.ceil(num_images / 4)
    
    _new_job(job_id, prompt=req.prompt, account=account, project_id=req.project_id, 
             style=req.style, mode="flow", num_images=num_images, instances=num_instances)

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
                _update_job(job_id, status="done", progress=100, current_step="completed", 
                           files=all_paths, errors=errors if errors else None,
                           last_log=f"Flow generation completed: {len(all_paths)} images generated")
            else:
                error_msg = f"Generation failed. Errors: {', '.join(errors)}" if errors else "No images captured."
                _update_job(job_id, status="failed", current_step="failed", error=error_msg, last_log=error_msg)
        except Exception as e:
            log.exception("Flow generation orchestrator failed")
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
