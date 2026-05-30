import logging
from datetime import datetime
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.cron import CronTrigger
from config import SCHEDULE_CRON
from drive_client import list_vids_projects

log = logging.getLogger(__name__)
scheduler = AsyncIOScheduler()


async def _sync_all():
    from playwright_client import sync_project

    log.info("Scheduled sync started at %s", datetime.now().isoformat())

    try:
        projects = list_vids_projects()
    except Exception as e:
        log.error("Failed to list Vids projects from Drive: %s", e)
        return

    downloaded = 0
    for project in projects:
        try:
            paths = await sync_project(project["id"], file_type="all")
            log.info("Downloaded %d file(s) for %s", len(paths), project.get("name"))
            downloaded += len(paths)
        except Exception as e:
            log.error("Error syncing project %s: %s", project.get("name"), e)

    log.info("Global scheduled sync complete. Downloaded %d files.", downloaded)


async def _keep_alive_job():
    from keep_alive import run_keep_alive
    log.info("Running scheduled keep-alive task...")
    try:
        success = await run_keep_alive(account="favamassimo", headless=True)
        log.info("Scheduled keep-alive run completed. Success: %s", success)
    except Exception as e:
        log.error("Failed running scheduled keep-alive: %s", e)


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
    
    # Run keep-alive every 4 hours to prevent session expiry
    from apscheduler.triggers.interval import IntervalTrigger
    scheduler.add_job(_keep_alive_job, IntervalTrigger(hours=4), id="keep_alive", replace_existing=True)
    
    scheduler.start()
    log.info("Scheduler started with cron: %s", cron)


def stop():
    scheduler.shutdown(wait=False)
