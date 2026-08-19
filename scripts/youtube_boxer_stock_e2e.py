#!/usr/bin/env python3
"""Run and audit the real YouTube stock chain for one boxer.

The runner submits one bounded multi-clip stock job per source, with a
bounded number of source jobs in flight. A green HTTP enqueue is never treated as success:
every job is polled, then SQLite and Drive are checked for the selected
profile's counts and canonical provenance.
"""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
import sqlite3
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, NamedTuple

TERMINAL = {"SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED", "DEAD_LETTERED"}

# ── Contract constants (July 2026) ──────────────────────────────────────
# The full profile produces 20 minutes per boxer. The canary profile is a
# deliberately smaller preflight: one fight source and three clips.
TARGET_VIDEOS = 20
TARGET_CLIPS_PER_VIDEO = 15
CLIP_DURATION_SECONDS = 4
CLIP_DURATION_MIN_SEC = 3.8             # ffprobe tolerance
CLIP_DURATION_MAX_SEC = 4.2
MAX_CONCURRENCY = 4                      # matches the media.stock worker budget


class RunnerProfile(NamedTuple):
    """Immutable profile bounds for one deterministic stock acquisition run."""

    name: str
    videos: int
    clips_per_video: int
    clip_duration_seconds: int = CLIP_DURATION_SECONDS

    @property
    def total_clips(self) -> int:
        return self.videos * self.clips_per_video

    @property
    def target_total_ms(self) -> int:
        return self.total_clips * self.clip_duration_seconds * 1000

    @property
    def total_min_ms(self) -> int:
        return self.total_clips * int((self.clip_duration_seconds - 0.2) * 1000)

    @property
    def total_max_ms(self) -> int:
        return self.total_clips * int((self.clip_duration_seconds + 0.2) * 1000)

    @property
    def per_source_min_ms(self) -> int:
        return self.clips_per_video * int((self.clip_duration_seconds - 0.2) * 1000)

    @property
    def per_source_max_ms(self) -> int:
        return self.clips_per_video * int((self.clip_duration_seconds + 0.2) * 1000)


PROFILES = {
    "canary": RunnerProfile("canary", videos=1, clips_per_video=3),
    "full": RunnerProfile("full", videos=TARGET_VIDEOS, clips_per_video=TARGET_CLIPS_PER_VIDEO),
    "tyson_interviews_5s": RunnerProfile("tyson_interviews_5s", videos=10, clips_per_video=6, clip_duration_seconds=5),
    # Usyk pilot: 15 distinct interview sources, two 30-second sections each
    # (exactly 15 minutes total; the stock API caps a single clip at 30s).
    "usyk_interviews_30s": RunnerProfile("usyk_interviews_30s", videos=15, clips_per_video=2, clip_duration_seconds=30),
    # Final Usyk delivery: 15 interview sources × 12 clips × 5s = 15:00.
    "usyk_interviews_5s": RunnerProfile("usyk_interviews_5s", videos=15, clips_per_video=12, clip_duration_seconds=5),
}

PREFLIGHT_MIN_DURATION_SECONDS = 64.0
PREFLIGHT_REPORT_SCHEMA = "youtube-auth-preflight.v1"
AUTH_REQUIRED_MARKERS = (
    "auth_required",
    "youtube_auth_required",
    "sign in to confirm",
    "confirm you're not a bot",
    "confirm your age",
    "authentication required",
    "login required",
)

QUERIES = {
    "fight": ("{name} fights", "{name} knockout", "{name} highlights", "{name} boxing fight"),
    "interview": ("{name} interview", "{name} press conference", "{name} documentary interview"),
    "training": ("{name} training", "{name} workout", "{name} backstage"),
}

