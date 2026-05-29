import json
import logging
import uuid
import time
import asyncio
import math
import random
import re
from datetime import datetime, timezone
from collections import OrderedDict
from contextlib import asynccontextmanager
from typing import Literal, Optional
from pathlib import Path

import httpx
import yaml
from fastapi import FastAPI, HTTPException, BackgroundTasks, Query
from pydantic import BaseModel

import scheduler as sched
from storage import ensure_dirs, list_downloaded_videos, list_downloaded_images
from style_presets import STYLE_PRESETS
from playwright_client import (
    generate_video_ai_v2,
    generate_avatar_v1,
    generate_flow_images,
    generate_character_video_v1,
    generate_vids_image_v1,
    generate_vids_image_v1_pooled,
)
from script_routes import router as script_router

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
STYLE_NAMES = tuple(STYLE_PRESETS.keys())

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


def _style_prompt_suffix(style: Optional[str]) -> str:
    if not style:
        return ""
    return STYLE_PRESETS.get(style, "")


def _compose_styled_prompt(prompt: str, style: Optional[str]) -> str:
    suffix = _style_prompt_suffix(style)
    if suffix:
        return f"{prompt}, {suffix}"
    return prompt


def _require_valid_style(style: Optional[str]) -> Optional[str]:
    if style is None:
        return None
    if style not in STYLE_PRESETS:
        raise HTTPException(status_code=400, detail=f"Unsupported style '{style}'")
    return style


def _split_script_into_scenes(script: str) -> list[str]:
    blocks = [block.strip() for block in script.split("\n\n") if block.strip()]
    if len(blocks) > 1:
        return blocks

    sentences = [part.strip() for part in re.split(r"(?<=[.!?])\s+", script.strip()) if part.strip()]
    return sentences if sentences else [script.strip()]


def _text_generation_config_path() -> Path:
    return Path(__file__).resolve().with_name("text_generation.yaml")


def _load_text_generation_config() -> dict:
    config_path = _text_generation_config_path()
    if not config_path.exists():
        raise HTTPException(status_code=500, detail=f"Missing text generation config: {config_path.name}")
    try:
        data = yaml.safe_load(config_path.read_text(encoding="utf-8")) or {}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"Failed to parse {config_path.name}: {exc}") from exc
    if not isinstance(data, dict):
        raise HTTPException(status_code=500, detail=f"Invalid {config_path.name} format")
    return data


def _build_script_generation_messages(req: "GenerateScriptRequest", config: dict) -> list[dict]:
    styles = config.get("styles") or {}
    style_key = req.style or "deep_dive"
    style_cfg = styles.get(style_key)
    if not style_cfg:
        raise HTTPException(status_code=400, detail=f"Unsupported script style '{style_key}'")

    system_prompt = style_cfg.get("system_prompt", "").strip()
    if not system_prompt:
        raise HTTPException(status_code=500, detail=f"Missing system prompt for style '{style_key}'")

    language = (req.language or "EN").strip().upper()
    audience = (req.audience or "").strip()
    tone = (req.tone or "").strip()
    scene_count = req.scene_count or 8

    user_prompt = [
        f"Topic: {req.prompt.strip()}",
        f"Language: {language}",
        f"Target scene count: {scene_count}",
        f"Audience: {audience or 'general'}",
        f"Tone: {tone or 'natural'}",
        "",
        "Return JSON only with this shape:",
        "{",
        '  "title": "...",',
        '  "style": "...",',
        '  "language": "...",',
        '  "script": "...",',
        '  "scenes": [',
        '    {"id": "scene_001", "speaker": "...", "text": "...", "image_hint": "..."}',
        "  ]",
        "}",
    ]

    return [
        {"role": "system", "content": system_prompt},
        {"role": "user", "content": "\n".join(user_prompt)},
    ]


async def _call_ollama_chat(config: dict, messages: list[dict]) -> dict:
    backend = config.get("backend") or {}
    if (backend.get("type") or "").lower() != "ollama":
        raise HTTPException(status_code=500, detail="Only ollama backend is supported by this endpoint")

    base_url = (backend.get("base_url") or "http://127.0.0.1:11434").rstrip("/")
    model = backend.get("model") or "gemma2:9b"
    timeout_seconds = int(backend.get("timeout_seconds") or 120)
    payload = {
        "model": model,
        "messages": messages,
        "stream": False,
        "format": "json",
        "options": {
            "temperature": 0.7,
        },
    }

    try:
        async with httpx.AsyncClient(timeout=timeout_seconds) as client:
            resp = await client.post(f"{base_url}/api/chat", json=payload)
            resp.raise_for_status()
            return resp.json()
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=503, detail=f"Ollama backend unavailable: {exc}") from exc


