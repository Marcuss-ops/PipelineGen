
from typing import Dict, List, Set, Any, Optional
from collections import defaultdict, deque
from threading import Lock
import threading
from .config import (
    WORKER_JOB_STATS_WINDOW,
    MAX_WORKER_LOGS_PER_WORKER,
    MAX_WORKER_REPORTED_ERRORS,
    MAX_ANSIBLE_LOGS_PER_COMPUTER
)

# Locks
queue_lock = Lock()
worker_lock = Lock()
registration_lock = Lock()
revoked_workers_lock = Lock()
quarantined_workers_lock = Lock()
worker_tokens_lock = Lock()
worker_job_stats_lock = Lock()
zip_creation_lock = Lock()
upload_lock = Lock()
uploaded_videos_lock = Lock()
worker_logs_lock = Lock()
worker_reported_errors_lock = Lock()
ansible_computers_lock = Lock()
ansible_computer_logs_lock = Lock()
ansible_runs_lock = Lock()
projects_lock = Lock()
_youtube_upload_log_lock = Lock()
_API_REQUESTS_LOG_LOCK = Lock()
api_requests_log_lock = Lock()
# Ultime N richieste API per dashboard monitor (path, status_code, duration_ms, timestamp_iso, error_type)
api_requests_log: List[Dict[str, Any]] = []
API_REQUESTS_LOG_MAX = 500

# Stato
active_workers: Dict[str, Dict[str, Any]] = {}
last_workers_save_time = 0
_heartbeat_logged_workers: Set[str] = set()

worker_job_stats: Dict[str, Any] = defaultdict(lambda: deque(maxlen=WORKER_JOB_STATS_WINDOW))

revoked_workers: set = set()
quarantined_workers: set = set()
worker_tokens: Dict[str, Dict[str, Any]] = {}

worker_commands: Dict[str, List[Any]] = {}
pending_updates: Dict[str, Dict[str, Any]] = {}
pending_uploads: Dict[str, Dict[str, Any]] = {}
uploaded_videos: Dict[str, Dict[str, Any]] = {}

worker_logs: Dict[str, List[Dict[str, Any]]] = defaultdict(list)
worker_reported_errors: Dict[str, Any] = defaultdict(lambda: deque(maxlen=MAX_WORKER_REPORTED_ERRORS))

ansible_computers: Dict[str, Dict[str, Any]] = {}
ansible_computer_logs: Dict[str, List[Dict[str, Any]]] = defaultdict(list)
ansible_runs: Dict[str, Dict[str, Any]] = {}

projects: Dict[str, Dict[str, Any]] = {}

# Caching per versionamento codice
cached_code_version: Optional[str] = None
cached_zip_mtime: float = 0

worker_fail_history: Dict[str, list] = {}
processed_videos: set = set()

# Flag Runtime
REGISTRATION_DISABLED = False
NEW_JOBS_PAUSED = False
NEW_JOBS_PAUSED_REASON: Optional[str] = None
SCHEDULING_PAUSED = False
SCHEDULING_PAUSED_REASON: Optional[str] = None

# Thread Events
zombie_killer_active = threading.Event()
auto_cleanup_active = threading.Event()
upload_retry_active = threading.Event()
upload_artifact_cleanup_active = threading.Event()
video_watcher_active = threading.Event()

# Thread pointers (optional, for explicit join if needed)
zombie_killer_thread: Optional[threading.Thread] = None
auto_cleanup_thread: Optional[threading.Thread] = None
upload_retry_thread: Optional[threading.Thread] = None
upload_artifact_cleanup_thread: Optional[threading.Thread] = None
video_watcher_thread: Optional[threading.Thread] = None

# Objects populated at startup
machine_inventory = None
machine_monitoring = None
machine_alerting = None
machine_provisioning = None
machine_runbooks = None
machine_storage = None
video_upload_storage = None
