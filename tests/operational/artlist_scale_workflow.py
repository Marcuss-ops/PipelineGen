from __future__ import annotations

import concurrent.futures
import threading
import time
from typing import Any

from artlist_scale_config import SUCCESS_JOB_STATUSES, TERMINAL_JOB_STATUSES, utc_now


def validate_settings(runner: Any) -> None:
    if not runner.s.admin_token:
        raise RuntimeError("VELOX_ADMIN_TOKEN is required; use scripts/with-velox-auth")
    if not runner.s.root_folder_id:
        raise RuntimeError("VELOX_DRIVE_ARTLIST_ROOT or ROOT_FOLDER_ID is required")
    if not runner.s.db_path.is_file():
        raise RuntimeError(f"SQLite database not found: {runner.s.db_path}")


def preflight(runner: Any) -> None:
    runner.log("Preflight: PipelineGen, scraper, canonical Artlist probes and Drive config")
    health = runner.http.get(f"{runner.s.base_url}/health")
    ready = runner.http.get(f"{runner.s.base_url}/ready")
    scraper = runner.http.get(f"{runner.s.scraper_url}/health")
    consumer = runner.http.get(f"{runner.s.base_url}/api/artlist/job-consumer", admin=True)
    diagnostics = runner.http.get(f"{runner.s.base_url}/api/artlist/diagnostics", admin=True)
    diagnostics_ok, failed_probes = runner.diagnostics_ok(diagnostics)
    runner.write_json("preflight/health.json", health)
    runner.write_json("preflight/ready.json", ready)
    runner.write_json("preflight/scraper_health.json", scraper)
    runner.write_json("preflight/job_consumer.json", consumer)
    runner.write_json("preflight/diagnostics.json", diagnostics)
    if ready.get("status") != "ready":
        raise RuntimeError(f"PipelineGen is not ready: {ready}")
    if scraper.get("healthy") is not True and scraper.get("ok") is not True:
        raise RuntimeError(f"Artlist scraper is not healthy: {scraper}")
    if not diagnostics_ok:
        raise RuntimeError(f"Artlist diagnostics failed probes: {', '.join(failed_probes)}")


def warmup(runner: Any) -> None:
    term = runner.s.keywords[0]
    start = time.monotonic()
    response = runner.http.post(f"{runner.s.scraper_url}/search", {"term": term, "limit": 1})
    elapsed_ms = round((time.monotonic() - start) * 1000)
    runner.write_json("preflight/warmup.json", response)
    runner.write_json("preflight/warmup_metrics.json", {"term": term, "elapsed_ms": elapsed_ms})
    clips = response.get("clips") if isinstance(response, dict) else None
    if response.get("ok") is not True or not isinstance(clips, list) or not clips:
        raise RuntimeError(f"Artlist scraper warmup returned no clips: {response}")
    runner.log(f"Warmup complete in {elapsed_ms} ms")


def health_sample(runner: Any) -> dict[str, Any]:
    sample: dict[str, Any] = {"timestamp": utc_now(), "pipeline_ready": False, "scraper_healthy": False, "diagnostics_ok": False, "failed_probes": []}
    try:
        sample["pipeline_ready"] = runner.http.get(f"{runner.s.base_url}/ready").get("status") == "ready"
    except Exception as exc:  # noqa: BLE001
        sample["ready_error"] = str(exc)
    try:
        scraper = runner.http.get(f"{runner.s.scraper_url}/health")
        sample["scraper_healthy"] = scraper.get("healthy") is True or scraper.get("ok") is True
    except Exception as exc:  # noqa: BLE001
        sample["scraper_error"] = str(exc)
    try:
        diagnostics = runner.http.get(f"{runner.s.base_url}/api/artlist/diagnostics", admin=True)
        ok, failed = runner.diagnostics_ok(diagnostics)
        sample["diagnostics_ok"], sample["failed_probes"] = ok, failed
    except Exception as exc:  # noqa: BLE001
        sample["diagnostics_error"] = str(exc)
    return sample


def start_health_monitor(runner: Any) -> None:
    runner.health_thread = threading.Thread(target=runner.health_loop, name="artlist-health", daemon=True)
    runner.health_thread.start()


def stop_health_monitor(runner: Any) -> None:
    runner.stop_health.set()
    if runner.health_thread is not None:
        runner.health_thread.join(timeout=runner.s.http_timeout + 5)
    runner.write_json("availability/api_health.json", runner.health_samples)
    unhealthy = [sample for sample in runner.health_samples if not (sample.get("pipeline_ready") and sample.get("scraper_healthy") and sample.get("diagnostics_ok"))]
    if unhealthy:
        runner.fail(f"API health monitor observed {len(unhealthy)} unhealthy samples")


