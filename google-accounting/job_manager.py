import time
import asyncio
import logging
import httpx
from collections import OrderedDict

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Job Store — in-memory with TTL and max size
# ---------------------------------------------------------------------------
MAX_JOBS = 1000
JOB_TTL_SECONDS = 24 * 3600  # 24 hours

_jobs: OrderedDict[str, dict] = OrderedDict()

def _now_ts() -> float:
    return time.time()

def get_job(job_id: str) -> dict | None:
    return _jobs.get(job_id)

def get_all_jobs() -> dict:
    return _jobs

def delete_job(job_id: str) -> bool:
    if job_id in _jobs:
        del _jobs[job_id]
        return True
    return False

def new_job(job_id: str, **fields):
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

def update_job(job_id: str, **fields):
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

async def cleanup_expired_jobs():
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
            if jid in _jobs:
                del _jobs[jid]
        if expired_ids:
            log.info("Cleaned up %d expired jobs", len(expired_ids))
