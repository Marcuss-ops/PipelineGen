#!/usr/bin/env python3
"""Quota-expensive Artlist/VidRush scale, indexing and replay battery.

Default matrix: 20 keywords x 10 clips. The runner exercises the real APIs,
Drive, SQLite, VLM and Qdrant, then replays the same workload and requires zero
new successful Artlist download-audit rows.
"""

from __future__ import annotations

import concurrent.futures
import json
import os
import shlex
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable


DEFAULT_KEYWORDS = [
    "business team modern office",
    "city skyline aerial drone",
    "factory automation robots",
    "doctor hospital technology",
    "solar panels renewable energy",
    "boxing training gym",
    "basketball practice court",
    "family cooking kitchen",
    "cybersecurity server room",
    "financial trading screens",
    "construction workers building",
    "electric car charging",
    "scientist laboratory research",
    "airport travelers terminal",
    "farmer tractor field",
    "ocean waves coastline",
    "students classroom learning",
    "warehouse logistics packages",
    "coffee shop barista",
    "mountain hiking adventure",
]

DIAGNOSTIC_PROBES = (
    "scraper",
    "browser",
    "session",
    "downloader",
    "ffmpeg_binary",
    "drive_folder",
    "sqlite_writable",
    "outbox_dispatcher",
    "qdrant_reachable",
    "embedding_provider",
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
        yield values[index : index + size]


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
        scraper_url = os.getenv(
            "VELOX_ARTLIST_SCRAPER_SERVER_URL", "http://127.0.0.1:9123"
        ).rstrip("/")
        qdrant_url = os.getenv("QDRANT_URL", "http://127.0.0.1:6333").rstrip("/")
        data_dir = Path(os.getenv("VELOX_DATA_DIR", "./data"))
        if not data_dir.is_absolute():
            data_dir = repo_root / data_dir
        db_path = data_dir / "media" / "media.db.sqlite"

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
        report_dir = Path(os.getenv("ARTLIST_SCALE_REPORT_DIR", f"/tmp/artlist_scale_{stamp}"))

        admin_bin = os.getenv("ARTLIST_SCALE_ADMIN_BIN", "").strip()
        admin_command = shlex.split(admin_bin) if admin_bin else ["go", "run", "./cmd/admin"]

        return cls(
            repo_root=repo_root,
            base_url=base_url,
            scraper_url=scraper_url,
            qdrant_url=qdrant_url,
            qdrant_api_key=os.getenv("QDRANT_API_KEY", os.getenv("VELOX_QDRANT_API_KEY", "")),
            qdrant_collection=os.getenv("QDRANT_COLLECTION", "media_assets_current"),
            db_path=db_path,
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
            report_dir=report_dir,
            admin_command=admin_command,
        )


class HttpClient:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings

    def request(
        self,
        method: str,
        url: str,
        *,
        payload: dict[str, Any] | None = None,
        admin: bool = False,
        headers: dict[str, str] | None = None,
        timeout: int | None = None,
    ) -> Any:
        request_headers = {"Accept": "application/json"}
        if payload is not None:
            request_headers["Content-Type"] = "application/json"
        if admin:
            request_headers["X-Velox-Admin-Token"] = self.settings.admin_token
        if headers:
            request_headers.update(headers)
        body = json.dumps(payload).encode("utf-8") if payload is not None else None
        request = urllib.request.Request(url, data=body, headers=request_headers, method=method)
        try:
            with urllib.request.urlopen(
                request,
                timeout=timeout or self.settings.http_timeout,
            ) as response:
                raw = response.read()
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"HTTP {exc.code} {method} {url}: {detail[:1000]}") from exc
        except urllib.error.URLError as exc:
            raise RuntimeError(f"request failed {method} {url}: {exc}") from exc
        if not raw:
            return {}
        try:
            return json.loads(raw)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"non-JSON response from {method} {url}: {raw[:500]!r}") from exc

    def get(self, url: str, *, admin: bool = False, headers: dict[str, str] | None = None) -> Any:
        return self.request("GET", url, admin=admin, headers=headers)

    def post(
        self,
        url: str,
        payload: dict[str, Any],
        *,
        admin: bool = False,
        headers: dict[str, str] | None = None,
    ) -> Any:
        return self.request("POST", url, payload=payload, admin=admin, headers=headers)