def submit_run(runner: Any, term: str, limit: int) -> str:
    payload = {"term": term, "limit": limit, "strategy": "verify", "dry_run": False, "clip_duration": 7, "width": 1920, "height": 1080, "fps": 30, "concurrency": runner.s.clip_concurrency, "root_folder_id": runner.s.root_folder_id}
    response = runner.http.post(f"{runner.s.base_url}/api/artlist/run", payload, admin=True)
    run_id = str(response.get("run_id", "")).strip()
    if not run_id:
        raise RuntimeError(f"Artlist run submission returned no run_id for {term!r}: {response}")
    return run_id


def poll_job(runner: Any, phase: str, index: int, term: str, run_id: str) -> dict[str, Any]:
    start = time.monotonic()
    last: dict[str, Any] = {}
    status = "UNKNOWN"
    for _attempt in range(1, runner.s.poll_max + 1):
        last = runner.http.get(f"{runner.s.base_url}/api/jobs/{run_id}/full", admin=True)
        status = str(last.get("status", "UNKNOWN")).upper()
        if status in TERMINAL_JOB_STATUSES:
            break
        time.sleep(runner.s.poll_interval)
    else:
        status = "TIMEOUT"
    result = {"phase": phase, "keyword_index": index, "term": term, "run_id": run_id, "status": status, "elapsed_ms": round((time.monotonic() - start) * 1000), "job": last}
    runner.write_json(f"{phase}/job_{index:02d}.json", result)
    return result


def run_phase(runner: Any, phase: str, *, limit: int | None = None, terms: list[str] | None = None) -> list[dict[str, Any]]:
    selected_terms = terms or runner.s.keywords
    selected_limit = limit or runner.s.clips_per_keyword
    runner.log(f"{phase}: submitting {len(selected_terms)} jobs ({selected_limit} clips each, clip concurrency={runner.s.clip_concurrency})")
    submissions: list[tuple[int, str, str]] = []
    for index, term in enumerate(selected_terms, start=1):
        try:
            submissions.append((index, term, runner.submit_run(term, selected_limit)))
        except Exception as exc:  # noqa: BLE001
            runner.fail(f"{phase} submit failed for keyword[{index}] {term!r}: {exc}")
    results: list[dict[str, Any]] = []
    if submissions:
        workers = min(runner.s.poll_workers, len(submissions))
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
            future_map = {executor.submit(runner.poll_job, phase, index, term, run_id): (index, term) for index, term, run_id in submissions}
            for future in concurrent.futures.as_completed(future_map):
                index, term = future_map[future]
                try:
                    results.append(future.result())
                except Exception as exc:  # noqa: BLE001
                    runner.fail(f"{phase} poll failed for keyword[{index}] {term!r}: {exc}")
    results.sort(key=lambda item: item["keyword_index"])
    runner.phase_results[phase] = results
    runner.write_json(f"{phase}/statuses.json", [{key: value for key, value in item.items() if key != "job"} for item in results])
    if len(submissions) != len(selected_terms):
        runner.fail(f"{phase} submitted {len(submissions)}/{len(selected_terms)} jobs")
    succeeded = sum(1 for item in results if item["status"] in SUCCESS_JOB_STATUSES)
    if succeeded != len(submissions):
        runner.fail(f"{phase} completed {succeeded}/{len(submissions)} jobs successfully")
    return results


def phase_items(runner: Any, phase: str, expected_per_job: int) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    for result in runner.phase_results.get(phase, []):
        job_result = result.get("job", {}).get("result", {})
        job_items = job_result.get("items", []) if isinstance(job_result, dict) else []
        if not isinstance(job_items, list):
            job_items = []
        if len(job_items) != expected_per_job:
            runner.fail(f"{phase} keyword {result['term']!r} returned {len(job_items)}/{expected_per_job} items")
        for item in job_items:
            if isinstance(item, dict):
                items.append({
                    "term": result["term"],
                    "run_id": result["run_id"],
                    "job_status": result.get("status", "UNKNOWN"),
                    "job_elapsed_ms": result.get("elapsed_ms", 0),
                    "keyword_index": result.get("keyword_index", 0),
                    **item,
                })
    runner.write_json(f"{phase}/items.json", items)
    return items