MIKE_TYSON_MANIFEST = (
    ("fight", ("0vnOfawuQF4", "pHXurRbFTss", "jfpUia2gJjg", "CfV8oYVYa_k", "O47EW2WWe28", "2Q-5DCL99JY", "lNPpojcSMgI", "3yYRQIQN8jQ", "isaR1SyVzoE", "tG2B90evafc", "aJ-AUIkBX_Y", "1ee-NU7Lp5Y")),
    ("interview", ("iHaK0M-207o", "LkWU9lB2zEQ", "OOhdx1TLutw", "7MNv4_rTkfU", "NrPWFWd8cVM", "pTrAokYX3CY")),
    ("training", ("ItX74qZf2o0", "K6i9tkWOXhs")),
)

MIKE_TYSON_INTERVIEW_MANIFEST = (
    ("interview", (
        "OOhdx1TLutw", "NrPWFWd8cVM", "GbdRsWmZDZ4", "EMtEuP7fu2M",
        "xmR4qCYF3b8", "CE8OKwvlRkM", "aXG6RDT4wJI", "8PWAWj80JQQ",
        "C_trSpg99yc", "S9MtJ164XJI",
    )),
)

# Keep the pilot's source set deterministic.  Dynamic search is useful for
# discovery, but rerunning it can replace a source with a new candidate and
# silently turn a 20-source run into 21 sources.  This manifest is the set
# already validated for the Ali pilot (13 fight, 5 interview, 2 training).
MUHAMMAD_ALI_MANIFEST = (
    ("fight", ("wjWFnnC7edI", "v9VkFC3SRXI", "pK0CF_CfrD0", "oLA8HIAQEzU", "oJUzl0aFHZw", "eIm2eK5uuVA", "bI-O40Hcnj8", "VFFDe9FQL3s", "RtINcMrdKY0", "I7P0oXNcL_o", "EhGWj-lvg-w", "6kEmuFoEy54", "-3BzkEwUNY8")),
    ("interview", ("lwfMZbkDttg", "R0iAWPDwYvo", "J8ZWZzt0bkQ", "HqiWFLsgVi4", "8CQTVRwi9Fk")),
    ("training", ("V7tfxuocZoM", "75fPUKEP7Ws")),
)

MANNY_PACQUIAO_MANIFEST = (
    ("fight", ("kUruG4y9mak", "VJAk5sy1xoI", "2Esu9upLw88", "Y6FXD5IbLDI", "Sl4V0e2Odqw", "w1BzF2CRC6o", "wgVMjmL2mJk", "ZogCWQST3us", "QddTpJ4aYo8", "CGknwOjzPTM", "bwsv5NrZn6Y", "BVpAM5leGv0")),
    ("interview", ("BKHQS8sj7iw", "yyIzSnJPOXY", "LKfGGQnSuJg", "4jm7XyE3Aa4", "DdtkvPHw6yI", "HzNyry9DNhk")),
    ("training", ("GGsJ9SHlA0o", "wMTdHR2c5ic")),
)

FLOYD_MAYWEATHER_MANIFEST = (
    ("fight", ("Yj3GM2L6nag", "39zhhfMGNRk", "D8y53m388nM", "KYvOC7MBuUw", "Z0qvcHTEPqg", "66Dg_n0H8rQ", "fKiGQfpupRA", "dXq8P_37lMg", "3DpkVOvuA0Y", "tA14uRHqqWs", "Sw51Rjd1BWY", "PnbJWE2wvpg")),
    ("interview", ("1gjZjirv740", "RVc37DH7Sns", "Kxb9AUmSrIA", "1UA6zGsvkrw", "RXw8fJDTb5I", "J6Zert5VaWk")),
    ("training", ("XVU8e6YGTY4", "sNumtUs8d6M")),
)

