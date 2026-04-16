
import os
from pathlib import Path
from threading import Lock

# Percorsi base
# SCRIPT_DIR verrà inizializzato/configurato, ma per ora usiamo un path relativo o environment
# In job_master_server.py era: Path(__file__).parent.absolute()
# Qui siamo in modules/core, quindi job_master_server è due livelli su.
# Ma attenzione: job_master_server.py viene eseguito dalla root?
# Manteniamo la logica che SCRIPT_DIR è la directory dove risiede job_master_server.py
# Se questo file è in /modules/core/, allora SCRIPT_DIR è ../../
SCRIPT_DIR = Path(__file__).resolve().parent.parent.parent

# File di configurazione e dati
QUEUE_FILE = SCRIPT_DIR / "data" / "job_queue.json"
WORKERS_FILE = SCRIPT_DIR / "data" / "workers.json"
PENDING_UPDATES_FILE = SCRIPT_DIR / "data" / "pending_updates.json"
REVOKED_WORKERS_FILE = SCRIPT_DIR / "data" / "revoked_workers.json"
POISON_JOBS_FILE = SCRIPT_DIR / "data" / "poison_jobs.json"
MASTER_CONFIG_FILE = SCRIPT_DIR / "data" / "master_config.json"
QUARANTINED_WORKERS_FILE = SCRIPT_DIR / "data" / "quarantined_workers.json"
JOB_EVENTS_FILE = SCRIPT_DIR / "data" / "job_events.jsonl"
YOUTUBE_UPLOAD_LOG_FILE = SCRIPT_DIR / "data" / "youtube_upload_log.jsonl"

MACHINE_INVENTORY_FILE = SCRIPT_DIR / "data" / "machine_inventory.json"
RUNBOOKS_FILE = SCRIPT_DIR / "data" / "runbooks.json"
ALERTING_CONFIG_FILE = SCRIPT_DIR / "data" / "alerting_config.json"

ANSIBLE_COMPUTERS_FILE = (SCRIPT_DIR / "config" / "ansible_computers.json") if (SCRIPT_DIR / "config" / "ansible_computers.json").exists() else (SCRIPT_DIR / "data" / "ansible_computers.json")
ANSIBLE_RUNS_FILE = (SCRIPT_DIR / "config" / "ansible_runs.json") if (SCRIPT_DIR / "config" / "ansible_runs.json").exists() else (SCRIPT_DIR / "data" / "ansible_runs.json")
ANSIBLE_COMMAND_HISTORY_FILE = (SCRIPT_DIR / "config" / "ansible_command_history.json") if (SCRIPT_DIR / "config").exists() else (SCRIPT_DIR / "data" / "ansible_command_history.json")

VOICEOVER_STORAGE_DIR = Path(os.environ.get("VELOX_VOICEOVER_STORAGE", str(SCRIPT_DIR / "data" / "voiceovers")))
VIDEOS_DIR = SCRIPT_DIR / "completed_videos"
PROJECTS_FILE = SCRIPT_DIR / "data" / "projects.json"

_VIDEO_UPLOAD_DB_PATH = str((SCRIPT_DIR / "data" / "video_uploads.db"))

API_REQUESTS_LOG_PATH = (SCRIPT_DIR / "data" / "api_requests.jsonl")
API_REQUESTS_ARCHIVE_DIR = (SCRIPT_DIR / "data" / "api_requests_archive")

THUMBNAILS_UPLOAD_DIR = (SCRIPT_DIR / "data" / "uploads" / "thumbnails")

# Costanti
DEFAULT_PIPELINE_VERSION = "video_v1.0"
DEFAULT_PRESET_VERSION = "preset_v1.0"
DEFAULT_ASSET_VERSION = "assets_v1.0"

WORKERS_SAVE_INTERVAL = 30
WORKER_JOB_STATS_WINDOW = 20
AUTO_REPAIR_COOLDOWN_SECONDS = 15 * 60

MAX_WORKER_LOGS_PER_WORKER = 2000
MAX_WORKER_REPORTED_ERRORS = 50
MAX_ANSIBLE_LOGS_PER_COMPUTER = 1000

JOB_LEASE_TTL_SECONDS = 80 * 60
JOB_ZOMBIE_CHECK_INTERVAL = 5 * 60

AUTO_CLEANUP_JOBS_INTERVAL = 60 * 60
AUTO_CLEANUP_OLD_HOURS = 24

UPLOAD_RETRY_CHECK_INTERVAL = 30
UPLOAD_ARTIFACT_CLEANUP_INTERVAL = 6 * 60 * 60
COMPLETED_VIDEOS_RETENTION_DAYS = 30
UPLOAD_DB_RETENTION_DAYS = 90
FILE_HASH_RETENTION_DAYS = 180

MAX_PARALLEL_PER_PROJECT = 5
MAX_JOB_RETRIES = 3

WORKER_FAIL_WINDOW_SECONDS = 15 * 60
WORKER_FAIL_THRESHOLD = 5

API_REQUESTS_ARCHIVE_KEEP = int(os.environ.get("API_REQUESTS_ARCHIVE_KEEP", "60"))
API_REQUESTS_ROTATE_INTERVAL_SEC = int(os.environ.get("API_REQUESTS_ROTATE_INTERVAL_SEC", str(2 * 60 * 60)))

API_THUMBNAIL_TTL_DAYS = int(os.environ.get("API_THUMBNAIL_TTL_DAYS", "7"))
API_THUMBNAIL_CLEAN_INTERVAL_SEC = int(os.environ.get("API_THUMBNAIL_CLEAN_INTERVAL_SEC", str(6 * 60 * 60)))

# Worker allowlist (IP/hostname). Comma-separated override via VELOX_ALLOWED_WORKERS.
# Empty by default: all workers can connect. Set VELOX_ALLOWED_WORKERS to restrict.
_allowed_env = os.environ.get("VELOX_ALLOWED_WORKERS", "")
ALLOWED_WORKER_IPS = [s.strip() for s in _allowed_env.split(",") if s.strip()]

import json
import shutil
import logging

logger = logging.getLogger(__name__)

def load_master_config():
    """Carica la configurazione master (best-effort)."""
    if not MASTER_CONFIG_FILE.exists():
        return {}
    try:
        with open(MASTER_CONFIG_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception as e:
        logger.warning(f"⚠️ Errore nel caricamento master_config.json: {e}")
        return {}


def save_master_config(config):
    """Salva la configurazione master (scrittura atomica)."""
    try:
        temp_file = MASTER_CONFIG_FILE.with_suffix(".tmp")
        with open(temp_file, "w", encoding="utf-8") as f:
            json.dump(config, f, indent=2, ensure_ascii=False)
        try:
            temp_file.replace(MASTER_CONFIG_FILE)
        except OSError:
            shutil.move(str(temp_file), str(MASTER_CONFIG_FILE))
    except Exception as e:
        logger.warning(f"⚠️ Errore nel salvataggio master_config.json: {e}")
