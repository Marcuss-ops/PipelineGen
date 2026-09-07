def write_auth_preflight_report(report: dict[str, Any], destination: str) -> None:
    """Write the already-sanitized report to stdout or an atomic JSON file."""
    encoded = json.dumps(report, indent=2, sort_keys=True)
    if destination == "--stdout":
        print(encoded)
        return
    target = Path(destination)
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_name(f".{target.name}.tmp")
    temporary.write_text(encoded + "\n", encoding="utf-8")
    temporary.replace(target)


def _select_manifest(
    manifest: tuple[tuple[str, tuple[str, ...]], ...],
    profile: RunnerProfile,
    label: str,
) -> list[dict[str, Any]]:
    manifest_videos = [
        (category, video_id)
        for category, ids in manifest
        for video_id in ids
    ]
    if profile.name == "canary":
        manifest_videos = [
            (category, video_id)
            for category, video_id in manifest_videos
            if category == "fight"
        ][:profile.videos]
    # Manifest validation is read-only and independent per source. Keep it
    # bounded so a transient YouTube stall on one video cannot serialize the
    # entire 20-video preflight behind repeated 90-second timeouts.
    with ThreadPoolExecutor(max_workers=min(4, len(manifest_videos))) as pool:
        futures = [pool.submit(describe, video_id, category) for category, video_id in manifest_videos]
        selected = [future.result() for future in futures]
    if len(selected) != profile.videos or len({item["video_id"] for item in selected}) != profile.videos:
        raise RuntimeError(f"{label} manifest is not exactly {profile.videos} unique videos for {profile.name}")
    return selected


def select(
    base: str,
    token: str,
    boxer: str,
    profile: RunnerProfile | None = None,
    manifest_path: Path | None = None,
    refresh_manifest: bool = False,
) -> list[dict[str, Any]]:
    profile = profile or PROFILES["full"]
    if manifest_path and manifest_path.exists() and not refresh_manifest:
        try:
            persisted = json.loads(manifest_path.read_text(encoding="utf-8"))
            selected = persisted.get("videos", persisted) if isinstance(persisted, dict) else persisted
            if isinstance(selected, list) and len(selected) == profile.videos and len({item.get("video_id") for item in selected}) == profile.videos:
                print(f"manifest=LOCKED path={manifest_path} videos={len(selected)}")
                return selected
        except (OSError, ValueError, TypeError):
            pass
    if boxer.casefold() == "usyk" and profile.name in {"usyk_interviews_30s", "usyk_interviews_5s"}:
        candidates = search(base, token, boxer, "interview")
        if len(candidates) < profile.videos:
            raise RuntimeError(f"interview: only {len(candidates)} usable candidates, need {profile.videos}")
        selected = candidates[:profile.videos]
        if len({item["video_id"] for item in selected}) != profile.videos:
            raise RuntimeError("Usyk interview selection is not exactly 15 unique videos")
        return selected
    if boxer.casefold() in {"floyd mayweather jr.", "muhammad ali"} and profile.name in {"floyd_interviews_5s", "ali_interviews_5s"}:
        candidates = search(base, token, boxer, "interview")
        if len(candidates) < profile.videos:
            raise RuntimeError(f"interview: only {len(candidates)} usable candidates, need {profile.videos}")
        selected = candidates[:profile.videos]
        if len({item["video_id"] for item in selected}) != profile.videos:
            raise RuntimeError(f"{boxer} interview selection is not exactly 15 unique videos")
        return selected
    if boxer.casefold() == "mike tyson":
        if profile.name == "tyson_interviews_5s":
            return _select_manifest(MIKE_TYSON_INTERVIEW_MANIFEST, profile, "Mike Tyson interviews")
        return _select_manifest(MIKE_TYSON_MANIFEST, profile, "Mike Tyson")
    if boxer.casefold() == "muhammad ali":
        # July 2026: yt-dlp --dump-single-json is blocked by YouTube
        # anti-bot for these videos, but --flat-playlist search still
        # works.  Fall through to the dynamic search path below so the
        # manifest is rebuilt from live YouTube results every run.
        pass
    if boxer.casefold() == "manny pacquiao":
        return _select_manifest(MANNY_PACQUIAO_MANIFEST, profile, "Manny Pacquiao")
    if boxer.casefold() == "floyd mayweather jr.":
        return _select_manifest(FLOYD_MAYWEATHER_MANIFEST, profile, "Floyd Mayweather Jr.")
    if boxer.casefold() == "sugar ray robinson":
        return _select_manifest(SUGAR_RAY_ROBINSON_MANIFEST, profile, "Sugar Ray Robinson")
    selected: list[dict[str, Any]] = []
    wanted_by_profile = (("fight", 1),) if profile.name == "canary" else (("fight", 12), ("interview", 6), ("training", 2))
    for category, wanted in wanted_by_profile:
        candidates = search(base, token, boxer, category)
        if len(candidates) < wanted:
            raise RuntimeError(f"{category}: only {len(candidates)} usable candidates, need {wanted}")
        selected.extend(candidates[:wanted])
    if len(selected) != profile.videos or len({item["video_id"] for item in selected}) != profile.videos:
        raise RuntimeError(f"selection is not exactly {profile.videos} unique videos")
    return selected


