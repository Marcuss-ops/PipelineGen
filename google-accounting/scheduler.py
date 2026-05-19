import logging
import os
from datetime import datetime
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.cron import CronTrigger
from config import SCHEDULE_CRON, SESSIONS_DIR

log = logging.getLogger(__name__)
scheduler = AsyncIOScheduler()


async def _sync_all():
    from playwright_client import list_projects, download_video

    log.info("Scheduled sync started at %s", datetime.now().isoformat())
    
    if not SESSIONS_DIR.exists():
        log.warning("No sessions directory found. Skipping sync.")
        return

    session_files = list(SESSIONS_DIR.glob("*.json"))
    if not session_files:
        log.warning("No session files found. Skipping sync.")
        return

    for session_file in session_files:
        account = session_file.stem
        log.info("Syncing account: %s", account)
        
        try:
            projects = await list_projects(account=account)
            downloaded = 0

            for project in projects:
                try:
                    path = await download_video(project["id"], account=account)
                    log.info("[%s] Downloaded: %s -> %s", account, project.get("name"), path)
                    downloaded += 1
                except Exception as e:
                    log.error("[%s] Error syncing project %s: %s", account, project.get("name"), e)

            log.info("[%s] Sync complete. Downloaded %d files.", account, downloaded)
        except Exception as e:
            log.error("Failed to sync account %s: %s", account, e)

    log.info("Global scheduled sync complete.")


def start(custom_cron: str | None = None):
    cron = custom_cron or SCHEDULE_CRON
    parts = cron.split()
    trigger = CronTrigger(
        minute=parts[0],
        hour=parts[1],
        day=parts[2],
        month=parts[3],
        day_of_week=parts[4],
    )
    scheduler.add_job(_sync_all, trigger, id="sync_all", replace_existing=True)
    scheduler.start()
    log.info("Scheduler started with cron: %s", cron)


def stop():
    scheduler.shutdown(wait=False)
