from __future__ import annotations

import os
import shlex
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

DEFAULT_KEYWORDS = [
    "business team modern office", "city skyline aerial drone",
    "factory automation robots", "doctor hospital technology",
    "solar panels renewable energy", "boxing training gym",
    "basketball practice court", "family cooking kitchen",
    "cybersecurity server room", "financial trading screens",
    "construction workers building", "electric car charging",
    "scientist laboratory research", "airport travelers terminal",
    "farmer tractor field", "ocean waves coastline",
    "students classroom learning", "warehouse logistics packages",
    "coffee shop barista", "mountain hiking adventure",
]
DIAGNOSTIC_PROBES = (
    "scraper", "browser", "session", "downloader", "ffmpeg_binary",
    "drive_folder", "sqlite_writable", "outbox_dispatcher",
    "qdrant_reachable", "embedding_provider",
)
TERMINAL_JOB_STATUSES = {"SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED"}
SUCCESS_JOB_STATUSES = {"SUCCEEDED", "COMPLETED"}


def env_int(name: str, default: int, *, minimum: int = 0, maximum: int | None = None) -> int:
    raw = os.getenv(name, str(default)).strip()
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer, got {raw!r}") from exc
    if value < minimum:
        raise ValueError(f"{name} must be >= {minimum}, got {value}")
    if maximum is not None and value > maximum:
        raise ValueError(f"{name} must be <= {maximum}, got {value}")
    return value


def env_float(name: str, default: float, *, minimum: float = 0.0) -> float:
    raw = os.getenv(name, str(default)).strip()
    try:
        value = float(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be numeric, got {raw!r}") from exc
    if value < minimum:
        raise ValueError(f"{name} must be >= {minimum}, got {value}")
    return value


def env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() not in {"0", "false", "no", "off", ""}


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def chunks(values: list[str], size: int) -> Iterable[list[str]]:
    for index in range(0, len(values), size):
        yield values[index:index + size]


@dataclass(frozen=True)
class Settings:
    repo_root: Path
    base_url: str
    scraper_url: str
    qdrant_url: str
    qdrant_api_key: str
    qdrant_collection: str
    db_path: Path
    admin_token: str
    root_folder_id: str
    keywords: list[str]
    clips_per_keyword: int
    clip_concurrency: int
    poll_interval: int
    poll_max: int
    http_timeout: int
    health_interval: int
    poll_workers: int
    run_replay: bool
    run_vlm: bool
    run_qdrant_reindex: bool
    require_m3u8: bool
    require_no_redownload: bool
    min_assets_per_minute: float
    vlm_interval: int
    vlm_timeout: int
    report_dir: Path
    admin_command: list[str]

    @classmethod
    def load(cls) -> "Settings":
        repo_root = Path(__file__).resolve().parents[2]
        host = os.getenv("VELOX_HOST", "127.0.0.1")
        port = os.getenv("PIPELINE_PORT", os.getenv("VELOX_PORT", "8000"))
        base_url = os.getenv("BASE_URL", f"http://{host}:{port}").rstrip("/")
        scraper_url = os.getenv("VELOX_ARTLIST_SCRAPER_SERVER_URL", "http://127.0.0.1:9123").rstrip("/")
        qdrant_url = os.getenv("QDRANT_URL", "http://127.0.0.1:6333").rstrip("/")
        data_dir = Path(os.getenv("VELOX_DATA_DIR", "./data"))
        if not data_dir.is_absolute():
            data_dir = repo_root / data_dir
        keywords_file = os.getenv("ARTLIST_SCALE_KEYWORDS_FILE", "").strip()
        keywords_csv = os.getenv("ARTLIST_SCALE_KEYWORDS", "").strip()
        if keywords_file:
            lines = Path(keywords_file).read_text(encoding="utf-8").splitlines()
            keywords = [line.strip() for line in lines if line.strip() and not line.lstrip().startswith("#")]
        elif keywords_csv:
            keywords = [value.strip() for value in keywords_csv.split(",") if value.strip()]
        else:
            keywords = list(DEFAULT_KEYWORDS)
        if not keywords:
            raise ValueError("no Artlist scale keywords configured")
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        admin_bin = os.getenv("ARTLIST_SCALE_ADMIN_BIN", "").strip()
        return cls(
            repo_root=repo_root,
            base_url=base_url,
            scraper_url=scraper_url,
            qdrant_url=qdrant_url,
            qdrant_api_key=os.getenv("QDRANT_API_KEY", os.getenv("VELOX_QDRANT_API_KEY", "")),
            qdrant_collection=os.getenv("QDRANT_COLLECTION", "media_assets_current"),
            db_path=data_dir / "media" / "media.db.sqlite",
            admin_token=os.getenv("VELOX_ADMIN_TOKEN", "").strip(),
            root_folder_id=os.getenv("ROOT_FOLDER_ID", os.getenv("VELOX_DRIVE_ARTLIST_ROOT", "")).strip(),
            keywords=keywords,
            clips_per_keyword=env_int("ARTLIST_SCALE_CLIPS_PER_KEYWORD", 10, minimum=1),
            clip_concurrency=env_int("ARTLIST_SCALE_CLIP_CONCURRENCY", 10, minimum=1, maximum=10),
            poll_interval=env_int("ARTLIST_SCALE_POLL_INTERVAL", 10, minimum=1),
            poll_max=env_int("ARTLIST_SCALE_POLL_MAX", 360, minimum=1),
            http_timeout=env_int("ARTLIST_SCALE_HTTP_TIMEOUT", 300, minimum=1),
            health_interval=env_int("ARTLIST_SCALE_HEALTH_INTERVAL", 15, minimum=1),
            poll_workers=env_int("ARTLIST_SCALE_POLL_WORKERS", 20, minimum=1, maximum=100),
            run_replay=env_bool("ARTLIST_SCALE_RUN_REPLAY", True),
            run_vlm=env_bool("ARTLIST_SCALE_RUN_VLM", True),
            run_qdrant_reindex=env_bool("ARTLIST_SCALE_RUN_QDRANT_REINDEX", True),
            require_m3u8=env_bool("ARTLIST_SCALE_REQUIRE_M3U8_PERSISTENCE", True),
            require_no_redownload=env_bool("ARTLIST_SCALE_REQUIRE_NO_REDOWNLOAD", True),
            min_assets_per_minute=env_float("ARTLIST_SCALE_MIN_ASSETS_PER_MINUTE", 0.0),
            vlm_interval=env_int("ARTLIST_SCALE_VLM_INTERVAL", 7, minimum=1),
            vlm_timeout=env_int("ARTLIST_SCALE_VLM_TIMEOUT", 120, minimum=1),
            report_dir=Path(os.getenv("ARTLIST_SCALE_REPORT_DIR", f"/tmp/artlist_scale_{stamp}")),
            admin_command=shlex.split(admin_bin) if admin_bin else ["go", "run", "./cmd/admin"],
        )