class ScaleRunner:
    def __init__(self, settings: Settings) -> None:
        self.s = settings
        self.http = HttpClient(settings)
        self.failures: list[str] = []
        self.health_samples: list[dict[str, Any]] = []
        self.stop_health = threading.Event()
        self.health_thread: threading.Thread | None = None
        self.phase_results: dict[str, list[dict[str, Any]]] = {}
        self.report: dict[str, Any] = {}
        self.s.report_dir.mkdir(parents=True, exist_ok=True)

    def log(self, message: str) -> None:
        print(f"[{datetime.now().strftime('%H:%M:%S')}] {message}", flush=True)

    def fail(self, message: str) -> None:
        self.failures.append(message)
        self.log(f"FAIL: {message}")

    def write_json(self, name: str, value: Any) -> None:
        path = self.s.report_dir / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, indent=2, ensure_ascii=False, default=str) + "\n", encoding="utf-8")

    @staticmethod
    def diagnostics_ok(payload: Any) -> tuple[bool, list[str]]:
        if not isinstance(payload, dict):
            return False, ["invalid diagnostics response"]
        failed = [
            name
            for name in DIAGNOSTIC_PROBES
            if not isinstance(payload.get(name), dict) or payload[name].get("ok") is not True
        ]
        return not failed, failed

    def validate_settings(self) -> None:
        if not self.s.admin_token:
            raise RuntimeError("VELOX_ADMIN_TOKEN is required; use scripts/with-velox-auth")
        if not self.s.root_folder_id:
            raise RuntimeError("VELOX_DRIVE_ARTLIST_ROOT or ROOT_FOLDER_ID is required")
        if not self.s.db_path.is_file():
            raise RuntimeError(f"SQLite database not found: {self.s.db_path}")

    def preflight(self) -> None:
        self.log("Preflight: PipelineGen, scraper, canonical Artlist probes and Drive config")
        health = self.http.get(f"{self.s.base_url}/health")
        ready = self.http.get(f"{self.s.base_url}/ready")
        scraper = self.http.get(f"{self.s.scraper_url}/health")
        consumer = self.http.get(f"{self.s.base_url}/api/artlist/job-consumer", admin=True)
        diagnostics = self.http.get(f"{self.s.base_url}/api/artlist/diagnostics", admin=True)
        diagnostics_ok, failed_probes = self.diagnostics_ok(diagnostics)

        self.write_json("preflight/health.json", health)
        self.write_json("preflight/ready.json", ready)
        self.write_json("preflight/scraper_health.json", scraper)
        self.write_json("preflight/job_consumer.json", consumer)
        self.write_json("preflight/diagnostics.json", diagnostics)

        if ready.get("status") != "ready":
            raise RuntimeError(f"PipelineGen is not ready: {ready}")
        if scraper.get("healthy") is not True and scraper.get("ok") is not True:
            raise RuntimeError(f"Artlist scraper is not healthy: {scraper}")
        if not diagnostics_ok:
            raise RuntimeError(f"Artlist diagnostics failed probes: {', '.join(failed_probes)}")

    def warmup(self) -> None:
        term = self.s.keywords[0]
        start = time.monotonic()
        response = self.http.post(f"{self.s.scraper_url}/search", {"term": term, "limit": 1})
        elapsed_ms = round((time.monotonic() - start) * 1000)
        self.write_json("preflight/warmup.json", response)
        self.write_json(
            "preflight/warmup_metrics.json",
            {"term": term, "elapsed_ms": elapsed_ms},
        )
        clips = response.get("clips") if isinstance(response, dict) else None
        if response.get("ok") is not True or not isinstance(clips, list) or not clips:
            raise RuntimeError(f"Artlist scraper warmup returned no clips: {response}")
        self.log(f"Warmup complete in {elapsed_ms} ms")

    def health_sample(self) -> dict[str, Any]:
        sample: dict[str, Any] = {
            "timestamp": utc_now(),
            "pipeline_ready": False,
            "scraper_healthy": False,
            "diagnostics_ok": False,
            "failed_probes": [],
        }
        try:
            ready = self.http.get(f"{self.s.base_url}/ready")
            sample["pipeline_ready"] = ready.get("status") == "ready"
        except Exception as exc:  # noqa: BLE001 - operational sampler records every failure
            sample["ready_error"] = str(exc)
        try:
            scraper = self.http.get(f"{self.s.scraper_url}/health")
            sample["scraper_healthy"] = scraper.get("healthy") is True or scraper.get("ok") is True
        except Exception as exc:  # noqa: BLE001
            sample["scraper_error"] = str(exc)
        try:
            diagnostics = self.http.get(f"{self.s.base_url}/api/artlist/diagnostics", admin=True)
            ok, failed = self.diagnostics_ok(diagnostics)
            sample["diagnostics_ok"] = ok
            sample["failed_probes"] = failed
        except Exception as exc:  # noqa: BLE001
            sample["diagnostics_error"] = str(exc)
        return sample

    def health_loop(self) -> None:
        while not self.stop_health.is_set():
            self.health_samples.append(self.health_sample())
            self.stop_health.wait(self.s.health_interval)

    def start_health_monitor(self) -> None:
        self.health_thread = threading.Thread(target=self.health_loop, name="artlist-health", daemon=True)
        self.health_thread.start()

    def stop_health_monitor(self) -> None:
        self.stop_health.set()
        if self.health_thread is not None:
            self.health_thread.join(timeout=self.s.http_timeout + 5)
        self.write_json("availability/api_health.json", self.health_samples)
        unhealthy = [
            sample
            for sample in self.health_samples
            if not (
                sample.get("pipeline_ready")
                and sample.get("scraper_healthy")
                and sample.get("diagnostics_ok")
            )
        ]
        if unhealthy:
            self.fail(f"API health monitor observed {len(unhealthy)} unhealthy samples")

    def audit_count(self) -> int:
        with sqlite3.connect(f"file:{self.s.db_path}?mode=ro", uri=True) as conn:
            row = conn.execute(
                "SELECT COUNT(*) FROM artlist_download_audit WHERE status='succeeded'"
            ).fetchone()
        return int(row[0]) if row else 0

    def submit_run(self, term: str, limit: int) -> str:
        payload = {
            "term": term,
            "limit": limit,
            "strategy": "verify",
            "dry_run": False,
            "clip_duration": 7,
            "width": 1920,
            "height": 1080,
            "fps": 30,
            "concurrency": self.s.clip_concurrency,
            "root_folder_id": self.s.root_folder_id,
        }
        response = self.http.post(f"{self.s.base_url}/api/artlist/run", payload, admin=True)
        run_id = str(response.get("run_id", "")).strip()
        if not run_id:
            raise RuntimeError(f"Artlist run submission returned no run_id for {term!r}: {response}")
        return run_id

    def poll_job(self, phase: str, index: int, term: str, run_id: str) -> dict[str, Any]:
        start = time.monotonic()
        last: dict[str, Any] = {}
        status = "UNKNOWN"
        for attempt in range(1, self.s.poll_max + 1):
            last = self.http.get(f"{self.s.base_url}/api/jobs/{run_id}/full", admin=True)
            status = str(last.get("status", "UNKNOWN")).upper()
            if status in TERMINAL_JOB_STATUSES:
                break
            time.sleep(self.s.poll_interval)
        else:
            status = "TIMEOUT"

        result = {
            "phase": phase,
            "keyword_index": index,
            "term": term,
            "run_id": run_id,
            "status": status,
            "elapsed_ms": round((time.monotonic() - start) * 1000),
            "job": last,
        }
        self.write_json(f"{phase}/job_{index:02d}.json", result)
        return result

    def run_phase(self, phase: str, *, limit: int | None = None, terms: list[str] | None = None) -> list[dict[str, Any]]:
        selected_terms = terms or self.s.keywords
        selected_limit = limit or self.s.clips_per_keyword
        self.log(
            f"{phase}: submitting {len(selected_terms)} jobs "
            f"({selected_limit} clips each, clip concurrency={self.s.clip_concurrency})"
        )
        submissions: list[tuple[int, str, str]] = []
        for index, term in enumerate(selected_terms, start=1):
            try:
                submissions.append((index, term, self.submit_run(term, selected_limit)))
            except Exception as exc:  # noqa: BLE001
                self.fail(f"{phase} submit failed for keyword[{index}] {term!r}: {exc}")

        results: list[dict[str, Any]] = []
        if submissions:
            workers = min(self.s.poll_workers, len(submissions))
            with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
                future_map = {
                    executor.submit(self.poll_job, phase, index, term, run_id): (index, term)
                    for index, term, run_id in submissions
                }
                for future in concurrent.futures.as_completed(future_map):
                    index, term = future_map[future]
                    try:
                        results.append(future.result())
                    except Exception as exc:  # noqa: BLE001
                        self.fail(f"{phase} poll failed for keyword[{index}] {term!r}: {exc}")
        results.sort(key=lambda item: item["keyword_index"])
        self.phase_results[phase] = results
        self.write_json(f"{phase}/statuses.json", [
            {key: value for key, value in item.items() if key != "job"} for item in results
        ])

        if len(submissions) != len(selected_terms):
            self.fail(f"{phase} submitted {len(submissions)}/{len(selected_terms)} jobs")
        succeeded = sum(1 for item in results if item["status"] in SUCCESS_JOB_STATUSES)
        if succeeded != len(submissions):
            self.fail(f"{phase} completed {succeeded}/{len(submissions)} jobs successfully")
        return results

    def phase_items(self, phase: str, expected_per_job: int) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        for result in self.phase_results.get(phase, []):
            job_result = result.get("job", {}).get("result", {})
            job_items = job_result.get("items", []) if isinstance(job_result, dict) else []
            if not isinstance(job_items, list):
                job_items = []
            if len(job_items) != expected_per_job:
                self.fail(
                    f"{phase} keyword {result['term']!r} returned "
                    f"{len(job_items)}/{expected_per_job} items"
                )
            for item in job_items:
                if isinstance(item, dict):
                    items.append({"term": result["term"], "run_id": result["run_id"], **item})
        self.write_json(f"{phase}/items.json", items)
        return items

    def load_assets(self, target_ids: list[str]) -> list[dict[str, Any]]:
        if not target_ids:
            return []
        placeholders = ",".join("?" for _ in target_ids)
        query = f"""
            SELECT id, source, media_type, lifecycle_state, index_state,
                   COALESCE(drive_file_id,'') AS drive_file_id,
                   COALESCE(drive_link,'') AS drive_link,
                   COALESCE(download_link,'') AS download_link,
                   COALESCE(local_path,'') AS local_path,
                   COALESCE(file_hash,'') AS file_hash,
                   COALESCE(source_version,'') AS source_version,
                   COALESCE(source_url,'') AS source_url,
                   COALESCE(metadata_json,'{{}}') AS metadata_json
            FROM media_assets WHERE id IN ({placeholders})
        """
        with sqlite3.connect(f"file:{self.s.db_path}?mode=ro", uri=True) as conn:
            conn.row_factory = sqlite3.Row
            return [dict(row) for row in conn.execute(query, target_ids)]

    def validate_assets(self, target_ids: list[str]) -> list[dict[str, Any]]:
        rows = self.load_assets(target_ids)
        found_ids = {row["id"] for row in rows}
        missing = sorted(set(target_ids) - found_ids)
        invalid: list[str] = []
        m3u8_ids: list[str] = []
        for row in rows:
            valid = all(
                (
                    row["source"] == "artlist",
                    row["media_type"] == "video",
                    row["lifecycle_state"] == "PUBLISHED",
                    row["index_state"] == "INDEXED",
                    bool(row["drive_file_id"]),
                    bool(row["drive_link"]),
                    bool(row["file_hash"]),
                    bool(row["source_version"]),
                    bool(row["source_url"] or row["download_link"]),
                )
            )
            if not valid:
                invalid.append(row["id"])
            haystack = " ".join(
                (row["source_url"], row["download_link"], row["metadata_json"])
            ).lower()
            if ".m3u8" in haystack:
                m3u8_ids.append(row["id"])

        report = {
            "requested_ids": len(target_ids),
            "found_rows": len(rows),
            "missing_ids": missing,
            "invalid_ids": invalid,
            "m3u8_persisted_count": len(m3u8_ids),
            "m3u8_persisted_ids": sorted(m3u8_ids),
            "assets": rows,
        }
        self.write_json("sqlite/assets.json", report)
        if missing:
            self.fail(f"SQLite is missing {len(missing)} target assets")
        if invalid:
            self.fail(f"{len(invalid)} target assets failed publication/index/Drive/hash checks")
        if self.s.require_m3u8 and not m3u8_ids:
            self.fail("no target asset persists an m3u8 URL in source_url, download_link or metadata_json")
        return rows

    def validate_drive(self, assets: list[dict[str, Any]]) -> None:
        drive_ids = sorted({row["drive_file_id"] for row in assets if row["drive_file_id"]})
        resolved_total = 0
        invalid: list[str] = []
        responses: list[Any] = []
        for batch in chunks(drive_ids, 50):
            response = self.http.post(
                f"{self.s.base_url}/api/drive/resolve-by-id",
                {"ids": batch},
                admin=True,
            )
            responses.append(response)
            resolved = response.get("resolved", []) if isinstance(response, dict) else []
            resolved_total += int(response.get("resolved_count", len(resolved)))
            for entry in resolved:
                size = int(entry.get("size", 0) or 0)
                if entry.get("trashed") is not False or size <= 0:
                    invalid.append(str(entry.get("id", entry.get("file_id", "unknown"))))
        self.write_json("drive/resolve.json", responses)
        if resolved_total != len(drive_ids):
            self.fail(f"Drive resolved {resolved_total}/{len(drive_ids)} unique files")
        if invalid:
            self.fail(f"Drive contains {len(invalid)} missing, trashed or empty target files")

    def run_admin(self, args: list[str], output_name: str) -> None:
        command = [*self.s.admin_command, *args]
        completed = subprocess.run(
            command,
            cwd=self.s.repo_root,
            env=os.environ.copy(),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=max(self.s.http_timeout, self.s.vlm_timeout) * 20,
            check=False,
        )
        self.write_json(
            output_name,
            {
                "command": command,
                "returncode": completed.returncode,
                "stdout": completed.stdout,
                "stderr": completed.stderr,
            },
        )
        if completed.returncode != 0:
            raise RuntimeError(
                f"admin command failed ({completed.returncode}): {' '.join(command)}\n"
                f"{completed.stderr[-2000:]}"
            )

    def validate_vlm(self, target_ids: list[str]) -> dict[str, Any]:
        if not self.s.run_vlm:
            report = {"skipped": True, "requested_ids": len(target_ids)}
            self.write_json("vlm/validation.json", report)
            return report

        self.log("VLM: generating canonical visual summaries for Artlist assets")
        self.run_admin(
            [
                "reindex-visual-summary",
                "--apply",
                "--source=artlist",
                f"--interval={self.s.vlm_interval}",
                f"--vlm-timeout={self.s.vlm_timeout}",
                "--json",
            ],
            "vlm/reindex_command.json",
        )

        placeholders = ",".join("?" for _ in target_ids)
        rows: list[dict[str, Any]] = []
        if target_ids:
            query = f"""
                SELECT asset_id, visual_summary_text, visible_actions_json,
                       visible_entities_json, frame_count, interval_seconds,
                       preprocessing_version, model_name, model_version,
                       source_hash, sampled_at
                FROM asset_visual_summaries WHERE asset_id IN ({placeholders})
            """
            with sqlite3.connect(f"file:{self.s.db_path}?mode=ro", uri=True) as conn:
                conn.row_factory = sqlite3.Row
                rows = [dict(row) for row in conn.execute(query, target_ids)]
        valid_ids = {
            row["asset_id"]
            for row in rows
            if int(row["frame_count"] or 0) > 0
            and int(row["interval_seconds"] or 0) > 0
            and row["preprocessing_version"]
            and row["model_name"]
            and row["model_version"]
            and row["source_hash"]
            and row["sampled_at"]
        }
        invalid_ids = sorted(set(target_ids) - valid_ids)
        report = {
            "requested_ids": len(target_ids),
            "rows": len(rows),
            "valid_rows": len(valid_ids),
            "invalid_ids": invalid_ids,
        }
        self.write_json("vlm/validation.json", report)
        if invalid_ids:
            self.fail(f"VLM produced valid summaries for {len(valid_ids)}/{len(target_ids)} target assets")
        return report

    def validate_qdrant(self, target_ids: list[str]) -> dict[str, Any]:
        if not self.s.run_vlm:
            report = {"skipped": True, "reason": "VLM disabled"}
            self.write_json("qdrant/validation.json", report)
            return report
        if self.s.run_qdrant_reindex:
            self.log("Qdrant: running blue-green reindex after VLM pass")
            self.run_admin(
                ["reindex-qdrant", "--apply", "--json"],
                "qdrant/reindex_command.json",
            )

        headers = {"api-key": self.s.qdrant_api_key} if self.s.qdrant_api_key else {}
        payloads: list[dict[str, Any]] = []
        for batch in chunks(target_ids, 50):
            response = self.http.post(
                f"{self.s.qdrant_url}/collections/{self.s.qdrant_collection}/points/scroll",
                {
                    "filter": {"must": [{"key": "asset_id", "match": {"any": batch}}]},
                    "limit": 100,
                    "with_payload": True,
                    "with_vector": False,
                },
                headers=headers,
            )
            for point in response.get("result", {}).get("points", []):
                payload = point.get("payload")
                if isinstance(payload, dict):
                    payloads.append(payload)

        valid_ids = {
            str(payload.get("asset_id"))
            for payload in payloads
            if payload.get("source") == "artlist"
            and payload.get("lifecycle_state") == "PUBLISHED"
            and payload.get("visual_preprocessing_version")
            and payload.get("visual_model_name")
            and payload.get("visual_model_version")
        }
        missing = sorted(set(target_ids) - valid_ids)
        report = {
            "requested_ids": len(target_ids),
            "valid_payloads": len(valid_ids),
            "missing_or_invalid_ids": missing,
        }
        self.write_json("qdrant/validation.json", report)
        if missing:
            self.fail(f"Qdrant contains valid VLM payloads for {len(valid_ids)}/{len(target_ids)} target assets")
        return report

    @staticmethod
    def identity_snapshot(assets: list[dict[str, Any]]) -> dict[str, dict[str, str]]:
        return {
            row["id"]: {
                "drive_file_id": row["drive_file_id"],
                "drive_link": row["drive_link"],
                "file_hash": row["file_hash"],
                "source_url": row["source_url"],
                "download_link": row["download_link"],
            }
            for row in assets
        }

    def replay(self, target_ids: list[str], identity_before: dict[str, dict[str, str]]) -> dict[str, Any]:
        report: dict[str, Any] = {"enabled": self.s.run_replay}
        if not self.s.run_replay:
            self.write_json("replay/validation.json", report)
            return report

        canary_before = self.audit_count()
        canary_results = self.run_phase(
            "replay_canary",
            limit=1,
            terms=[self.s.keywords[0]],
        )
        self.phase_items("replay_canary", 1)
        canary_after = self.audit_count()
        canary_delta = canary_after - canary_before
        report["canary_download_audit_delta"] = canary_delta
        report["canary_statuses"] = [item["status"] for item in canary_results]
        if canary_delta != 0:
            self.fail(
                f"replay canary created {canary_delta} successful download-audit rows; "
                "full replay aborted to protect Artlist quota"
            )
            self.write_json("replay/validation.json", report)
            return report

        replay_before = self.audit_count()
        self.run_phase("replay")
        self.phase_items("replay", self.s.clips_per_keyword)
        replay_after = self.audit_count()
        replay_delta = replay_after - replay_before
        assets_after = self.load_assets(target_ids)
        identity_after = self.identity_snapshot(assets_after)
        changed = sorted(
            asset_id
            for asset_id in target_ids
            if identity_before.get(asset_id) != identity_after.get(asset_id)
        )
        report.update(
            {
                "download_audit_delta": replay_delta,
                "changed_identity_ids": changed,
            }
        )
        if self.s.require_no_redownload and replay_delta != 0:
            self.fail(f"full replay created {replay_delta} successful download-audit rows; expected zero")
        if changed:
            self.fail(f"replay changed Drive/hash identity for {len(changed)} assets")
        self.write_json("replay/validation.json", report)
        return report

    def execute(self) -> int:
        started = time.monotonic()
        first_audit_before = 0
        first_audit_after = 0
        target_ids: list[str] = []
        assets: list[dict[str, Any]] = []
        vlm_report: dict[str, Any] = {}
        qdrant_report: dict[str, Any] = {}
        replay_report: dict[str, Any] = {}

        try:
            self.validate_settings()
            self.preflight()
            self.warmup()
            self.start_health_monitor()

            first_audit_before = self.audit_count()
            self.run_phase("first")
            first_items = self.phase_items("first", self.s.clips_per_keyword)
            first_audit_after = self.audit_count()
            target_ids = sorted(
                {str(item.get("clip_id", "")).strip() for item in first_items if item.get("clip_id")}
            )
            if not target_ids:
                raise RuntimeError("first phase produced no target clip IDs")

            assets = self.validate_assets(target_ids)
            self.validate_drive(assets)
            identity_before = self.identity_snapshot(assets)
            self.write_json("sqlite/identity_before.json", identity_before)
            vlm_report = self.validate_vlm(target_ids)
            qdrant_report = self.validate_qdrant(target_ids)
            replay_report = self.replay(target_ids, identity_before)
        except Exception as exc:  # noqa: BLE001 - final operational envelope must be written
            self.fail(str(exc))
        finally:
            if self.health_thread is not None:
                self.stop_health_monitor()

        elapsed_ms = round((time.monotonic() - started) * 1000)
        first_items_count = sum(
            len(
                result.get("job", {}).get("result", {}).get("items", [])
                if isinstance(result.get("job", {}).get("result", {}), dict)
                else []
            )
            for result in self.phase_results.get("first", [])
        )
        assets_per_minute = round(
            first_items_count / (elapsed_ms / 60000), 3
        ) if elapsed_ms > 0 else 0.0
        if assets_per_minute < self.s.min_assets_per_minute:
            self.fail(
                f"throughput {assets_per_minute} assets/min is below minimum "
                f"{self.s.min_assets_per_minute}"
            )

        summary = {
            "ok": not self.failures,
            "report_dir": str(self.s.report_dir),
            "matrix": {
                "keywords": len(self.s.keywords),
                "clips_per_keyword": self.s.clips_per_keyword,
                "requested_items": len(self.s.keywords) * self.s.clips_per_keyword,
            },
            "results": {
                "returned_items": first_items_count,
                "unique_assets": len(target_ids),
            },
            "performance": {
                "clip_concurrency": self.s.clip_concurrency,
                "elapsed_ms": elapsed_ms,
                "assets_per_minute": assets_per_minute,
            },
            "availability": {
                "samples": len(self.health_samples),
                "unhealthy_samples": sum(
                    1
                    for sample in self.health_samples
                    if not (
                        sample.get("pipeline_ready")
                        and sample.get("scraper_healthy")
                        and sample.get("diagnostics_ok")
                    )
                ),
            },
            "dedup": {
                "first_download_audit_delta": first_audit_after - first_audit_before,
                **replay_report,
            },
            "vlm": vlm_report,
            "qdrant": qdrant_report,
            "failures": self.failures,
        }
        self.report = summary
        self.write_json("summary.json", summary)
        print(json.dumps(summary, indent=2, ensure_ascii=False), flush=True)
        if self.failures:
            self.log("FAILURES:")
            for failure in self.failures:
                print(f"  - {failure}", flush=True)
            return 1
        self.log("PASS: Artlist scale, Drive, VLM/Qdrant and replay dedup checks succeeded")
        return 0


def main() -> int:
    try:
        settings = Settings.load()
    except Exception as exc:  # noqa: BLE001
        print(f"configuration error: {exc}", file=sys.stderr)
        return 2
    return ScaleRunner(settings).execute()


if __name__ == "__main__":
    raise SystemExit(main())
