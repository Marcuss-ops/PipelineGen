
import json
import time
import os
import secrets
import logging
import threading
import shutil
from datetime import datetime, timezone
from pathlib import Path
from typing import Dict, Any, List, Optional, Tuple
from fastapi import HTTPException
from . import workers

from . import state
from .config import (
    QUEUE_FILE,
    POISON_JOBS_FILE,
    PROJECTS_FILE,
    JOB_EVENTS_FILE,
    JOB_LEASE_TTL_SECONDS,
    JOB_ZOMBIE_CHECK_INTERVAL,
    AUTO_CLEANUP_JOBS_INTERVAL,
    AUTO_CLEANUP_OLD_HOURS,
    UPLOAD_RETRY_CHECK_INTERVAL,
    UPLOAD_ARTIFACT_CLEANUP_INTERVAL,
    UPLOAD_DB_RETENTION_DAYS,
    FILE_HASH_RETENTION_DAYS,
    COMPLETED_VIDEOS_RETENTION_DAYS,
    VIDEOS_DIR,
    _VIDEO_UPLOAD_DB_PATH
)

# Late import to avoid circular dependency if possible, or use local import
# We need VideoUploadStorage. It was in job_master_server.py
try:
    from ...modules.video_upload_storage import VideoUploadStorage
except ImportError:
    # Fallback if structure is different
    try:
        from modules.video_upload_storage import VideoUploadStorage
    except ImportError:
        VideoUploadStorage = None

logger = logging.getLogger(__name__)

# Initialize storage
if VideoUploadStorage:
    state.video_upload_storage = VideoUploadStorage(db_path=_VIDEO_UPLOAD_DB_PATH)
else:
    logger.warning("VideoUploadStorage module not found.")

_queue_cache = None
_queue_cache_mtime = 0
_queue_backup_file = QUEUE_FILE.with_suffix(".bak")