def write_manifest(path: Path, boxer: str, profile: RunnerProfile, videos: list[dict[str, Any]]) -> None:
    """Persist source selection atomically so retries cannot rediscover different videos."""
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema": "velox.youtube-stock-manifest.v1",
        "boxer": boxer,
        "profile": profile.name,
        "videos": videos,
        "locked_at": datetime.now(timezone.utc).isoformat(),
    }
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)
    print(f"manifest=LOCKED path={path} videos={len(videos)}")


def cleanup_stale_staging(root: Path, ttl_seconds: int = 1800, max_bytes: int = 0) -> int:
    """Remove stale partials and enforce an optional bounded staging quota."""
    import shutil
    now = time.time()
    removed = 0
    candidates: list[Path] = []
    partial_root = root / "stock_pipeline_staging"
    if partial_root.is_dir():
        for item in partial_root.iterdir():
            if item.is_file() and (item.name.endswith(".part") or ".partial." in item.name or item.name.endswith(".tmp")):
                if now - item.stat().st_mtime > ttl_seconds:
                    candidates.append(item)
    for item in root.glob("stock_stage_*"):
        if item.is_dir() and now - item.stat().st_mtime > ttl_seconds:
            shutil.rmtree(item)
            removed += 1
    if max_bytes > 0 and partial_root.is_dir():
        # Only evict files already outside the TTL. Active .part files are
        # never deleted by the quota guard while a download may still own
        # them.
        live = [p for p in candidates if p.exists()]
        total = sum(p.stat().st_size for p in live)
        for item in sorted(live, key=lambda p: p.stat().st_mtime):
            size = item.stat().st_size
            if total > max_bytes:
                item.unlink(missing_ok=True)
                total -= size
                removed += 1
    for item in candidates:
        if item.exists():
            item.unlink(missing_ok=True)
            removed += 1
    if removed:
        print(f"staging_cleanup=removed:{removed} ttl_seconds={ttl_seconds} max_bytes={max_bytes}")
    return removed


def write_timing_report(path: Path, boxer: str, profile: RunnerProfile, records: list[dict[str, Any]]) -> None:
    """Persist per-source timings and p50/p95 bottleneck summaries atomically."""
    if not records:
        return
    path.parent.mkdir(parents=True, exist_ok=True)

    def percentile(values: list[int], p: float) -> float:
        return round(statistics.quantiles(values, n=100, method="inclusive")[int(p) - 1], 1) if len(values) > 1 else float(values[0])

    fields = ("wall_ms", "download_ms", "extract_ms", "drive_ms")
    summary: dict[str, Any] = {}
    for field in fields:
        values = [int(record.get(field) or 0) for record in records]
        summary[field] = {"p50": percentile(values, 50), "p95": percentile(values, 95), "max": max(values)}
    bottleneck = max(fields, key=lambda field: summary[field]["p95"])
    payload = {
        "schema": "velox.youtube-stock-timings.v1",
        "boxer": boxer,
        "profile": profile.name,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "sources": records,
        "summary": summary,
        "p95_bottleneck": bottleneck,
    }
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)
    print(f"timings_report={path} p95_bottleneck={bottleneck}")