SUGAR_RAY_ROBINSON_MANIFEST = (
    ("fight", ("-dixu5le9NI", "H4eP1TTedYc", "eobxArm4tDA", "3BBtxCqNFBA", "HmVSxcShBqg", "cOCDmL4F3nM", "4FzA5frXpzI", "QvDCTmK0Naw", "80RUvhi5uaI", "gOdNYYY99GU", "Ey37kbCfozQ", "n_M4SFK8NCc")),
    ("interview", ("naPht4IBx4w", "ohQYcpFSKs0", "Xi-2E5QcXtQ", "4LNQKtq5SEw", "CrAMsMCb2bg", "iuRLVCEdUUo")),
    ("training", ("FQivVOx8SnM", "7D3_UMN97gI")),
)

PREFLIGHT_TARGETS = (
    ("Floyd Mayweather Jr.", FLOYD_MAYWEATHER_MANIFEST[0][1][0]),
    ("Sugar Ray Robinson", SUGAR_RAY_ROBINSON_MANIFEST[0][1][0]),
)

CANONICAL_BOXER_NAMES = {
    "mike tyson": "Mike Tyson",
    "muhammad ali": "Muhammad Ali",
    "manny pacquiao": "Manny Pacquiao",
    "floyd mayweather jr.": "Floyd Mayweather Jr.",
    "sugar ray robinson": "Sugar Ray Robinson",
}


def profile_for(name: str) -> RunnerProfile:
    try:
        return PROFILES[name.casefold()]
    except KeyError as exc:
        raise ValueError(f"unknown runner profile: {name!r}") from exc


def manifest_for_boxer(boxer: str) -> tuple[tuple[str, tuple[str, ...]], ...] | None:
    manifests = {
        "mike tyson": MIKE_TYSON_MANIFEST,
        "manny pacquiao": MANNY_PACQUIAO_MANIFEST,
        "floyd mayweather jr.": FLOYD_MAYWEATHER_MANIFEST,
        "sugar ray robinson": SUGAR_RAY_ROBINSON_MANIFEST,
    }
    return manifests.get(boxer.casefold())


def expected_source_video_ids(boxer: str, profile: RunnerProfile) -> set[str] | None:
    manifest = manifest_for_boxer(boxer)
    if manifest is None:
        return None
    selected: list[str] = []
    for category, ids in manifest:
        if profile.name == "canary" and category != "fight":
            continue
        selected.extend(ids)
    return set(selected[:profile.videos])


def bounded_concurrency(requested: int) -> int:
    """Clamp client fan-out so a caller cannot overload downstream services."""
    return max(1, min(requested, MAX_CONCURRENCY))


def canonical_boxer_name(value: str) -> str:
    normalized = " ".join(value.split()).casefold()
    return CANONICAL_BOXER_NAMES.get(normalized, " ".join(value.split()))


def boxer_slug(value: str) -> str:
    return canonical_boxer_name(value).casefold().replace(".", "").replace(" ", "-")


def http(base: str, token: str, method: str, path: str, body: Any = None, request_id: str = "") -> dict[str, Any]:
    raw = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base.rstrip("/") + path, data=raw, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/json")
    if request_id:
        req.add_header("Idempotency-Key", request_id)
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"HTTP {exc.code} {path}: {exc.read().decode(errors='replace')}") from exc


def build_ytdlp_command(
    target: str,
    *operation_args: str,
    cookies_path: str | None = None,
) -> list[str]:
    """Build every runner yt-dlp command from the shared cookie resolver."""
    command = [os.environ.get("YTDLP_PATH", "yt-dlp")]
    resolved_cookies_path = (
        resolve_youtube_cookies_path() if cookies_path is None else cookies_path.strip()
    )
    if resolved_cookies_path:
        command.extend(("--cookies", resolved_cookies_path))
    command.extend(("--js-runtime", os.environ.get("YT_JS_RUNTIME_PATH", "node"),
                    "--remote-components", "ejs:github", "--no-warnings",
                    "--extractor-args", "youtube:player_client=android_creator"))
    command.extend(operation_args)
    command.append(target)
    return command


