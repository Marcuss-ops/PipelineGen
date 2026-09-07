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
    # Floyd Mayweather delivery: 15 interview sources × 12 clips × 5s = 15:00.
    "floyd_interviews_5s": RunnerProfile("floyd_interviews_5s", videos=15, clips_per_video=12, clip_duration_seconds=5),
    # Muhammad Ali delivery: 15 interview sources × 12 clips × 5s = 15:00.
    "ali_interviews_5s": RunnerProfile("ali_interviews_5s", videos=15, clips_per_video=12, clip_duration_seconds=5),
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