def folder(base: str, token: str, root_id: str, boxer: str) -> str:
    listing = http(base, token, "GET", f"/api/drive/files?folder_id={urllib.parse.quote(root_id)}")
    matches = [f for f in listing.get("files", []) if f.get("mime_type") == "application/vnd.google-apps.folder" and f.get("name", "").casefold() == boxer.casefold()]
    if len(matches) > 1:
        raise RuntimeError(f"multiple Drive folders named {boxer!r} under root")
    if matches:
        return str(matches[0]["id"])
    created = http(base, token, "POST", "/api/drive/folders", {"parent_id": root_id, "folders": [boxer]})
    return str(created.get("created", {}).get(boxer, ""))


def resolve_boxe_folder(base: str, token: str) -> str:
    """Resolve Boxe through the canonical alias resolver and publisher canary."""
    result = http(base, token, "POST", "/api/drive/canary-upload", {"folder_alias": "Boxe"})
    folder_id = str(result.get("folder_id") or "")
    if not result.get("ok") or not folder_id:
        raise RuntimeError("Drive canary alias resolution for Boxe failed")
    return folder_id


def segments(video: dict[str, Any], profile: RunnerProfile | None = None) -> list[dict[str, Any]]:
    profile = profile or PROFILES["full"]
    duration = max(0, int(float(video["duration"])))
    clip_seconds = profile.clip_duration_seconds
    max_start = max(0, duration - clip_seconds)
    # Prefer skipping the first eight seconds, but fall back to the full
    # source for short interviews. The old fixed-step formula could produce
    # EndSec beyond the probed duration (e.g. a 65s source yielded 68s).
    first_start = 8 if max_start >= 8 else 0
    if profile.clips_per_video == 1:
        starts = [first_start]
    else:
        span = max_start - first_start
        starts = [first_start + (span * i) // (profile.clips_per_video - 1)
                  for i in range(profile.clips_per_video)]
    def stamp(total_seconds: int) -> str:
        return f"{total_seconds // 60:02d}:{total_seconds % 60:02d}"
    return [{"start": stamp(start),
             "end": stamp(min(start + clip_seconds, duration)),
             "name": video.get("title", ""),
             "source_title": video.get("title", ""),
             "source_channel": video.get("channel", ""),
             "category": video["category"],
             "description": f"{video['category']} scene featuring {video.get('title') or video['video_id']}"}
            for start in starts]


def latest_job(db: Path, video: dict[str, Any], segment: dict[str, Any], submitted_at: str) -> str:
    with sqlite3.connect(db) as conn:
        row = conn.execute(
            "SELECT id FROM jobs WHERE type='youtube_clip.extract' AND created_at>=? AND payload_json LIKE ? AND payload_json LIKE ? ORDER BY created_at DESC LIMIT 1",
            (submitted_at, f"%{video['video_id']}%", f"%{segment['start']}%"),
        ).fetchone()
    if not row:
        raise RuntimeError(f"enqueue returned no correlated YouTube job for {video['video_id']} {segment['start']}")
    return str(row[0])


def clip_id(video_id: str, segment: dict[str, Any]) -> str:
    def seconds(value: str) -> int:
        parts = [int(part) for part in value.split(":")]
        return parts[-1] + (parts[-2] * 60 if len(parts) > 1 else 0)
    return f"yt_{video_id}_{seconds(segment['start'])}_{seconds(segment['end'])}_v1"


def wait_asset(db: Path, asset_id: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        with sqlite3.connect(db) as conn:
            row = conn.execute("SELECT drive_file_id, file_hash, duration_ms FROM media_assets WHERE id=?", (asset_id,)).fetchone()
        if row and row[0] and row[1] and row[2] and row[2] > 0:
            return
        time.sleep(3)
    raise RuntimeError(f"job reported success but asset {asset_id} was not durably persisted")


def asset_complete(db: Path, asset_id: str) -> bool:
    with sqlite3.connect(db) as conn:
        row = conn.execute("""
          SELECT drive_file_id, file_hash, duration_ms,
                 json_extract(metadata_json, '$.source_provider'),
                 json_extract(metadata_json, '$.video_id'),
                 json_extract(metadata_json, '$.source_title'),
                 json_extract(metadata_json, '$.source_channel')
          FROM media_assets WHERE id=? AND lifecycle_state='ACTIVE'
        """, (asset_id,)).fetchone()
    return bool(row and row[0] and row[1] and row[2] and row[2] > 0 and all(row[3:]))