def search(_base: str, _token: str, boxer: str, category: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    seen: set[str] = set()
    for template in QUERIES[category]:
        query = template.format(name=boxer)
        command = build_ytdlp_command(
            f"ytsearch10:{query}", "--flat-playlist", "--dump-single-json"
        )
        try:
            raw = subprocess.check_output(command, text=True, timeout=90, stderr=subprocess.DEVNULL)
            entries = json.loads(raw).get("entries", [])
        except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
            raise RuntimeError(f"YouTube search failed for {query!r}: {exc}") from exc
        for item in entries:
            video_id = str(item.get("id") or "")
            duration = float(item.get("duration", 0) or 0)
            if not video_id or video_id in seen or duration < 64:
                continue
            seen.add(video_id)
            url = f"https://www.youtube.com/watch?v={video_id}"
            out.append({"video_id": video_id, "url": url, "title": item.get("title", ""),
                        "channel": item.get("channel", item.get("uploader", "")),
                        "duration": duration, "category": category})
    return out


def describe(video_id: str, category: str) -> dict[str, Any]:
    command = build_ytdlp_command(
        f"https://www.youtube.com/watch?v={video_id}",
        "--dump-single-json", "--skip-download"
    )
    try:
        item = json.loads(subprocess.check_output(command, text=True, timeout=30, stderr=subprocess.DEVNULL))
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"YouTube manifest video {video_id} unavailable: {exc}") from exc
    duration = float(item.get("duration", 0) or 0)
    if duration < PREFLIGHT_MIN_DURATION_SECONDS:
        raise RuntimeError(f"YouTube manifest video {video_id} is too short: {duration}s")
    return {"video_id": video_id, "url": f"https://www.youtube.com/watch?v={video_id}",
            "title": item.get("title", ""), "channel": item.get("channel", item.get("uploader", "")),
            "duration": duration, "category": category}


def resolve_youtube_cookies_path(environ: dict[str, str] | None = None) -> str:
    """Resolve the cookie path without reading or exposing the cookie file.

    The canonical deployment variable wins. The legacy variable remains a
    migration bridge; unset configuration stays empty so authentication
    failures remain visible instead of targeting a local repository file.
    """
    environment = os.environ if environ is None else environ
    return (
        environment.get("VELOX_YOUTUBE_COOKIES_FILE", "").strip()
        or environment.get("YT_COOKIES_PATH", "").strip()
        or ""
    )


def build_preflight_command(video_id: str, cookies_path: str) -> list[str]:
    """Build a probe using the explicit resolved path without reporting it."""
    return build_ytdlp_command(
        f"https://www.youtube.com/watch?v={video_id}",
        "--dump-single-json", "--skip-download",
        cookies_path=cookies_path,
    )


def _contains_auth_required(output: str) -> bool:
    normalized = output.casefold()
    return any(marker in normalized for marker in AUTH_REQUIRED_MARKERS)