def load_queue() -> Dict[str, Any]:
    """Carica la coda job dal file con caching basato su mtime."""
    global _queue_cache, _queue_cache_mtime
    
    if not QUEUE_FILE.exists():
        _queue_cache = {}
        _queue_cache_mtime = 0
        return {}
        
    try:
        mtime = QUEUE_FILE.stat().st_mtime
        if _queue_cache is not None and mtime <= _queue_cache_mtime:
            return _queue_cache
            
        with open(QUEUE_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
            _queue_cache = data
            _queue_cache_mtime = mtime
            return data
    except Exception as e:
        logger.error(f"Errore caricamento coda job: {e}")
        # Fallback robusto: prova backup atomico se il file principale e' corrotto/troncato.
        try:
            if _queue_backup_file.exists():
                with open(_queue_backup_file, "r", encoding="utf-8") as f:
                    data = json.load(f)
                logger.warning("⚠️ Coda ripristinata da backup .bak")
                _queue_cache = data
                _queue_cache_mtime = _queue_backup_file.stat().st_mtime
                return data
        except Exception as backup_err:
            logger.error(f"Errore caricamento backup coda job: {backup_err}")
        return _queue_cache if _queue_cache is not None else {}

def save_queue(jobs: Dict[str, Any]):
    """Salva la coda job nel file e invalida la cache."""
    global _queue_cache, _queue_cache_mtime
    try:
        tmp = QUEUE_FILE.with_suffix(".tmp")
        # Mantieni ultimo snapshot valido per recovery in caso di scrittura interrotta.
        if QUEUE_FILE.exists():
            try:
                shutil.copy2(QUEUE_FILE, _queue_backup_file)
            except Exception as backup_err:
                logger.warning(f"⚠️ Impossibile aggiornare backup coda: {backup_err}")
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(jobs, f, indent=2, ensure_ascii=False)
        tmp.replace(QUEUE_FILE)
        
        # Aggiorna la cache
        _queue_cache = jobs
        _queue_cache_mtime = QUEUE_FILE.stat().st_mtime
    except Exception as e:
        logger.error(f"Errore salvataggio coda job: {e}")

def load_poison_jobs() -> Dict[str, Any]:
    if not POISON_JOBS_FILE.exists():
        return {}
    try:
        with open(POISON_JOBS_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception as e:
        logger.warning(f"⚠️ Errore nel caricamento poison_jobs.json: {e}")
        return {}

def save_poison_jobs(poison_jobs: Dict[str, Any]) -> None:
    try:
        temp_file = POISON_JOBS_FILE.with_suffix(".tmp")
        with open(temp_file, "w", encoding="utf-8") as f:
            json.dump(poison_jobs, f, indent=2, ensure_ascii=False)
        temp_file.replace(POISON_JOBS_FILE)
    except Exception as e:
        logger.warning(f"⚠️ Errore nel salvataggio poison_jobs.json: {e}")

def log_job_event(job_id: str, event_type: str, extra: Optional[Dict[str, Any]] = None) -> None:
    try:
        event = {
            "timestamp": datetime.now(timezone.utc).isoformat() + "Z",
            "job_id": job_id,
            "event": event_type,
        }
        if extra and isinstance(extra, dict):
            event.update(extra)
        JOB_EVENTS_FILE.parent.mkdir(parents=True, exist_ok=True)
        with open(JOB_EVENTS_FILE, "a", encoding="utf-8") as f:
            json.dump(event, f, ensure_ascii=False)
            f.write("\n")
    except Exception as e:
        logger.debug(f"⚠️ Errore in log_job_event: {e}")

def load_projects() -> Dict[str, Dict[str, Any]]:
    if not PROJECTS_FILE.exists():
        return {}
    try:
        with open(PROJECTS_FILE, 'r', encoding='utf-8') as f:
            return json.load(f)
    except Exception as e:
        logger.error(f"Errore nel caricamento progetti: {e}")
        return {}

def save_projects(projects_data: Dict[str, Dict[str, Any]]):
    try:
        temp_file = PROJECTS_FILE.with_suffix('.tmp')
        with open(temp_file, 'w', encoding='utf-8') as f:
            json.dump(projects_data, f, indent=2, ensure_ascii=False)
        temp_file.replace(PROJECTS_FILE)
    except Exception as e:
        logger.error(f"Errore nel salvataggio progetti: {e}")

def _find_latest_completed_video_for_job(job_id: str) -> Optional[str]:
    """Helper per trovare video completato."""
    # This was a local helper in upload_retry_loop, moving here
    try:
        for f in Path(VIDEOS_DIR).glob(f"video_{job_id}_*.mp4"):
            return str(f)
        for f in Path(VIDEOS_DIR).glob(f"*{job_id}*.mp4"):
            return str(f)
    except Exception:
        pass
    return None

# Loops

def upload_retry_loop() -> None:
    logger.info("🔁 Upload retry loop avviato")
    while state.upload_retry_active.is_set():
        try:
            due = []
            if state.video_upload_storage:
                due = state.video_upload_storage.list_due_retries(limit=10)
        except Exception:
            due = []

        for upload_state in due:
            try:
                job_id = upload_state.job_id
                output_video_id = upload_state.output_video_id
                if not job_id or not output_video_id:
                    continue

                video_path = None
                try:
                    with state.queue_lock:
                        jobs = load_queue()
                        j = jobs.get(job_id) if isinstance(jobs, dict) else None
                    if isinstance(j, dict):
                        video_path = j.get("master_video_path")
                except Exception:
                    video_path = None

                if not video_path or not os.path.exists(video_path):
                    video_path = _find_latest_completed_video_for_job(job_id)

                if not video_path or not os.path.exists(video_path):
                    logger.warning(f"⚠️ Retry: video_path non trovato per job {job_id}, output_video_id={output_video_id}")
                    try:
                        if state.video_upload_storage:
                            state.video_upload_storage.mark_failed(
                                output_video_id=output_video_id,
                                error="Retry fallito: file video non trovato sul master",
                            )
                    except Exception:
                        pass
                    continue
                
                try:
                    from modules.video.upload.manager import process_upload
                    process_upload(
                        job_id=job_id,
                        video_path=video_path,
                        youtube_group=None,
                        video_name=None,
                        voiceover_channel_mapping=None,
                        audio_path=None,
                        output_video_id=output_video_id,
                        output_video_mapping=None,
                    )
                except ImportError:
                    logger.warning(f"🔁 Retry skipped: process_upload module not found for {job_id}")

            except Exception as e:
                logger.warning(f"⚠️ Retry loop error: {e}")

        for _ in range(int(UPLOAD_RETRY_CHECK_INTERVAL)):
            if not state.upload_retry_active.is_set():
                break
            time.sleep(1)

    logger.info("🛑 Upload retry loop terminato")


def upload_artifact_cleanup_loop() -> None:
    logger.info("🧹 Upload artifact cleanup loop avviato")
    while state.upload_artifact_cleanup_active.is_set():
        try:
            if state.video_upload_storage:
                try:
                    state.video_upload_storage.cleanup_old(
                        uploads_days=UPLOAD_DB_RETENTION_DAYS,
                        file_hash_days=FILE_HASH_RETENTION_DAYS,
                    )
                except Exception:
                    pass

            cutoff = time.time() - float(COMPLETED_VIDEOS_RETENTION_DAYS) * 24 * 60 * 60
            removed = 0
            try:
                for p in Path(VIDEOS_DIR).glob("*.mp4"):
                    try:
                        if not p.is_file():
                            continue
                        if p.stat().st_mtime <= cutoff:
                            p.unlink(missing_ok=True)
                            removed += 1
                    except Exception:
                        continue
            except Exception:
                pass
            if removed:
                logger.info(f"🧹 Cleanup completed_videos: rimossi {removed} file (> {COMPLETED_VIDEOS_RETENTION_DAYS} giorni)")
        except Exception as e:
            logger.warning(f"⚠️ Artifact cleanup error: {e}")

        for _ in range(int(UPLOAD_ARTIFACT_CLEANUP_INTERVAL // 10)):
            if not state.upload_artifact_cleanup_active.is_set():
                break
            time.sleep(10)

    logger.info("🛑 Upload artifact cleanup loop terminato")

def check_and_kill_zombie_jobs():
    def _parse_ts(val) -> float:
        if val is None:
            return 0.0
        if isinstance(val, (int, float)):
            return float(val)
        if isinstance(val, str):
            s = val.strip()
            if not s:
                return 0.0
            try:
                if s.endswith("Z"):
                    s = s[:-1] + "+00:00"
                return datetime.fromisoformat(s).timestamp()
            except Exception:
                return 0.0
        return 0.0

    while state.zombie_killer_active.is_set():
        try:
            state.zombie_killer_active.wait(JOB_ZOMBIE_CHECK_INTERVAL)
            if not state.zombie_killer_active.is_set():
                break

            reset_after_min = int(os.environ.get("VELOX_ZOMBIE_RESET_AFTER_MINUTES", "30") or "30")
            mark_error = str(os.environ.get("VELOX_ZOMBIE_MARK_ERROR", "1")).lower() in ("1", "true", "yes", "on")
            requeue = str(os.environ.get("VELOX_ZOMBIE_REQUEUE", "1")).lower() in ("1", "true", "yes", "on")
            reset_after_s = max(60, min(reset_after_min * 60, 24 * 3600))
            now_ts = time.time()

            reset_jobs: List[str] = []
            with state.queue_lock:
                jobs = load_queue()
                changed = False
                for job_id, job in jobs.items():
                    status = str(job.get("status") or "").upper()
                    if status != "PROCESSING":
                        continue
                    worker_id = job.get("assigned_to")
                    if not worker_id:
                        continue

                    last_hb_ts = 0.0
                    try:
                        if state.worker_lock and state.active_workers:
                            with state.worker_lock:
                                info = state.active_workers.get(worker_id) or {}
                                last_hb_ts = _parse_ts(info.get("last_heartbeat"))
                    except Exception:
                        last_hb_ts = 0.0

                    if last_hb_ts:
                        age = now_ts - last_hb_ts
                    else:
                        # Fallback to job timestamps to avoid immediate requeue when master restarted
                        assigned_ts = _parse_ts(job.get("assigned_at") or job.get("started_at") or job.get("processing_at"))
                        if not assigned_ts:
                            # No reliable timing info; skip this cycle
                            continue
                        age = now_ts - assigned_ts
                    if age >= reset_after_s:
                        msg = f"Zombie: no heartbeat for {int(age)}s (threshold {reset_after_s}s)"
                        if mark_error:
                            job["last_error"] = msg
                            job["last_error_at"] = now_ts
                            job.setdefault("history", []).append(
                                {
                                    "status": "ERROR",
                                    "timestamp": now_ts,
                                    "message": msg,
                                    "worker_id": worker_id,
                                }
                            )
                        if requeue:
                            job["status"] = "PENDING"
                            job["retry_count"] = int(job.get("retry_count") or 0) + 1
                            job.pop("assigned_to", None)
                            job.pop("assigned_at", None)
                            job.pop("leased_at", None)
                            job.pop("processing_at", None)
                            job.pop("claimed_by", None)
                            job.pop("claimed_at", None)
                            job.setdefault("history", []).append(
                                {
                                    "status": "PENDING",
                                    "timestamp": now_ts,
                                    "message": f"Requeued after zombie timeout",
                                }
                            )
                        else:
                            job["status"] = "ERROR"
                        jobs[job_id] = job
                        changed = True
                        reset_jobs.append(job_id)

                if changed:
                    save_queue(jobs)

            if reset_jobs:
                logger.warning(
                    f"🧟 Zombie reset: {len(reset_jobs)} job rimessi in PENDING "
                    f"(soglia {reset_after_min} min)"
                )
        except Exception as e:
            logger.error(f"❌ Errore zombie_killer: {e}", exc_info=True)
            time.sleep(10)

def _cleanup_old_jobs_internal(hours: int) -> Tuple[int, List[str]]:
    removed_ids = []
    # Allow override via days (completed-only) for clarity
    days_env = os.environ.get("VELOX_AUTO_CLEANUP_COMPLETED_DAYS")
    only_completed = str(os.environ.get("VELOX_AUTO_CLEANUP_ONLY_COMPLETED", "1")).lower() in ("1", "true", "yes", "on")
    if days_env:
        try:
            cutoff_time = time.time() - (float(days_env) * 24 * 3600)
        except Exception:
            cutoff_time = time.time() - (hours * 3600)
    else:
        cutoff_time = time.time() - (hours * 3600)
    
    with state.queue_lock:
        jobs = load_queue()
        if not jobs:
            return 0, []
        
        for job_id, job_data in list(jobs.items()):
            status = job_data.get("status", "")
            created_at = job_data.get("created_at", 0)
            
            if isinstance(created_at, str):
                try:
                    dt = datetime.fromisoformat(created_at.replace('Z', '+00:00'))
                    created_at = dt.timestamp()
                except Exception:
                    created_at = 0
            
            if (status == "COMPLETED") or (not only_completed and status in ("ERROR", "FAILED")):
                if created_at < cutoff_time:
                    removed_ids.append(job_id)
                    del jobs[job_id]
        
        if removed_ids:
            save_queue(jobs)
    
    return len(removed_ids), removed_ids

def auto_cleanup_jobs():
    while state.auto_cleanup_active.is_set():
        try:
            state.auto_cleanup_active.wait(AUTO_CLEANUP_JOBS_INTERVAL)
            if not state.auto_cleanup_active.is_set():
                break
            
            removed_count, _ = _cleanup_old_jobs_internal(AUTO_CLEANUP_OLD_HOURS)
            
            if removed_count > 0:
                logger.info(
                    f"🧹 Auto-cleanup: rimossi {removed_count} job "
                    f"(più vecchi di {AUTO_CLEANUP_OLD_HOURS}h)"
                )
        except Exception as e:
            logger.error(f"❌ Errore durante auto-cleanup job: {e}", exc_info=True)
            time.sleep(60)

def watch_completed_videos_folder():
    pass



async def update_job_logs(payload: Dict[str, Any]):
    """Endpoint chiamato dai worker per appendere log nel job (usato dalla dashboard /job/{id})."""
    job_id = str(payload.get("job_id") or "").strip()
    if not job_id:
        raise HTTPException(status_code=400, detail="job_id è obbligatorio")

    worker_id = payload.get("worker_id")
    raw_logs = payload.get("logs")
    if raw_logs is None:
        raw_logs = []
    if not isinstance(raw_logs, list):
        raise HTTPException(status_code=400, detail="logs deve essere una lista")

    now_iso = datetime.now(timezone.utc).isoformat() + "Z"
    normalized: List[Dict[str, Any]] = []
    for item in raw_logs[:5000]:
        if isinstance(item, dict):
            entry = dict(item)
            if "timestamp" not in entry and "time" not in entry:
                entry["timestamp"] = now_iso
            if "is_error" not in entry:
                lvl = str(entry.get("level") or "").lower()
                entry["is_error"] = lvl in ("error", "critical")
            if worker_id and "worker_id" not in entry:
                entry["worker_id"] = worker_id
            normalized.append(entry)
        else:
            normalized.append({"timestamp": now_iso, "message": str(item), "is_error": False, "worker_id": worker_id})

    with state.queue_lock:
        jobs = load_queue()
        if job_id not in jobs:
            raise HTTPException(status_code=404, detail=f"Job {job_id} non trovato")
        job = jobs.get(job_id) or {}
        existing = job.get("logs")
        if not isinstance(existing, list):
            existing = []
        existing.extend(normalized)
        try:
            max_logs = int(os.environ.get("VELOX_MAX_JOB_LOGS", "20000"))
        except Exception:
            max_logs = 20000
        if max_logs > 0 and len(existing) > max_logs:
            existing = existing[-max_logs:]
        job["logs"] = existing
        job["logs_updated_at"] = now_iso
        jobs[job_id] = job
        save_queue(jobs)

    try:
        if worker_id and normalized:
            workers.worker_log_append(worker_id, normalized)
    except Exception:
        pass
    try:
        if logger:
            logger.debug(f"🧾 update_job_logs: job={job_id[:8]} +{len(normalized)} (tot={len(existing)})")
    except Exception:
        pass

    return {"status": "ok", "job_id": job_id, "added": len(normalized), "total": len(existing)}
