def run_source(
    base: str,
    token: str,
    db: Path,
    boxer: str,
    folder_id: str,
    video: dict[str, Any],
    index: int,
    profile: RunnerProfile | None = None,
    destination_name: str | None = None,
) -> dict[str, Any]:
    profile = profile or PROFILES["full"]
    run_id = os.environ.get("VELOX_STOCK_RUN_ID") or datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    source_url = video["url"]
    clip_specs = []
    for clip_index, segment in enumerate(segments(video, profile), 1):
        def seconds(value: str) -> int:
            parts = [int(part) for part in value.split(":")]
            return parts[-1] + (parts[-2] * 60 if len(parts) > 1 else 0)
        clip_specs.append({
            "title": video.get("title", video["video_id"]),
            "description": segment["description"],
            "url": source_url,
            "start_sec": seconds(segment["start"]),
            "end_sec": seconds(segment["end"]),
            "category": video["category"],
            "tags": [boxer, video["category"]],
            "slug": f"{run_id}-{index:02d}-{video['video_id']}-{clip_index:02d}",
        })
    payload = {
            # Explicit clip specs carry the source URL and time windows. Do
            # not also send direct_urls: that makes the stock planner stage a
            # full 1080p source before extracting the sections, defeating the
            # sections_only download mode and making short clips needlessly
            # expensive.
            "clips": clip_specs,
            "total_minutes": 1,
            "target_total_duration_seconds": profile.clips_per_video * profile.clip_duration_seconds,
            "target_duration_per_source_seconds": profile.clips_per_video * profile.clip_duration_seconds,
            "clips_per_source": profile.clips_per_video,
            "clip_duration_seconds": profile.clip_duration_seconds,
            "download_mode": "sections_only",
            "clip_duration": profile.clip_duration_seconds,
            "folder_name": destination_name or boxer,
            "drive_folder_id": folder_id,
            "subfolder": video["category"],
            "metadata": {
                "title": video.get("title", video["video_id"]),
                "description": f"{video['category']} stock for {boxer}.",
                "category": "Boxe",
                "tags": [boxer, video["category"]],
            },
            "async": True,
            "no_effects": True,
            "no_transitions": True,
        }
    request_id = f"youtube-stock-{run_id}-{boxer_slug(boxer)}-{index:02d}-{video['video_id']}"
    accepted = http(base, token, "POST", "/api/stock-pipeline/run", payload, request_id)
    job_id = str(accepted.get("job_id") or accepted.get("run_id") or "")
    if not job_id:
        raise RuntimeError(f"stock pipeline source {video['video_id']} returned no job_id")
    deadline = time.monotonic() + 1800
    while time.monotonic() < deadline:
        result = http(base, token, "GET", f"/api/jobs/{job_id}/full")
        state = str(result.get("status", result.get("state", ""))).upper()
        if state in TERMINAL:
            if state not in {"SUCCEEDED", "COMPLETED"}:
                raise RuntimeError(f"stock pipeline {job_id} ended {state}: {result.get('error', '')}")
            timing = result.get("timing") or {}
            stages = {
                str(stage.get("name")): int(stage.get("duration_ms") or 0)
                for stage in timing.get("stages", [])
                if isinstance(stage, dict)
            }
            drive_ms = sum(
                int(operation.get("work_ms") or 0)
                for operation in timing.get("operations", [])
                if isinstance(operation, dict) and operation.get("component") == "drive"
            )
            wall_ms = int(timing.get("wall_ms") or 0)
            timing_text = (
                f"wall={wall_ms / 1000:.1f}s"
                f" download={stages.get('stock.youtube_download', 0) / 1000:.1f}s"
                f" extract={stages.get('stock.extract', 0) / 1000:.1f}s"
                f" drive={drive_ms / 1000:.1f}s"
            )
            print(f"[{index:02d}/{profile.videos}] {video['category']:<9} {video['video_id']} clips {profile.clips_per_video} SUCCEEDED {timing_text}")
            return {
                "video_id": video["video_id"],
                "category": video["category"],
                "wall_ms": wall_ms,
                "download_ms": stages.get("stock.youtube_download", 0),
                "extract_ms": stages.get("stock.extract", 0),
                "drive_ms": drive_ms,
                "job_id": job_id,
            }
        time.sleep(5)
    else:
        raise RuntimeError(f"timeout waiting for stock pipeline {job_id}")


