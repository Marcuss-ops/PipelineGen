"""
Video Upload Storage - SQLite persistence layer.

Stores immutable job specs for uploads and idempotent upload state.
"""

from __future__ import annotations

import json
import sqlite3
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone, timedelta
from pathlib import Path
from typing import Any, Dict, Optional, Tuple


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


@dataclass(frozen=True)
class JobSpec:
    job_id: str
    output_video_id: Optional[str]
    youtube_group: Optional[str]
    channel_id: Optional[str]
    youtube_title: Optional[str]
    video_filename: Optional[str]
    spec_version: int
    spec_json: Dict[str, Any]


@dataclass(frozen=True)
class UploadState:
    output_video_id: str
    status: str
    job_id: Optional[str]
    channel_id: Optional[str]
    youtube_title: Optional[str]
    youtube_video_id: Optional[str]
    file_sha256: Optional[str]
    retry_count: int
    next_retry_at: Optional[str]
    started_at: Optional[str]
    completed_at: Optional[str]
    error: Optional[str]
    thumbnail_set: int = 0
    thumbnail_url: Optional[str] = None
    thumbnail_updated_at: Optional[str] = None


class VideoUploadStorage:
    """SQLite storage for immutable job specs and upload idempotency."""

    def __init__(self, db_path: str):
        self.db_path = Path(db_path)
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._init_database()

    @contextmanager
    def _get_connection(self):
        conn = sqlite3.connect(str(self.db_path), timeout=30)
        conn.row_factory = sqlite3.Row
        try:
            yield conn
            conn.commit()
        except Exception:
            conn.rollback()
            raise
        finally:
            conn.close()

    def _init_database(self) -> None:
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS video_job_specs (
                    job_id TEXT PRIMARY KEY,
                    output_video_id TEXT,
                    youtube_group TEXT,
                    channel_id TEXT,
                    youtube_title TEXT,
                    video_filename TEXT,
                    spec_version INTEGER NOT NULL DEFAULT 1,
                    spec_json TEXT NOT NULL,
                    created_at TEXT,
                    updated_at TEXT
                )
                """
            )
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS video_uploads (
                    output_video_id TEXT PRIMARY KEY,
                    status TEXT NOT NULL,
                    job_id TEXT,
                    channel_id TEXT,
                    youtube_title TEXT,
                    youtube_video_id TEXT,
                    file_sha256 TEXT,
                    retry_count INTEGER DEFAULT 0,
                    next_retry_at TEXT,
                    started_at TEXT,
                    completed_at TEXT,
                    error TEXT
                )
                """
            )
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS uploaded_file_hashes (
                    sha256 TEXT NOT NULL,
                    channel_id TEXT NOT NULL,
                    youtube_video_id TEXT NOT NULL,
                    uploaded_at TEXT,
                    PRIMARY KEY (sha256, channel_id)
                )
                """
            )

            # Cache of what's currently in each channel (best-effort snapshot from YouTube API calls)
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS youtube_channel_videos_cache (
                    channel_id TEXT NOT NULL,
                    youtube_video_id TEXT NOT NULL,
                    title TEXT,
                    privacy_status TEXT,
                    published_at TEXT,
                    thumbnail_url TEXT,
                    youtube_url TEXT,
                    last_seen_at TEXT,
                    fetched_at TEXT,
                    PRIMARY KEY (channel_id, youtube_video_id)
                )
                """
            )

            # Best-effort migrations for existing DBs
            def _ensure_cols(table: str, cols: Dict[str, str]) -> None:
                cur.execute(f"PRAGMA table_info({table})")
                existing = {r[1] for r in cur.fetchall() if r and len(r) > 1}
                for name, ddl in cols.items():
                    if name in existing:
                        continue
                    cur.execute(f"ALTER TABLE {table} ADD COLUMN {ddl}")

            _ensure_cols(
                "video_uploads",
                {
                    "file_sha256": "file_sha256 TEXT",
                    "retry_count": "retry_count INTEGER DEFAULT 0",
                    "next_retry_at": "next_retry_at TEXT",
                    "thumbnail_set": "thumbnail_set INTEGER DEFAULT 0",
                    "thumbnail_url": "thumbnail_url TEXT",
                    "thumbnail_updated_at": "thumbnail_updated_at TEXT",
                },
            )

            # Indexes (after migrations, so columns exist even on old DBs)
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_video_uploads_status ON video_uploads(status)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_video_uploads_next_retry_at ON video_uploads(next_retry_at)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_video_job_specs_output_id ON video_job_specs(output_video_id)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_video_job_specs_youtube_group ON video_job_specs(youtube_group)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_video_uploads_youtube_video_id ON video_uploads(youtube_video_id)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_yt_cache_channel ON youtube_channel_videos_cache(channel_id)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_yt_cache_privacy ON youtube_channel_videos_cache(privacy_status)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_yt_cache_seen ON youtube_channel_videos_cache(last_seen_at)"
            )

    def upsert_job_spec(self, job_id: str, spec: Dict[str, Any]) -> JobSpec:
        now = _utc_now_iso()
        output_video_id = spec.get("output_video_id")
        youtube_group = spec.get("youtube_group")

        channel_id = None
        youtube_title = None
        video_filename = None

        ovm = spec.get("output_video_mapping") or {}
        if output_video_id and isinstance(ovm, dict):
            entry = ovm.get(output_video_id)
            if isinstance(entry, dict):
                channel_id = entry.get("channel_id")
                youtube_title = entry.get("youtube_title")
                video_filename = entry.get("video_filename")

        spec_json = dict(spec)
        payload = json.dumps(spec_json, ensure_ascii=False)

        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute("SELECT spec_version FROM video_job_specs WHERE job_id = ?", (job_id,))
            row = cur.fetchone()
            prev_version = int(row["spec_version"]) if row and row.get("spec_version") is not None else 0
            new_version = prev_version + 1 if prev_version else 1

            cur.execute(
                """
                INSERT INTO video_job_specs (
                    job_id, output_video_id, youtube_group, channel_id, youtube_title, video_filename,
                    spec_version, spec_json, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(job_id) DO UPDATE SET
                    output_video_id=excluded.output_video_id,
                    youtube_group=excluded.youtube_group,
                    channel_id=excluded.channel_id,
                    youtube_title=excluded.youtube_title,
                    video_filename=excluded.video_filename,
                    spec_version=excluded.spec_version,
                    spec_json=excluded.spec_json,
                    updated_at=excluded.updated_at
                """,
                (
                    job_id,
                    output_video_id,
                    youtube_group,
                    channel_id,
                    youtube_title,
                    video_filename,
                    new_version,
                    payload,
                    now,
                    now,
                ),
            )

        return JobSpec(
            job_id=job_id,
            output_video_id=output_video_id,
            youtube_group=youtube_group,
            channel_id=channel_id,
            youtube_title=youtube_title,
            video_filename=video_filename,
            spec_version=new_version,
            spec_json=spec_json,
        )

    def get_job_spec(self, job_id: str) -> Optional[JobSpec]:
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute("SELECT * FROM video_job_specs WHERE job_id = ?", (job_id,))
            row = cur.fetchone()
            if not row:
                return None
            raw = dict(row)
            spec_json = {}
            try:
                spec_json = json.loads(raw.get("spec_json") or "{}")
            except Exception:
                spec_json = {}
            return JobSpec(
                job_id=raw.get("job_id") or job_id,
                output_video_id=raw.get("output_video_id"),
                youtube_group=raw.get("youtube_group"),
                channel_id=raw.get("channel_id"),
                youtube_title=raw.get("youtube_title"),
                video_filename=raw.get("video_filename"),
                spec_version=int(raw.get("spec_version") or 1),
                spec_json=spec_json,
            )

    def get_upload_state(self, output_video_id: str) -> Optional[UploadState]:
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute("SELECT * FROM video_uploads WHERE output_video_id = ?", (output_video_id,))
            row = cur.fetchone()
            if not row:
                return None
            raw = dict(row)
            return UploadState(
                output_video_id=raw.get("output_video_id") or output_video_id,
                status=raw.get("status") or "UNKNOWN",
                job_id=raw.get("job_id"),
                channel_id=raw.get("channel_id"),
                youtube_title=raw.get("youtube_title"),
                youtube_video_id=raw.get("youtube_video_id"),
                file_sha256=raw.get("file_sha256"),
                retry_count=int(raw.get("retry_count") or 0),
                next_retry_at=raw.get("next_retry_at"),
                started_at=raw.get("started_at"),
                completed_at=raw.get("completed_at"),
                error=raw.get("error"),
                thumbnail_set=int(raw.get("thumbnail_set") or 0),
                thumbnail_url=raw.get("thumbnail_url"),
                thumbnail_updated_at=raw.get("thumbnail_updated_at"),
            )

    def try_begin_upload(
        self,
        *,
        output_video_id: str,
        job_id: Optional[str],
        channel_id: Optional[str],
        youtube_title: Optional[str],
    ) -> Tuple[bool, UploadState]:
        """
        Atomically marks an upload as UPLOADING.
        Returns (started, state). If started is False, another upload is already in progress
        or the upload is blocked (e.g. NEEDS_INPUT).
        """
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT OR IGNORE INTO video_uploads (
                    output_video_id, status, job_id, channel_id, youtube_title, started_at, retry_count
                ) VALUES (?, 'UPLOADING', ?, ?, ?, ?, 0)
                """,
                (output_video_id, job_id, channel_id, youtube_title, now),
            )
            started = cur.rowcount == 1
            if not started:
                cur.execute("SELECT status FROM video_uploads WHERE output_video_id=?", (output_video_id,))
                row = cur.fetchone()
                status = (row["status"] if row else None) or "UNKNOWN"
                # Allow retries / re-uploads after failure
                if status in {"RETRY", "FAILED"}:
                    cur.execute(
                        """
                        UPDATE video_uploads
                        SET status='UPLOADING', job_id=?, channel_id=?, youtube_title=?, started_at=?, next_retry_at=NULL, error=NULL
                        WHERE output_video_id=?
                        """,
                        (job_id, channel_id, youtube_title, now, output_video_id),
                    )
                    started = cur.rowcount == 1
        state = self.get_upload_state(output_video_id) or UploadState(
            output_video_id=output_video_id,
            status="UPLOADING" if started else "UNKNOWN",
            job_id=job_id,
            channel_id=channel_id,
            youtube_title=youtube_title,
            youtube_video_id=None,
            file_sha256=None,
            retry_count=0,
            next_retry_at=None,
            started_at=now if started else None,
            completed_at=None,
            error=None,
        )
        return started, state

    def mark_uploaded(
        self,
        *,
        output_video_id: str,
        youtube_video_id: str,
        channel_id: Optional[str],
        youtube_title: Optional[str],
        file_sha256: Optional[str] = None,
    ) -> None:
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE video_uploads
                SET status='UPLOADED', youtube_video_id=?, channel_id=?, youtube_title=?, file_sha256=?, completed_at=?, error=NULL, next_retry_at=NULL
                WHERE output_video_id=?
                """,
                (youtube_video_id, channel_id, youtube_title, file_sha256, now, output_video_id),
            )

        # Also store file hash mapping for dedup (best effort)
        if file_sha256 and channel_id and youtube_video_id:
            try:
                with self._get_connection() as conn:
                    cur = conn.cursor()
                    cur.execute(
                        """
                        INSERT OR IGNORE INTO uploaded_file_hashes (sha256, channel_id, youtube_video_id, uploaded_at)
                        VALUES (?, ?, ?, ?)
                        """,
                        (file_sha256, channel_id, youtube_video_id, now),
                    )
            except Exception:
                pass

    def mark_thumbnail_set(
        self,
        *,
        output_video_id: str,
        thumbnail_url: Optional[str] = None,
        thumbnail_set: bool = True,
    ) -> None:
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            # Ensure row exists
            cur.execute(
                """
                INSERT OR IGNORE INTO video_uploads (output_video_id, status, retry_count, thumbnail_set)
                VALUES (?, 'UNKNOWN', 0, 0)
                """,
                (output_video_id,),
            )
            cur.execute(
                """
                UPDATE video_uploads
                SET thumbnail_set=?, thumbnail_url=?, thumbnail_updated_at=?
                WHERE output_video_id=?
                """,
                (1 if thumbnail_set else 0, thumbnail_url, now, output_video_id),
            )

    def list_uploads(
        self,
        *,
        status: Optional[str] = None,
        limit: int = 200,
        channel_ids: Optional[list[str]] = None,
        only_missing_thumbnail: bool = False,
        exclude_statuses: Optional[list[str]] = None,
    ) -> list[Dict[str, Any]]:
        """
        Lista upload recenti dal DB (per dashboard/automazioni).
        """
        safe_limit = max(1, min(int(limit), 2000))
        where: list[str] = []
        args: list[Any] = []
        if status:
            where.append("status = ?")
            args.append(status)
        elif exclude_statuses:
            cleaned = [str(s).strip().upper() for s in exclude_statuses if str(s).strip()]
            cleaned = [s for s in cleaned if s]
            if cleaned:
                placeholders = ",".join(["?"] * len(cleaned))
                where.append(f"status NOT IN ({placeholders})")
                args.extend(cleaned)
        if channel_ids:
            placeholders = ",".join(["?"] * len(channel_ids))
            where.append(f"channel_id IN ({placeholders})")
            args.extend(channel_ids)
        if only_missing_thumbnail:
            where.append("(COALESCE(thumbnail_set,0) = 0)")
        where_sql = ("WHERE " + " AND ".join(where)) if where else ""
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT output_video_id, status, job_id, channel_id, youtube_title, youtube_video_id,
                       retry_count, next_retry_at, started_at, completed_at, error,
                       COALESCE(thumbnail_set,0) AS thumbnail_set, thumbnail_url, thumbnail_updated_at
                FROM video_uploads
                {where_sql}
                ORDER BY COALESCE(completed_at, started_at) DESC
                LIMIT ?
                """,
                (*args, safe_limit),
            )
            rows = cur.fetchall() or []
        return [dict(r) for r in rows]

    def count_uploads_by_channel(
        self,
        *,
        status: Optional[str] = None,
        youtube_group: Optional[str] = None,
        channel_ids: Optional[list[str]] = None,
        only_missing_thumbnail: bool = False,
    ) -> Dict[str, int]:
        """
        Counts uploads grouped by channel_id.

        - If youtube_group is provided, it matches against video_job_specs.youtube_group via job_id.
        - Returns only channels with count > 0.
        """
        where: list[str] = []
        args: list[Any] = []

        if status:
            where.append("u.status = ?")
            args.append(status)
        if channel_ids:
            placeholders = ",".join(["?"] * len(channel_ids))
            where.append(f"u.channel_id IN ({placeholders})")
            args.extend(channel_ids)
        if only_missing_thumbnail:
            where.append("(COALESCE(u.thumbnail_set,0) = 0)")
        if youtube_group:
            where.append("s.youtube_group = ?")
            args.append(youtube_group)

        where_sql = ("WHERE " + " AND ".join(where)) if where else ""

        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT u.channel_id AS channel_id, COUNT(*) AS cnt
                FROM video_uploads u
                LEFT JOIN video_job_specs s ON s.job_id = u.job_id
                {where_sql}
                GROUP BY u.channel_id
                """,
                tuple(args),
            )
            rows = cur.fetchall() or []

        out: Dict[str, int] = {}
        for r in rows:
            try:
                cid = str(r["channel_id"] or "").strip()
                cnt = int(r["cnt"] or 0)
            except Exception:
                continue
            if cid and cnt > 0:
                out[cid] = cnt
        return out

    def get_upload_row_by_youtube_video_id(self, youtube_video_id: str) -> Optional[Dict[str, Any]]:
        """
        Finds a single upload row by youtube_video_id (best-effort).
        Returns a dict with the same shape as list_uploads() rows, or None if not found.
        """
        vid = str(youtube_video_id or "").strip()
        if not vid:
            return None
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT output_video_id, status, job_id, channel_id, youtube_title, youtube_video_id,
                       retry_count, next_retry_at, started_at, completed_at, error,
                       COALESCE(thumbnail_set,0) AS thumbnail_set, thumbnail_url, thumbnail_updated_at
                FROM video_uploads
                WHERE youtube_video_id = ?
                ORDER BY COALESCE(completed_at, started_at) DESC
                LIMIT 1
                """,
                (vid,),
            )
            row = cur.fetchone()
            return dict(row) if row else None

    def update_youtube_title_by_youtube_video_id(self, *, youtube_video_id: str, youtube_title: str) -> int:
        """
        Best-effort: updates youtube_title for rows matching youtube_video_id.
        Returns number of rows updated.
        """
        vid = str(youtube_video_id or "").strip()
        title = str(youtube_title or "").strip()
        if not vid or not title:
            return 0
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "UPDATE video_uploads SET youtube_title=? WHERE youtube_video_id=?",
                (title, vid),
            )
            return int(cur.rowcount or 0)

    def mark_failed(self, *, output_video_id: str, error: str) -> None:
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE video_uploads
                SET status='FAILED', error=?, completed_at=?
                WHERE output_video_id=?
                """,
                (error, now, output_video_id),
            )

    def mark_needs_input(self, *, output_video_id: str, error: str) -> None:
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE video_uploads
                SET status='NEEDS_INPUT', error=?, completed_at=?
                WHERE output_video_id=?
                """,
                (error, now, output_video_id),
            )

    def schedule_retry(self, *, output_video_id: str, error: str, retry_in_seconds: int) -> None:
        now = datetime.now(timezone.utc)
        next_at = (now + timedelta(seconds=max(0, int(retry_in_seconds)))).isoformat()
        with self._get_connection() as conn:
            cur = conn.cursor()
            # Ensure row exists
            cur.execute(
                """
                INSERT OR IGNORE INTO video_uploads (output_video_id, status, retry_count)
                VALUES (?, 'RETRY', 0)
                """,
                (output_video_id,),
            )
            cur.execute(
                """
                UPDATE video_uploads
                SET status='RETRY', error=?, retry_count=COALESCE(retry_count,0)+1, next_retry_at=?
                WHERE output_video_id=?
                """,
                (error, next_at, output_video_id),
            )

    def list_due_retries(self, *, limit: int = 20) -> list[UploadState]:
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT * FROM video_uploads
                WHERE status='RETRY' AND next_retry_at IS NOT NULL AND next_retry_at <= ?
                ORDER BY next_retry_at ASC
                LIMIT ?
                """,
                (now, int(limit)),
            )
            rows = cur.fetchall() or []
        return [
            UploadState(
                output_video_id=r["output_video_id"],
                status=r["status"],
                job_id=r["job_id"],
                channel_id=r["channel_id"],
                youtube_title=r["youtube_title"],
                youtube_video_id=r["youtube_video_id"],
                file_sha256=r["file_sha256"],
                retry_count=int(r["retry_count"] or 0),
                next_retry_at=r["next_retry_at"],
                started_at=r["started_at"],
                completed_at=r["completed_at"],
                error=r["error"],
            )
            for r in rows
        ]

    def upsert_youtube_channel_videos_cache(self, *, channel_id: str, items: list[Dict[str, Any]]) -> int:
        """
        Best-effort snapshot of channel content as seen via YouTube API.
        Accepts items shaped like /api/v1/youtube/videos entries: {video_id, title, privacy_status, published_at, thumbnail_url, youtube_url}.
        Returns number of rows upserted.
        """
        cid = str(channel_id or "").strip()
        if not cid:
            return 0
        now = _utc_now_iso()
        upserted = 0
        with self._get_connection() as conn:
            cur = conn.cursor()
            for it in items or []:
                if not isinstance(it, dict):
                    continue
                vid = str(it.get("youtube_video_id") or it.get("video_id") or "").strip()
                if not vid:
                    continue
                title = it.get("title")
                privacy_status = it.get("privacy_status")
                published_at = it.get("published_at")
                thumbnail_url = it.get("thumbnail_url")
                youtube_url = it.get("youtube_url")
                cur.execute(
                    """
                    INSERT INTO youtube_channel_videos_cache (
                        channel_id, youtube_video_id, title, privacy_status, published_at, thumbnail_url, youtube_url, last_seen_at, fetched_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    ON CONFLICT(channel_id, youtube_video_id) DO UPDATE SET
                        title=excluded.title,
                        privacy_status=excluded.privacy_status,
                        published_at=excluded.published_at,
                        thumbnail_url=excluded.thumbnail_url,
                        youtube_url=excluded.youtube_url,
                        last_seen_at=excluded.last_seen_at,
                        fetched_at=excluded.fetched_at
                    """,
                    (
                        cid,
                        vid,
                        (str(title) if title is not None else None),
                        (str(privacy_status) if privacy_status is not None else None),
                        (str(published_at) if published_at is not None else None),
                        (str(thumbnail_url) if thumbnail_url is not None else None),
                        (str(youtube_url) if youtube_url is not None else None),
                        now,
                        now,
                    ),
                )
                upserted += 1
        return upserted

    def list_youtube_channel_videos_cache(
        self,
        *,
        channel_id: str,
        privacy_statuses: Optional[list[str]] = None,
        since_days: int = 0,
        limit: int = 200,
    ) -> list[Dict[str, Any]]:
        cid = str(channel_id or "").strip()
        if not cid:
            return []
        safe_limit = max(1, min(int(limit), 2000))
        where: list[str] = ["channel_id = ?"]
        args: list[Any] = [cid]
        if privacy_statuses:
            cleaned = [str(s).strip().lower() for s in privacy_statuses if str(s).strip()]
            cleaned = [s for s in cleaned if s]
            if cleaned:
                placeholders = ",".join(["?"] * len(cleaned))
                where.append(f"LOWER(COALESCE(privacy_status,'')) IN ({placeholders})")
                args.extend(cleaned)
        if int(since_days or 0) > 0:
            now = datetime.now(timezone.utc)
            cutoff = (now - timedelta(days=max(0, int(since_days)))).isoformat()
            where.append("(published_at IS NULL OR published_at >= ?)")
            args.append(cutoff)
        where_sql = "WHERE " + " AND ".join(where)
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT channel_id, youtube_video_id, title, privacy_status, published_at, thumbnail_url, youtube_url, last_seen_at, fetched_at
                FROM youtube_channel_videos_cache
                {where_sql}
                ORDER BY COALESCE(published_at, fetched_at, last_seen_at) DESC
                LIMIT ?
                """,
                (*args, safe_limit),
            )
            rows = cur.fetchall() or []
        return [dict(r) for r in rows]

    def prune_youtube_channel_videos_cache(
        self,
        *,
        channel_id: str,
        keep_youtube_video_ids: list[str],
        privacy_statuses: Optional[list[str]] = None,
    ) -> int:
        """
        Removes cached rows for a channel that are not present anymore in the latest fetched set.
        Typically called after a refresh from YouTube API for a given privacy filter.
        Returns number of rows deleted.
        """
        cid = str(channel_id or "").strip()
        if not cid:
            return 0

        keep = [str(x).strip() for x in (keep_youtube_video_ids or []) if str(x).strip()]
        # If keep is empty, we still may want to prune (delete) the filtered set for this channel.

        where: list[str] = ["channel_id = ?"]
        args: list[Any] = [cid]

        if privacy_statuses:
            cleaned = [str(s).strip().lower() for s in privacy_statuses if str(s).strip()]
            cleaned = [s for s in cleaned if s]
            if cleaned:
                placeholders = ",".join(["?"] * len(cleaned))
                where.append(f"LOWER(COALESCE(privacy_status,'')) IN ({placeholders})")
                args.extend(cleaned)

        if keep:
            placeholders = ",".join(["?"] * len(keep))
            where.append(f"youtube_video_id NOT IN ({placeholders})")
            args.extend(keep)

        where_sql = "WHERE " + " AND ".join(where)
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(f"DELETE FROM youtube_channel_videos_cache {where_sql}", tuple(args))
            return int(cur.rowcount or 0)

    def delete_youtube_channel_video_cache(self, *, channel_id: str, youtube_video_id: str) -> int:
        cid = str(channel_id or "").strip()
        vid = str(youtube_video_id or "").strip()
        if not cid or not vid:
            return 0
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM youtube_channel_videos_cache WHERE channel_id=? AND youtube_video_id=?",
                (cid, vid),
            )
            return int(cur.rowcount or 0)

    def get_uploaded_video_id_by_hash(self, *, sha256: str, channel_id: str) -> Optional[str]:
        if not sha256 or not channel_id:
            return None
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT youtube_video_id FROM uploaded_file_hashes WHERE sha256=? AND channel_id=?",
                (sha256, channel_id),
            )
            row = cur.fetchone()
            if not row:
                return None
            return row["youtube_video_id"]

    def cleanup_old(self, *, uploads_days: int, file_hash_days: int) -> None:
        """Remove old upload records and file-hash mappings (best effort)."""
        now = datetime.now(timezone.utc)
        uploads_cutoff = (now - timedelta(days=int(uploads_days))).isoformat()
        hashes_cutoff = (now - timedelta(days=int(file_hash_days))).isoformat()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM video_uploads WHERE completed_at IS NOT NULL AND completed_at < ?",
                (uploads_cutoff,),
            )
            cur.execute(
                "DELETE FROM uploaded_file_hashes WHERE uploaded_at IS NOT NULL AND uploaded_at < ?",
                (hashes_cutoff,),
            )

    def mark_deleted(
        self,
        *,
        output_video_id: str,
        reason: Optional[str] = None,
        clear_file_hash: bool = True,
    ) -> bool:
        """
        Marks an upload row as DELETED (local state only).
        Optionally removes the uploaded_file_hashes mapping to allow re-uploads.
        """
        oid = str(output_video_id or "").strip()
        if not oid:
            return False
        now = _utc_now_iso()
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT file_sha256, channel_id FROM video_uploads WHERE output_video_id=?",
                (oid,),
            )
            row = cur.fetchone()
            file_sha256 = (row["file_sha256"] if row else None) if row else None
            channel_id = (row["channel_id"] if row else None) if row else None

            cur.execute(
                """
                UPDATE video_uploads
                SET status='DELETED', error=?, completed_at=?
                WHERE output_video_id=?
                """,
                ((str(reason)[:500] if reason else "deleted"), now, oid),
            )
            updated = int(cur.rowcount or 0) > 0

            if clear_file_hash and file_sha256 and channel_id:
                cur.execute(
                    "DELETE FROM uploaded_file_hashes WHERE sha256=? AND channel_id=?",
                    (file_sha256, channel_id),
                )

        return updated

    def delete_upload(
        self,
        *,
        output_video_id: str,
        clear_file_hash: bool = True,
    ) -> bool:
        """
        Hard delete: removes the upload row from video_uploads.
        Optionally removes uploaded_file_hashes mapping to allow re-uploads.
        """
        oid = str(output_video_id or "").strip()
        if not oid:
            return False
        with self._get_connection() as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT file_sha256, channel_id FROM video_uploads WHERE output_video_id=?",
                (oid,),
            )
            row = cur.fetchone()
            file_sha256 = (row["file_sha256"] if row else None) if row else None
            channel_id = (row["channel_id"] if row else None) if row else None

            cur.execute("DELETE FROM video_uploads WHERE output_video_id=?", (oid,))
            deleted = int(cur.rowcount or 0) > 0

            if clear_file_hash and file_sha256 and channel_id:
                cur.execute(
                    "DELETE FROM uploaded_file_hashes WHERE sha256=? AND channel_id=?",
                    (file_sha256, channel_id),
                )

        return deleted