def _extract_ollama_message_content(response: dict) -> str:
    message = response.get("message") or {}
    content = message.get("content")
    if not content:
        raise HTTPException(status_code=502, detail="Ollama returned no message content")
    return content


def _parse_generated_script(content: str) -> dict:
    try:
        parsed = json.loads(content)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"Model output was not valid JSON: {exc}") from exc
    if not isinstance(parsed, dict):
        raise HTTPException(status_code=502, detail="Model output JSON must be an object")
    return parsed


def _write_generated_script_files(payload: dict, config: dict, req: "GenerateScriptRequest") -> dict:
    output_cfg = config.get("output") or {}
    root_dir = Path(__file__).resolve().parent / (output_cfg.get("root_dir") or "docs/generated")
    root_dir.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
    style = (req.style or "deep_dive").strip()
    base_name = (req.output_name or output_cfg.get("file_prefix") or "script").strip() or "script"
    safe_base = re.sub(r"[^A-Za-z0-9._-]+", "_", base_name).strip("_") or "script"
    safe_style = re.sub(r"[^A-Za-z0-9._-]+", "_", style).strip("_") or "deep_dive"
    stem = f"{timestamp}_{safe_base}_{safe_style}"

    json_path = root_dir / f"{stem}.json"
    md_path = root_dir / f"{stem}.md"

    meta = {
        "project_id": req.project_id,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "backend": "ollama",
        "model": (config.get("backend") or {}).get("model"),
        "style": style,
        "language": (req.language or "EN").strip().upper(),
        "source_prompt": req.prompt,
    }
    json_payload = {
        "meta": meta,
        "content": payload,
    }
    json_path.write_text(json.dumps(json_payload, indent=2, ensure_ascii=False), encoding="utf-8")

    md_lines = [
        f"# {payload.get('title') or safe_base}",
        "",
        f"- Style: `{style}`",
        f"- Language: `{meta['language']}`",
        f"- Model: `{meta['model']}`",
        f"- Generated at: `{meta['generated_at']}`",
        "",
        "## Script",
        "",
        payload.get("script", "").strip(),
        "",
        "## Scenes",
        "",
    ]
    for scene in payload.get("scenes", []) or []:
        md_lines.append(f"### {scene.get('id', 'scene')}")
        speaker = scene.get("speaker")
        if speaker:
            md_lines.append(f"- Speaker: {speaker}")
        if scene.get("text"):
            md_lines.append(f"- Text: {scene['text']}")
        if scene.get("image_hint"):
            md_lines.append(f"- Image hint: {scene['image_hint']}")
        md_lines.append("")

    md_path.write_text("\n".join(md_lines).rstrip() + "\n", encoding="utf-8")

    return {
        "json_path": str(json_path),
        "markdown_path": str(md_path),
        "output_dir": str(root_dir),
    }


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
    from session_pool import pool as session_pool_instance
    ensure_dirs()
    sched.start()
    await session_pool_instance.start()
    log.info("Session pool initialized — warm contexts ready")
    cleanup_task = asyncio.create_task(_cleanup_expired_jobs())
    asyncio.create_task(session_pool_instance.warmup_account("favamassimo"))
    yield
    cleanup_task.cancel()
    await session_pool_instance.stop()
    sched.stop()


app = FastAPI(title="Google Automation Hub", lifespan=lifespan)
app.include_router(script_router)


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


class VidsImageRequest(BaseModel):
    video_id: str = "new"
    prompt: str
    style: Optional[str] = None
    headless: bool = True
    account: Optional[str] = None
    callback_url: Optional[str] = None
    drive_folder_id: Optional[str] = None


class StoryboardRequest(BaseModel):
    script: str
    style: Optional[str] = None
    language: Optional[str] = "it"
    project_id: Optional[str] = None
    file_prefix: Optional[str] = "scena"


class GenerateScriptRequest(BaseModel):
    prompt: str
    style: Optional[str] = "deep_dive"
    language: Optional[str] = "EN"
    scene_count: Optional[int] = 8
    audience: Optional[str] = None
    tone: Optional[str] = None
    output_name: Optional[str] = None
    project_id: Optional[str] = None


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


@app.get("/styles")
async def list_style_presets():
    return {"styles": STYLE_PRESETS}