def run_preflight_probe(
    boxer: str,
    video_id: str,
    cookies_path: str,
    *,
    runner: Any = subprocess.run,
    timeout: int = 90,
) -> dict[str, Any]:
    """Probe one manifest video and return only sanitized, typed results."""
    result: dict[str, Any] = {
        "boxer": boxer,
        "video_id": video_id,
        "available": False,
        "auth_required": False,
        "duration_seconds": None,
        "duration_check": "FAIL",
        "status": "FAIL",
        "error_code": None,
    }
    if not os.path.isfile(cookies_path) or not os.access(cookies_path, os.R_OK):
        result["error_code"] = "COOKIE_FILE_UNAVAILABLE"
        return result

    try:
        completed = runner(
            build_preflight_command(video_id, cookies_path),
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        result["error_code"] = "YT_DLP_TIMEOUT"
        return result
    except OSError:
        result["error_code"] = "YT_DLP_UNAVAILABLE"
        return result

    combined_output = f"{completed.stdout or ''}\n{completed.stderr or ''}"
    if _contains_auth_required(combined_output):
        result["auth_required"] = True
        result["error_code"] = "AUTH_REQUIRED"
        return result
    if completed.returncode != 0:
        result["error_code"] = "YT_DLP_FAILED"
        return result

    try:
        item = json.loads(completed.stdout or "")
        duration = float(item.get("duration", 0) or 0)
    except (TypeError, ValueError, json.JSONDecodeError):
        result["error_code"] = "INVALID_METADATA"
        return result

    result["available"] = True
    result["duration_seconds"] = duration
    if duration < PREFLIGHT_MIN_DURATION_SECONDS:
        result["error_code"] = "DURATION_TOO_SHORT"
        return result
    result["duration_check"] = "PASS"
    result["status"] = "PASS"
    return result


def run_auth_preflight(
    *,
    cookies_path: str | None = None,
    runner: Any = subprocess.run,
) -> dict[str, Any]:
    """Run the two real manifest probes and return a sanitized JSON report."""
    resolved_path = cookies_path or resolve_youtube_cookies_path()
    probes = [
        run_preflight_probe(boxer, video_id, resolved_path, runner=runner)
        for boxer, video_id in PREFLIGHT_TARGETS
    ]
    passed = all(probe["status"] == "PASS" for probe in probes)
    return {
        "schema_version": PREFLIGHT_REPORT_SCHEMA,
        "youtube_auth": "PASS" if passed else "FAIL",
        "cookie_file_configured": bool(resolved_path),
        "cookie_file_readable": os.path.isfile(resolved_path) and os.access(resolved_path, os.R_OK),
        "floyd_manifest_probe": probes[0]["status"],
        "sugar_ray_manifest_probe": probes[1]["status"],
        "probes": probes,
    }


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


def cleanup_stale_staging(root: Path, ttl_seconds: int = 1800) -> int:
    """Remove only old pipeline partials; never touch completed media or databases."""
    import shutil
    now = time.time()
    removed = 0
    partial_root = root / "stock_pipeline_staging"
    if partial_root.is_dir():
        for item in partial_root.iterdir():
            if item.is_file() and (item.name.endswith(".part") or ".partial." in item.name or item.name.endswith(".tmp")) and now - item.stat().st_mtime > ttl_seconds:
                item.unlink()
                removed += 1
    for item in root.glob("stock_stage_*"):
        if item.is_dir() and now - item.stat().st_mtime > ttl_seconds:
            shutil.rmtree(item)
            removed += 1
    if removed:
        print(f"staging_cleanup=removed:{removed} ttl_seconds={ttl_seconds}")
    return removed


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
    duration = int(video["duration"])
    # Non-overlapping windows distributed over the source, avoiding the
    # first/last few seconds where intros/outros are commonly black.
    usable = max(60, duration - 8)
    step = max(profile.clip_duration_seconds, usable // (profile.clips_per_video + 1))
    def stamp(total_seconds: int) -> str:
        return f"{total_seconds // 60:02d}:{total_seconds % 60:02d}"
    return [{"start": stamp(start),
             "end": stamp(start + profile.clip_duration_seconds),
             "name": video.get("title", ""),
             "source_title": video.get("title", ""),
             "source_channel": video.get("channel", ""),
             "category": video["category"],
             "description": f"{video['category']} scene featuring {video.get('title') or video['video_id']}"}
            for i in range(profile.clips_per_video)
            for start in [8 + i * step]]


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


def run_source(
    base: str,
    token: str,
    db: Path,
    boxer: str,
    folder_id: str,
    video: dict[str, Any],
    index: int,
    profile: RunnerProfile | None = None,
) -> None:
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
            "folder_name": boxer,
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
            break
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
    scope = "source='youtube' AND lifecycle_state='ACTIVE'"
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


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("boxer", nargs="?")
    ap.add_argument("--preflight-auth", action="store_true")
    ap.add_argument("--preflight-report", default="out/01_youtube_auth_preflight.json")
    ap.add_argument("--base", default=os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000"))
    ap.add_argument("--db", default="data/media/media.db.sqlite")
    ap.add_argument("--root-id", default=os.environ.get("YOUTUBE_STOCK_ROOT_ID", ""))
    ap.add_argument("--folder-id", default="")
    ap.add_argument("--verify-only", action="store_true")
    ap.add_argument("--profile", choices=sorted(PROFILES), default="full")
    ap.add_argument("--concurrency", type=int, default=MAX_CONCURRENCY)
    ap.add_argument("--manifest", default="", help="persisted source manifest; reused unless --refresh-manifest is set")
    ap.add_argument("--refresh-manifest", action="store_true")
    ap.add_argument("--staging-ttl-seconds", type=int, default=1800)
    args = ap.parse_args()
    if args.preflight_auth:
        report = run_auth_preflight()
        write_auth_preflight_report(report, args.preflight_report)
        return 0 if report["youtube_auth"] == "PASS" else 1
    if not args.boxer:
        ap.error("boxer is required unless --preflight-auth is used")
    profile = profile_for(args.profile)
    # Keep the display name used for Drive/folder_path separate from the
    # deterministic slug used in idempotency keys and reports.
    args.boxer = canonical_boxer_name(args.boxer)
    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    os.environ["VELOX_STOCK_RUN_ID"] = run_id
    manifest_path = Path(args.manifest) if args.manifest else Path("out") / boxer_slug(args.boxer) / f"{profile.name}.manifest.json"
    cleanup_stale_staging(Path("data/tmp"), max(60, args.staging_ttl_seconds))
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise SystemExit("VELOX_ADMIN_TOKEN is required")
    if args.verify_only:
        target = args.folder_id
        if not target:
            raise SystemExit("--folder-id is required with --verify-only")
        expected_sources = expected_source_video_ids(args.boxer, profile)
        if expected_sources is None:
            raise SystemExit(
                "--verify-only requires a static manifest for the selected boxer; "
                "run acquisition first or provide a persisted run manifest"
            )
        verify(
            Path(args.db),
            target,
            args.boxer,
            profile,
            expected_source_video_ids=expected_sources,
        )
        return 0
    selected = select(args.base, token, args.boxer, profile, manifest_path, args.refresh_manifest)
    write_manifest(manifest_path, args.boxer, profile, selected)
    if args.folder_id:
        target = args.folder_id
        upload_parent = target
    elif args.root_id:
        target = folder(args.base, token, args.root_id, args.boxer)
        # The stock endpoint owns creation/reuse of the boxer folder. Pass the
        # explicit stock parent so it does not create Mike Tyson/Mike Tyson.
        upload_parent = args.root_id
    else:
        target = resolve_boxe_folder(args.base, token)
        upload_parent = target
    if not target:
        raise SystemExit("could not resolve/create boxer Drive folder")
    print(
        f"selected={len(selected)} profile={profile.name} "
        f"clips={profile.total_clips} boxer={args.boxer} slug={boxer_slug(args.boxer)}"
    )
    # The server-side worker pool is independently bounded. Keep this
    # client fan-out at or below the safe limit for YouTube, Drive, FFmpeg,
    # and SQLite, even when a caller requests a larger value.
    workers = bounded_concurrency(args.concurrency)
    with ThreadPoolExecutor(max_workers=workers, thread_name_prefix="youtube-stock") as pool:
        futures = [pool.submit(run_source, args.base, token, Path(args.db), args.boxer, upload_parent, video, index, profile)
                   for index, video in enumerate(selected, 1)]
        for future in as_completed(futures):
            future.result()
    verify(
        Path(args.db),
        target,
        args.boxer,
        profile,
        expected_source_video_ids={video["video_id"] for video in selected},
        expected_run_id=run_id,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