def verify(
    db: Path,
    folder_id: str,
    boxer: str,
    profile: RunnerProfile | None = None,
    expected_source_video_ids: set[str] | None = None,
    wait_for_index_seconds: int = 180,
    expected_run_id: str | None = None,
) -> None:
    profile = profile or PROFILES["full"]
    # The canonical stock publisher persists source=stock and terminal
    # lifecycle_state=PUBLISHED. Accept ACTIVE as a compatibility state for
    # older runs, but never mix in other providers or transient rows.
    scope = "source='stock' AND lifecycle_state IN ('ACTIVE','PUBLISHED')"
    scope_params: list[Any] = []
    if expected_source_video_ids is not None:
        if not expected_source_video_ids:
            raise RuntimeError("verification requires at least one expected source video ID")
        placeholders = ",".join("?" for _ in expected_source_video_ids)
        scope += f" AND source_video_id IN ({placeholders})"
        scope_params.extend(sorted(expected_source_video_ids))
    if expected_run_id:
        scope += " AND json_extract(metadata_json, '$.slug') LIKE ?"
        scope_params.append(f"{expected_run_id}-%")
    params = tuple(scope_params)
    with sqlite3.connect(db) as conn:
        row = conn.execute(
            f"""SELECT COUNT(*), COUNT(DISTINCT source_video_id), COALESCE(SUM(duration_ms),0),
                 COUNT(DISTINCT file_hash), SUM(CASE WHEN drive_file_id='' THEN 1 ELSE 0 END),
                 SUM(CASE WHEN source_video_id='' OR source_url='' OR category='' OR duration_ms<=0 THEN 1 ELSE 0 END),
                 SUM(CASE WHEN index_state != 'INDEXED' THEN 1 ELSE 0 END)
          FROM media_assets WHERE {scope}""",
            params,
        ).fetchone()
        per_source = conn.execute(
            f"SELECT source_video_id, COUNT(*) FROM media_assets WHERE {scope} GROUP BY source_video_id",
            params,
        ).fetchall()
        per_dur = conn.execute(
            f"""SELECT source_video_id, COUNT(*) AS clip_count, SUM(duration_ms) AS total_duration_ms
              FROM media_assets WHERE {scope}
              GROUP BY source_video_id
              HAVING COUNT(*) != ? OR SUM(duration_ms) < ? OR SUM(duration_ms) > ?""",
            params + (profile.clips_per_video, profile.per_source_min_ms, profile.per_source_max_ms),
        ).fetchall()
    count, videos, duration, hashes, missing_drive, incomplete, missing_index = row
    if missing_index and wait_for_index_seconds > 0:
        time.sleep(5)
        return verify(db, folder_id, boxer, profile, expected_source_video_ids, wait_for_index_seconds - 5, expected_run_id)
    expected = profile.total_clips
    if (
        count != expected
        or videos != profile.videos
        or not profile.total_min_ms <= duration <= profile.total_max_ms
        or hashes != expected
        or missing_drive
        or incomplete
        or missing_index
    ):
        raise RuntimeError(
            f"SQLite gate failed: clips={count}, videos={videos}, duration_ms={duration}, "
            f"expected_duration_ms={profile.total_min_ms}..{profile.total_max_ms}, "
            f"hashes={hashes}, missing_drive={missing_drive}, incomplete={incomplete}, missing_index={missing_index}"
        )
    if any(n > profile.clips_per_video for _, n in per_source):
        raise RuntimeError(f"more than {profile.clips_per_video} clips found for a source video")
    if per_dur:
        offenders = [f"{r[0]} clips={r[1]} dur_ms={r[2]}" for r in per_dur]
        raise RuntimeError(f"per-source duration gate failed ({len(offenders)} sources): {'; '.join(offenders[:5])}")
    # Local media is deliberately removed after canonical Drive publication.
    # The durable physical gate is therefore the SQLite/Drive contract above:
    # ACTIVE + INDEXED + non-empty Drive identity/link + valid duration/hash.
    print(
        f"SQLite gate: {profile.total_clips} clips, {profile.videos} videos, "
        f"{profile.target_total_ms // 1000} nominal seconds, complete provenance"
    )