@app.post("/generate-script")
async def generate_script_endpoint(req: GenerateScriptRequest):
    """Generates a new script from a prompt and writes it to docs/generated."""
    config = _load_text_generation_config()
    messages = _build_script_generation_messages(req, config)
    ollama_response = await _call_ollama_chat(config, messages)
    content = _extract_ollama_message_content(ollama_response)
    payload = _parse_generated_script(content)

    if "script" not in payload:
        raise HTTPException(status_code=502, detail="Generated script JSON missing 'script' field")
    if "scenes" not in payload:
        payload["scenes"] = []

    write_result = _write_generated_script_files(payload, config, req)

    return {
        "status": "ok",
        "style": req.style or "deep_dive",
        "language": (req.language or "EN").strip().upper(),
        "output": write_result,
        "script": payload,
    }


@app.post("/generate-storyboard")
async def generate_storyboard_endpoint(req: StoryboardRequest):
    """Builds a storyboard JSON draft from a script using the selected image style."""
    style = _require_valid_style(req.style)
    style_suffix = _style_prompt_suffix(req.style)
    scenes = _split_script_into_scenes(req.script)
    prefix = (req.file_prefix or "scena").strip() or "scena"
    language = (req.language or "it").strip().upper() or "IT"

    storyboard = []
    for idx, text in enumerate(scenes, start=1):
        scene_id = f"{prefix}_{idx:03d}"
        storyboard.append(
            {
                "id_scena": scene_id,
                "testo_originale": text,
                "prompt_immagine": f"{text}, {style_suffix}" if style_suffix else text,
                "file_immagine_atteso": f"immagini/{scene_id}.png",
                "file_audio_atteso": f"audio/{language}_{scene_id}.mp3",
            }
        )

    return {
        "project_id": req.project_id,
        "style": style,
        "language": language,
        "scenes": storyboard,
    }


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
    """Generates images via Google Flow using the selected style preset."""
    job_id = f"flow-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    style = _require_valid_style(req.style)
    styled_prompt = _compose_styled_prompt(req.prompt, req.style)
    _new_job(job_id, prompt=req.prompt, account=account, project_id=req.project_id,
             style=style, mode="google-flow", requested_num_images=req.num_images,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="generating",
                   last_log="Starting Google Flow image generation")
        try:
            files = await generate_flow_images(
                prompt=req.prompt,
                project_id=req.project_id,
                style=style,
                account=account,
                headless=req.headless,
            )
            if files:
                result_fields = {
                    "files": files,
                    "styled_prompt": styled_prompt,
                }
                if req.drive_folder_id:
                    drive_results = []
                    for img_path in files:
                        dr = await _auto_upload_to_drive(img_path, req.drive_folder_id, "image")
                        if dr:
                            drive_results.append(dr)
                    if drive_results:
                        result_fields["drive_uploads"] = drive_results
                _update_job(job_id, status="done", progress=100, current_step="completed",
                           last_log=f"Flow image generation completed: {len(files)} images generated", **result_fields)
            else:
                _update_job(job_id, status="failed", current_step="failed", error="No images captured.", last_log="No images captured.")
        except Exception as e:
            log.exception("Flow image generation failed")
            _update_job(job_id, status="failed", current_step="failed", error=str(e), last_log=str(e))

    background_tasks.add_task(_run)
    return {"job_id": job_id, "status": "pending"}


@app.post("/generate-vids-images")
async def generate_vids_image_endpoint(req: VidsImageRequest, background_tasks: BackgroundTasks):
    """Genera immagini via Google Vids Image Synthesis."""
    job_id = f"vids-img-{str(uuid.uuid4())[:8]}"
    account = req.account or "favamassimo"
    style = _require_valid_style(req.style)
    styled_prompt = _compose_styled_prompt(req.prompt, req.style)
    _new_job(job_id, prompt=req.prompt, video_id=req.video_id, account=account,
             style=style,
             callback_url=req.callback_url, drive_folder_id=req.drive_folder_id)

    async def _run():
        _update_job(job_id, status="running", progress=10, current_step="opening_vids", last_log="Opening Google Vids for image generation")
        try:
            _update_job(job_id, progress=35, current_step="generating_image", last_log="Generating image via Vids Image Synthesis")
            file_path = await generate_vids_image_v1_pooled(
                video_id=req.video_id,
                prompt=styled_prompt,
                account=account,
            )
            if file_path:
                result_fields = {"file_path": str(file_path)}
                # Auto-upload to Drive
                if req.drive_folder_id:
                    drive_result = await _auto_upload_to_drive(str(file_path), req.drive_folder_id, "image")
                    if drive_result:
                        result_fields.update(drive_result)
                _update_job(job_id, status="done", progress=100, current_step="completed",
                            last_log="Vids image generation completed", **result_fields)
            else:
                _update_job(job_id, status="failed", current_step="failed", error="Image generation failed, no file path returned.")
        except Exception as e:
            log.exception("Vids image generation failed")
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
