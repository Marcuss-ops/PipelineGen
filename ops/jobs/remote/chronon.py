#!/usr/bin/env python3
"""chronon.py — client portabile per il catalogo media di PipelineGen.

Legge dal master gli asset della media SSOT (metadati, Drive link,
trascrizioni, info vettoriali), cerca nel catalogo, scarica i file e
invia/monitora job. Usa solo la libreria standard di Python 3: non serve
installare nulla sul computer remoto e non serve copiare il database.

La credenziale è la chiave M2M creata sul master con:

    ops/jobs/remote/provision-key.sh --client-id <pc> --master-url <url>

Esempi (i flag comuni vanno dopo il sottocomando):

    python3 chronon.py facets   --env ~/pc-casa-01.env
    python3 chronon.py assets   --env ~/pc-casa-01.env --source youtube --limit 20
    python3 chronon.py asset    yt_xxx --env ~/pc-casa-01.env
    python3 chronon.py transcript yt_xxx --lang it --env ~/pc-casa-01.env
    python3 chronon.py download yt_xxx -o clip.mp4 --env ~/pc-casa-01.env
    python3 chronon.py download yt_xxx -o clip.mp4 --resume --env ~/pc-casa-01.env
    python3 chronon.py search   "elon musk" --env ~/pc-casa-01.env
    python3 chronon.py submit   --type clip.render --payload job.json --key progetto-01 --env ~/pc-casa-01.env
    python3 chronon.py job      job_abcd1234 --env ~/pc-casa-01.env
    python3 chronon.py verify   --env ~/pc-casa-01.env

Il file env contiene VELOX_MASTER_URL e VELOX_M2M_SECRET (mode 0600).
La chiave deve avere gli scope jobs.submit, jobs.read, media.read.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

DEFAULT_TIMEOUT = 60
DEFAULT_DOWNLOAD_TIMEOUT = 600
DEFAULT_RETRIES = 3
RETRYABLE_HTTP_STATUS = {408, 425, 429, 500, 502, 503, 504}
# Sources served by the canonical clip download route. The generic asset
# listing is ordered by updated_at and can be dominated by images/scripts, so
# the verifier asks these explicitly before giving up on an automatic download.
DOWNLOAD_SOURCES = (
    "youtube",
    "voiceover",
    "stock",
    "clip_drive",
    "bgm",
    "sound_effect",
    "clip.render",
)


def die(message: str, code: int = 2) -> None:
    print(f"chronon: {message}", file=sys.stderr)
    raise SystemExit(code)


def load_env(path: str) -> dict[str, str]:
    """Read a KEY=VALUE env file without executing it."""
    values: dict[str, str] = {}
    with open(path, "r", encoding="utf-8") as handle:
        for raw in handle:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("export "):
                line = line[len("export "):].strip()
            if "=" not in line:
                continue
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def with_query(path: str, params: dict[str, object]) -> str:
    clean = {key: value for key, value in params.items() if value not in (None, "", False)}
    if not clean:
        return path
    return path + "?" + urllib.parse.urlencode(clean)


class Api:
    def __init__(self, base_url: str, secret: str, timeout: int = DEFAULT_TIMEOUT):
        self.base_url = base_url.rstrip("/")
        self.secret = secret
        self.timeout = timeout

    def _open(self, method: str, path: str, body: object | None = None, idem_key: str = ""):
        data = None
        headers = {
            "Authorization": f"Bearer {self.secret}",
            "Accept": "application/json",
        }
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if idem_key:
            headers["Idempotency-Key"] = idem_key
        request = urllib.request.Request(
            self.base_url + path, data=data, headers=headers, method=method
        )
        try:
            return urllib.request.urlopen(request, timeout=self.timeout)
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", "replace")[:2000]
            detail = raw
            try:
                parsed = json.loads(raw)
                detail = parsed.get("error") or parsed.get("message") or raw
            except Exception:
                pass
            die(f"HTTP {exc.code} su {method} {path}: {detail}", 1)
        except urllib.error.URLError as exc:
            die(f"master non raggiungibile ({self.base_url}): {exc.reason}", 1)

    def get_json(self, path: str) -> object:
        with self._open("GET", path) as response:
            raw = response.read()
        return json.loads(raw) if raw else {}

    def post_json(self, path: str, body: object, idem_key: str = "") -> object:
        with self._open("POST", path, body=body, idem_key=idem_key) as response:
            raw = response.read()
        return json.loads(raw) if raw else {}

    def post_stream(self, path: str):
        return self._open("POST", path, body=None)

    def post_stream_raw(self, path: str, extra_headers: dict[str, str] | None = None):
        """Open a POST stream and let HTTP/network errors propagate.

        The download path needs the status and headers (206/Range) and its own
        retry policy, so it must not go through _open(), which exits on the
        first HTTP error.
        """
        headers = {
            "Authorization": f"Bearer {self.secret}",
            "Accept": "application/octet-stream",
        }
        if extra_headers:
            headers.update(extra_headers)
        request = urllib.request.Request(
            self.base_url + path, data=None, headers=headers, method="POST"
        )
        return urllib.request.urlopen(request, timeout=self.timeout)

    def probe(self, method: str, path: str, raw_body: bytes = b"") -> tuple[int, bytes]:
        """Non-fatal request used by `verify`: return status + body instead of exiting."""
        headers = {
            "Authorization": f"Bearer {self.secret}",
            "Accept": "application/json",
        }
        data = raw_body or None
        if data is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(
            self.base_url + path, data=data, headers=headers, method=method
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return int(response.getcode() or 200), response.read()
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read()
        except OSError:
            return 0, b""


def asset_path(asset_id: str) -> str:
    return "/api/v1/media/assets/" + urllib.parse.quote(asset_id, safe="")


def print_asset_rows(items: list[dict]) -> None:
    print(f"{'ID':<32} {'FONTE':<14} {'TIPO':<10} {'DURATA':>8}  {'ASSET':<12} {'DRIVE':<5} NOME")
    for item in items:
        duration = item.get("duration_ms") or 0
        seconds = f"{duration / 1000:.1f}s" if isinstance(duration, (int, float)) else "-"
        drive = "si" if item.get("has_drive_file") else "no"
        print(
            f"{str(item.get('id', ''))[:32]:<32} "
            f"{str(item.get('source', '-'))[:14]:<14} "
            f"{str(item.get('media_type', '-'))[:10]:<10} "
            f"{seconds:>8}  "
            f"{str(item.get('asset_state', '-'))[:12]:<12} "
            f"{drive:<5} "
            f"{item.get('name', '')}"
        )


def fetch_asset(api: Api, asset_id: str) -> dict:
    asset = api.get_json(asset_path(asset_id))
    if not isinstance(asset, dict) or not asset.get("id"):
        die(f"asset {asset_id} non trovato", 3)
    return asset


def probe_json(api: Api, method: str, path: str, raw_body: bytes = b"") -> tuple[int, dict]:
    """Like probe(), but parse a JSON object when the master returns one."""
    code, raw = api.probe(method, path, raw_body)
    try:
        payload = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        payload = {}
    return code, payload if isinstance(payload, dict) else {}


def is_clip_like(asset: dict) -> bool:
    """The M2M download route is the canonical CLIP route, not a generic
    asset download. Images/scripts are visible in the catalog but must not be
    chosen by the automatic verifier: the clip handler answers 500 for them.
    Rows whose id is a filesystem path are not routable asset ids either.
    """
    asset_id = str(asset.get("id") or "")
    if not asset_id or "/" in asset_id or "\\" in asset_id:
        return False
    media_type = str(asset.get("media_type") or "").strip().lower()
    source = str(asset.get("source") or "").strip().lower()
    if media_type in {"image", "text", "script", "document"}:
        return False
    return source not in {"internet_images", "script"}


def resolve_download_path(raw_output: str, asset_id: str) -> str:
    """Keep the operator's directory; sanitize only the file name.

    The previous implementation called os.path.basename() on the whole path,
    silently moving `/tmp/clip.mp4` to `./clip.mp4`. A trailing separator or an
    existing directory means "put the asset inside this directory".
    """
    raw = os.path.expanduser(raw_output or f"{asset_id}.mp4")
    if raw.endswith((os.sep, "/")) or os.path.isdir(raw):
        return os.path.join(raw, f"{asset_id}.mp4")
    directory, name = os.path.split(raw)
    name = os.path.basename(name) or f"{asset_id}.mp4"
    return os.path.join(directory, name) if directory else name


def _sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _http_error_detail(exc: urllib.error.HTTPError) -> str:
    raw = exc.read().decode("utf-8", "replace")[:500]
    try:
        parsed = json.loads(raw)
        return str(parsed.get("error") or parsed.get("message") or raw)
    except Exception:
        return raw or str(exc)


def download_asset(
    api: Api,
    asset_id: str,
    output: str,
    source: str = "",
    timeout: int = DEFAULT_DOWNLOAD_TIMEOUT,
    max_bytes: int = 0,
    force: bool = False,
    resume: bool = False,
    retries: int = DEFAULT_RETRIES,
) -> tuple[int, str, str]:
    """Download one asset atomically; return (bytes, content-type, sha256).

    The .part file is kept on a transient network failure so `--resume` can
    continue from its size. A 200 response to a Range request means the server
    ignored it (or the asset is local without range support): the partial file
    is truncated and the download restarts from zero.
    """
    asset = fetch_asset(api, asset_id)
    source = source or str(asset.get("source") or infer_source(asset_id))
    expected_hash = str(asset.get("content_hash") or "").strip().lower()
    if os.path.exists(output) and not force:
        die(f"{output} esiste già (usa --force per sovrascriverlo)", 1)
    directory = os.path.dirname(output)
    if directory and not os.path.isdir(directory):
        die(f"la directory {directory} non esiste", 1)

    path = (
        f"/api/v1/media/clips/{urllib.parse.quote(source, safe='')}"
        f"/clips/{urllib.parse.quote(asset_id, safe='')}/download"
    )
    stream_api = Api(api.base_url, api.secret, timeout)
    temporary = output + ".part"
    resume_offset = os.path.getsize(temporary) if resume and os.path.exists(temporary) else 0
    attempts = max(0, retries)
    content_type = "application/octet-stream"

    for attempt in range(attempts + 1):
        if os.path.exists(temporary):
            resume_offset = os.path.getsize(temporary) if resume else 0
        headers = {"Range": f"bytes={resume_offset}-"} if resume_offset > 0 else None
        try:
            with stream_api.post_stream_raw(path, headers) as response:
                status = int(response.getcode() or 200)
                if resume_offset > 0 and status != 206:
                    # Range ignored: start clean instead of appending a full body.
                    resume_offset = 0
                content_type = response.headers.get("Content-Type", content_type)
                declared = (response.headers.get("Content-Length") or "").strip()
                if max_bytes > 0 and declared.isdigit() and resume_offset + int(declared) > max_bytes:
                    if os.path.exists(temporary):
                        os.remove(temporary)
                    die(
                        f"download rifiutato: {declared} byte superano il limite di {max_bytes}",
                        1,
                    )

                mode = "ab" if resume_offset > 0 else "wb"
                size = resume_offset
                with open(temporary, mode) as handle:
                    while True:
                        chunk = response.read(1024 * 1024)
                        if not chunk:
                            break
                        size += len(chunk)
                        if max_bytes > 0 and size > max_bytes:
                            handle.close()
                            os.remove(temporary)
                            die(f"download interrotto: supera il limite di {max_bytes} byte", 1)
                        handle.write(chunk)

            digest = _sha256_file(temporary)
            if expected_hash and digest != expected_hash:
                os.remove(temporary)
                die(
                    f"hash non valido per {asset_id}: atteso {expected_hash}, ottenuto {digest}",
                    1,
                )
            os.replace(temporary, output)
            return size, content_type, digest
        except SystemExit:
            if not resume and os.path.exists(temporary):
                os.remove(temporary)
            raise
        except urllib.error.HTTPError as exc:
            if exc.code == 416 and resume_offset > 0:
                os.remove(temporary)
                resume_offset = 0
                continue
            detail = _http_error_detail(exc)
            if exc.code in RETRYABLE_HTTP_STATUS and attempt < attempts:
                time.sleep(min(10, 2**attempt))
                continue
            if not resume and os.path.exists(temporary):
                os.remove(temporary)
            die(f"HTTP {exc.code} su POST {path}: {detail}", 1)
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            if attempt < attempts:
                time.sleep(min(10, 2**attempt))
                continue
            if not resume and os.path.exists(temporary):
                os.remove(temporary)
            reason = getattr(exc, "reason", exc)
            die(f"download interrotto ({reason}); riprova con --resume", 1)

    die("download fallito dopo i tentativi disponibili", 1)


def cmd_facets(api: Api, args: argparse.Namespace) -> int:
    facets = api.get_json("/api/v1/media/facets")
    if args.json:
        print(json.dumps(facets, indent=2, ensure_ascii=False))
        return 0
    labels = {
        "sources": "Sorgenti",
        "providers": "Provider",
        "media_types": "Tipi media",
        "lifecycle_states": "Lifecycle",
        "asset_states": "Stato asset",
        "index_states": "Stato indice",
    }
    for key, label in labels.items():
        groups = facets.get(key) or []
        print(f"\n{label}:")
        for group in groups:
            print(
                f"  {str(group.get('code', '')):<24} "
                f"{int(group.get('count', 0)):>8}  {group.get('label', '')}"
            )
    return 0


def list_assets(api: Api, args: argparse.Namespace, search: str = "") -> list[dict]:
    params = {
        "source": getattr(args, "source", ""),
        "provider": getattr(args, "provider", ""),
        "media_type": getattr(args, "media_type", ""),
        "lifecycle_state": getattr(args, "lifecycle", ""),
        "asset_state": getattr(args, "asset_state", ""),
        "index_state": getattr(args, "index_state", ""),
        "search": search or getattr(args, "search", ""),
        "limit": getattr(args, "limit", 50),
        "cursor": getattr(args, "cursor", ""),
    }
    page = api.get_json(with_query("/api/v1/media/assets", params))
    items = page.get("items") or []
    if args.json:
        print(json.dumps(page, indent=2, ensure_ascii=False))
        return items
    print_asset_rows(items)
    total = page.get("total")
    has_more = page.get("has_more")
    print(f"\n{len(items)} mostrati · totale {total} · altri: {'si' if has_more else 'no'}")
    if has_more and page.get("next_cursor"):
        print(f"prossima pagina: --cursor {page['next_cursor']}")
    return items


def cmd_assets(api: Api, args: argparse.Namespace) -> int:
    list_assets(api, args)
    return 0


def cmd_search(api: Api, args: argparse.Namespace) -> int:
    list_assets(api, args, search=" ".join(args.query))
    return 0


def cmd_asset(api: Api, args: argparse.Namespace) -> int:
    asset = fetch_asset(api, args.asset_id)
    if args.json:
        print(json.dumps(asset, indent=2, ensure_ascii=False))
        return 0

    print(f"id:            {asset.get('id')}")
    print(f"nome:          {asset.get('name')}")
    print(f"file:          {asset.get('filename')}")
    print(f"sorgente:      {asset.get('source')}")
    print(f"provider:      {asset.get('provider')}")
    print(f"tipo:          {asset.get('media_type')}")
    print(f"durata:        {(asset.get('duration_ms') or 0) / 1000:.2f}s")
    print(f"lifecycle:     {asset.get('lifecycle_state')}")
    print(f"stato asset:   {asset.get('asset_state')}")
    print(f"stato indice:  {asset.get('index_state')}")
    print(f"drive:         {'si' if asset.get('has_drive_file') else 'no'}")
    print(f"locale:        {'si' if asset.get('has_local_file') else 'no'}")
    print(f"hash:          {asset.get('content_hash') or '-'}")
    print(
        f"embedding:     {'si' if asset.get('has_embedding') else 'no'} "
        f"(versione: {asset.get('embedding_version') or '-'})"
    )

    locations = asset.get("locations") or []
    print(f"\nposizioni ({len(locations)}):")
    for location in locations:
        print(f"  - {location.get('location_kind', '-')}: {location.get('uri', '-')}")
        if location.get("external_id"):
            print(f"      drive_file_id: {location['external_id']}")
        if location.get("access_url"):
            print(f"      link:          {location['access_url']}")
        if location.get("download_url"):
            print(f"      download:      {location['download_url']}")

    tracks = asset.get("text_tracks") or []
    print(f"\ntrascrizioni / testi ({len(tracks)}):")
    if not tracks:
        print("  (nessuna traccia corrente su questo asset)")
    for track in tracks:
        content = (track.get("text_content") or "").replace("\n", " ")
        preview = content if args.full else content[:240] + ("…" if len(content) > 240 else "")
        original = "originale" if track.get("is_original") else "traduzione"
        print(
            f"  - {track.get('language_code', '-'):<6} "
            f"{track.get('text_kind', '-'):<14} "
            f"{track.get('status', '-'):<8} "
            f"{len(track.get('text_content') or ''):>6} caratteri  {original}"
        )
        if preview:
            print(f"      {preview}")

    vectors = asset.get("vectors") or []
    print(f"\nvettori ({len(vectors)}):")
    if not vectors:
        print("  (nessun vettore registrato per questo asset)")
    for vector in vectors:
        print(
            f"  - {vector.get('embedding_type', '-')} / {vector.get('model_id', '-')} "
            f"dim={vector.get('dimensions', '-')} "
            f"creato={vector.get('created_at', '-')}"
        )
    return 0


def cmd_transcript(api: Api, args: argparse.Namespace) -> int:
    asset = fetch_asset(api, args.asset_id)
    tracks = asset.get("text_tracks") or []
    selected = [
        track
        for track in tracks
        if track.get("text_kind") == args.kind
        and (not args.lang or track.get("language_code") == args.lang)
    ]
    if not selected:
        available = sorted(
            {f"{track.get('language_code')}/{track.get('text_kind')}" for track in tracks}
        )
        die(
            f"nessuna traccia {args.kind}"
            + (f" in lingua {args.lang}" if args.lang else "")
            + f" per {args.asset_id}. Disponibili: {', '.join(available) or 'nessuna'}",
            3,
        )
    if args.json:
        print(json.dumps(selected, indent=2, ensure_ascii=False))
        return 0
    for index, track in enumerate(selected):
        if index:
            print("\n" + "-" * 72 + "\n")
        print(
            f"# {track.get('language_code')} · {track.get('text_kind')} · "
            f"{track.get('status')} · {track.get('provider') or '-'}"
        )
        print(track.get("text_content") or "")
    return 0


def infer_source(asset_id: str) -> str:
    if asset_id.startswith("yt_"):
        return "youtube"
    if asset_id.startswith("vo_") or asset_id.startswith("voiceover"):
        return "voiceover"
    return "local"


def cmd_download(api: Api, args: argparse.Namespace) -> int:
    output = resolve_download_path(args.out, args.asset_id)
    size, content_type, digest = download_asset(
        api,
        args.asset_id,
        output,
        source=args.source,
        timeout=args.download_timeout,
        force=args.force,
        resume=args.resume,
        retries=args.retries,
    )
    print(
        f"scaricato {args.asset_id} → {output} "
        f"({size} byte, {content_type}, sha256={digest[:12]}…)"
    )
    return 0


def _record_check(
    checks: list[dict[str, object]],
    name: str,
    ok: bool,
    detail: str,
    skipped: bool = False,
) -> None:
    item: dict[str, object] = {"name": name, "ok": bool(ok), "detail": detail}
    if skipped:
        item["skipped"] = True
    checks.append(item)


def _finish_verify(
    api: Api,
    args: argparse.Namespace,
    checks: list[dict[str, object]],
    inspected: dict | None,
    download_id: str,
) -> int:
    ok = all(bool(check["ok"]) for check in checks)
    result = {
        "ok": ok,
        "master": api.base_url,
        "asset_id": str(inspected.get("id")) if inspected else "",
        "download_asset_id": download_id,
        "checks": checks,
    }
    if args.json:
        print(json.dumps(result, indent=2, ensure_ascii=False))
    else:
        print(f"Verifica master: {api.base_url}")
        for check in checks:
            status = "SKIP" if check.get("skipped") else ("PASS" if check["ok"] else "FAIL")
            print(f"  [{status:<4}] {check['name']:<14} {check['detail']}")
        print()
        print(
            "Esito: OK — accesso remoto completo verificato."
            if ok
            else "Esito: FAIL — vedi i controlli sopra."
        )
    return 0 if ok else 1


def cmd_verify(api: Api, args: argparse.Namespace) -> int:
    """Prove the exact remote contract end-to-end without creating a job."""
    checks: list[dict[str, object]] = []

    health_code, health_raw = api.probe("GET", "/health")
    health: dict = {}
    try:
        parsed = json.loads(health_raw) if health_raw else {}
        health = parsed if isinstance(parsed, dict) else {}
    except json.JSONDecodeError:
        pass
    _record_check(
        checks,
        "health",
        health_code == 200,
        f"HTTP {health_code}" + (f" · {health.get('status')}" if health.get("status") else ""),
    )
    if health_code != 200:
        return _finish_verify(api, args, checks, None, "")

    ready_code, ready = probe_json(api, "GET", "/ready")
    if ready_code == 404:
        ready_code, ready = probe_json(api, "GET", "/health/ready")
    _record_check(
        checks,
        "readiness",
        ready_code == 200,
        f"HTTP {ready_code}" + (f" · {ready.get('status')}" if ready.get("status") else ""),
    )

    facets_code, facets = probe_json(api, "GET", "/api/v1/media/facets")
    source_counts = facets.get("sources") or []
    total = sum(
        int(group.get("count", 0))
        for group in source_counts
        if isinstance(group, dict)
    )
    _record_check(
        checks,
        "facets",
        facets_code == 200,
        f"HTTP {facets_code} · {total} asset per fonte",
    )

    list_code, page = probe_json(
        api,
        "GET",
        with_query("/api/v1/media/assets", {"limit": args.max_assets}),
    )
    items = page.get("items") or []
    _record_check(
        checks,
        "assets",
        list_code == 200 and bool(items),
        f"HTTP {list_code} · {len(items)} asset nella pagina",
    )

    inspected: dict | None = None
    if args.asset_id:
        detail_code, payload = probe_json(api, "GET", asset_path(args.asset_id))
        if detail_code == 200 and payload.get("id"):
            inspected = payload
        _record_check(
            checks,
            "asset_detail",
            inspected is not None,
            f"{args.asset_id}: HTTP {detail_code}",
        )
    else:
        detail_code = 0
        best: dict | None = None
        for item in items[: args.max_assets]:
            candidate_id = str(item.get("id") or "")
            if not candidate_id:
                continue
            detail_code, payload = probe_json(api, "GET", asset_path(candidate_id))
            if detail_code != 200 or not payload.get("id"):
                continue
            if best is None:
                best = payload
            if payload.get("text_tracks") and payload.get("vectors"):
                best = payload
                break
            if (payload.get("text_tracks") or payload.get("vectors")) and not (
                best.get("text_tracks") or best.get("vectors")
            ):
                best = payload
        inspected = best
        _record_check(
            checks,
            "asset_detail",
            inspected is not None,
            f"HTTP {detail_code} · {len(items)} candidati",
        )

    if inspected:
        tracks = inspected.get("text_tracks") or []
        vectors = inspected.get("vectors") or []
        languages = ",".join(
            sorted({str(track.get("language_code") or "?") for track in tracks})
        )
        ready_tracks = [
            track
            for track in tracks
            if str(track.get("status") or "").upper() == "READY"
            and str(track.get("text_content") or "").strip()
        ]
        tracks_ok = bool(ready_tracks) or args.allow_empty_text
        _record_check(
            checks,
            "text_tracks",
            tracks_ok,
            f"{len(tracks)} tracce · {len(ready_tracks)} READY"
            + (f" ({languages})" if languages else "")
            + (" · vuoto consentito" if args.allow_empty_text and not ready_tracks else ""),
        )
        vector_detail = f"{len(vectors)} vettori"
        if vectors:
            first = vectors[0]
            vector_detail += (
                f" · {first.get('embedding_type', '-')}/{first.get('model_id', '-')}"
                f" dim={first.get('dimensions', '-')}"
            )
        _record_check(
            checks,
            "vectors",
            bool(vectors) or args.allow_empty_vectors,
            vector_detail + (" · vuoto consentito" if args.allow_empty_vectors and not vectors else ""),
        )
    else:
        _record_check(checks, "text_tracks", False, "asset detail non disponibile")
        _record_check(checks, "vectors", False, "asset detail non disponibile")

    download_id = ""
    if args.skip_download:
        _record_check(checks, "download", True, "saltato (--skip-download)", skipped=True)
    else:
        candidates: list[dict] = []
        if args.download_id:
            code, payload = probe_json(api, "GET", asset_path(args.download_id))
            if code == 200 and payload.get("id"):
                candidates.append(payload)
            else:
                _record_check(
                    checks,
                    "download",
                    False,
                    f"{args.download_id}: HTTP {code}",
                )
        else:
            seen: set[str] = set()

            def add_candidate(asset: dict | None) -> None:
                if not asset or not is_clip_like(asset):
                    return
                if not (asset.get("has_local_file") or asset.get("has_drive_file")):
                    return
                asset_id = str(asset.get("id") or "")
                if not asset_id or asset_id in seen:
                    return
                seen.add(asset_id)
                candidates.append(asset)

            for asset in ([inspected] if inspected else []) + items:
                add_candidate(asset)
            source_limit = max(1, min(args.max_assets, 20))
            for source in DOWNLOAD_SOURCES:
                source_code, source_page = probe_json(
                    api,
                    "GET",
                    with_query(
                        "/api/v1/media/assets",
                        {"source": source, "limit": source_limit},
                    ),
                )
                if source_code != 200:
                    continue
                for item in source_page.get("items") or []:
                    add_candidate(item)
            candidates.sort(
                key=lambda asset: (
                    not bool(asset.get("has_local_file")),
                    asset.get("duration_ms") or 10**9,
                )
            )

        if candidates:
            last_error = "nessun download riuscito"
            # A catalog can contain clip-like rows whose source is not served by
            # the canonical download use case. Try a few candidates instead of
            # failing the whole verification on the first 500.
            for target in candidates[:5]:
                candidate_id = str(target.get("id") or args.download_id)
                # The list projection can contain rows that are not routable
                # (a filesystem path used as id). Resolve the detail first so
                # the verifier tries the next candidate instead of surfacing a
                # misleading 404 as a download failure.
                detail_code, detail = probe_json(api, "GET", asset_path(candidate_id))
                if detail_code != 200 or not detail.get("id"):
                    last_error = f"{candidate_id}: asset non risolvibile (HTTP {detail_code})"
                    continue
                temporary_directory = tempfile.mkdtemp(prefix="pipelinegen-verify-")
                output = os.path.join(temporary_directory, "download.bin")
                try:
                    size, content_type, digest = download_asset(
                        api,
                        candidate_id,
                        output,
                        source=str(target.get("source") or ""),
                        timeout=args.download_timeout,
                        max_bytes=args.max_download_bytes,
                        force=True,
                    )
                    download_id = candidate_id
                    expected_hash = str(detail.get("content_hash") or "").strip().lower()
                    hash_state = "hash ok" if expected_hash and expected_hash == digest else "hash non disponibile"
                    _record_check(
                        checks,
                        "download",
                        size > 0,
                        f"{candidate_id} · {size} byte · {content_type} · "
                        f"sha256={digest[:12]}… · {hash_state}",
                    )
                    break
                except SystemExit as exc:
                    last_error = f"{candidate_id}: fallito (exit {exc.code})"
                except Exception as exc:
                    last_error = f"{candidate_id}: {exc}"
                finally:
                    shutil.rmtree(temporary_directory, ignore_errors=True)
            else:
                _record_check(checks, "download", False, last_error)
        elif not args.download_id:
            _record_check(
                checks,
                "download",
                False,
                "nessun asset audio/video con file locale/Drive nella pagina",
            )

    if args.skip_jobs:
        _record_check(checks, "jobs_submit", True, "saltato (--skip-jobs)", skipped=True)
    else:
        code, _ = api.probe("POST", "/api/v1/jobs", raw_body=b"{")
        ok = code in (400, 422)
        if code in (401, 403):
            detail = f"HTTP {code} · chiave senza jobs.submit o non valida"
        elif ok:
            detail = f"HTTP {code} · jobs.submit autorizzato, nessun job creato"
        else:
            detail = f"HTTP {code} · risposta inattesa"
        _record_check(checks, "jobs_submit", ok, detail)

    return _finish_verify(api, args, checks, inspected, download_id)


def cmd_submit(api: Api, args: argparse.Namespace) -> int:
    try:
        with open(args.payload, "r", encoding="utf-8") as handle:
            document = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        die(f"payload non leggibile ({args.payload}): {exc}", 1)

    if isinstance(document, dict) and document.get("type") and "payload" in document:
        envelope = document
    elif args.type:
        envelope = {"type": args.type, "payload": document}
    else:
        die("il payload non contiene {type,payload}: passa --type TYPE", 1)

    envelope.setdefault("project", args.key)
    envelope.setdefault("idempotency_key", args.idem_key or uuid.uuid4().hex)
    response = api.post_json(
        "/api/v1/jobs", envelope, idem_key=str(envelope["idempotency_key"])
    )
    if args.json:
        print(json.dumps(response, indent=2, ensure_ascii=False))
        return 0
    print(
        f"job_id={response.get('job_id') or response.get('id') or '-'} "
        f"status={response.get('status', '-')} "
        f"idempotency_key={envelope['idempotency_key']}"
    )
    return 0


def cmd_job(api: Api, args: argparse.Namespace) -> int:
    job = api.get_json("/api/v1/jobs/" + urllib.parse.quote(args.job_id, safe=""))
    if args.json:
        print(json.dumps(job, indent=2, ensure_ascii=False))
        return 0
    print(f"job:     {args.job_id}")
    print(f"stato:   {job.get('status', '-')}")
    print(f"stage:   {job.get('current_stage', '-')}")
    print(f"progress:{job.get('progress', 0)}%")
    if job.get("error"):
        print(f"errore:  {job['error']}")
    if job.get("result"):
        print("result:  " + json.dumps(job["result"], ensure_ascii=False)[:2000])
    return 0


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--env",
        default=os.environ.get("VELOX_M2M_ENV", ""),
        help="file env con VELOX_MASTER_URL e VELOX_M2M_SECRET (0600)",
    )
    parser.add_argument("--url", default="", help="URL del master (es. http://192.168.1.10:8000)")
    parser.add_argument("--secret", default="", help="chiave M2M (preferisci --env)")
    parser.add_argument("--json", action="store_true", help="stampa il JSON grezzo")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help="timeout HTTP in secondi")


def add_filters(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--source", default="", help="youtube, voiceover, stock, image, ...")
    parser.add_argument("--provider", default="")
    parser.add_argument("--media-type", dest="media_type", default="")
    parser.add_argument("--lifecycle", default="")
    parser.add_argument("--asset-state", dest="asset_state", default="")
    parser.add_argument("--index-state", dest="index_state", default="")
    parser.add_argument("--search", default="", help="ricerca testuale nel catalogo")
    parser.add_argument("--limit", type=int, default=50)
    parser.add_argument("--cursor", default="")


def main() -> int:
    common = argparse.ArgumentParser(add_help=False)
    add_common(common)

    parser = argparse.ArgumentParser(
        description="Client portabile del catalogo media PipelineGen (API M2M del master).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="La chiave M2M si crea sul master con ops/jobs/remote/provision-key.sh.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    facets = sub.add_parser("facets", parents=[common], help="conteggi del catalogo")
    facets.set_defaults(func=cmd_facets)

    assets = sub.add_parser("assets", parents=[common], help="elenca gli asset")
    add_filters(assets)
    assets.set_defaults(func=cmd_assets)

    search = sub.add_parser("search", parents=[common], help="cerca nel catalogo")
    search.add_argument("query", nargs="+")
    add_filters(search)
    search.set_defaults(func=cmd_search)

    asset = sub.add_parser("asset", parents=[common], help="dettaglio di un asset")
    asset.add_argument("asset_id")
    asset.add_argument("--full", action="store_true", help="stampa il testo completo delle tracce")
    asset.set_defaults(func=cmd_asset)

    transcript = sub.add_parser("transcript", parents=[common], help="trascrizione di un asset")
    transcript.add_argument("asset_id")
    transcript.add_argument("--lang", default="", help="codice lingua (es. it, en)")
    transcript.add_argument("--kind", default="transcript", help="tipo traccia (default transcript)")
    transcript.set_defaults(func=cmd_transcript)

    download = sub.add_parser("download", parents=[common], help="scarica il file di un asset")
    download.add_argument("asset_id")
    download.add_argument("-o", "--out", default="", help="file di destinazione")
    download.add_argument("--source", default="", help="forza la sorgente (default: dal catalogo)")
    download.add_argument("--force", action="store_true")
    download.add_argument(
        "--resume", action="store_true", help="riprendi da un file .part esistente"
    )
    download.add_argument(
        "--retries", type=int, default=DEFAULT_RETRIES, help="tentativi su errori transitori"
    )
    download.add_argument("--download-timeout", type=int, default=DEFAULT_DOWNLOAD_TIMEOUT)
    download.set_defaults(func=cmd_download)

    submit = sub.add_parser("submit", parents=[common], help="invia un job al master")
    submit.add_argument("--type", default="", help="job type (se il payload non è già un envelope)")
    submit.add_argument("--payload", required=True, help="file JSON del payload")
    submit.add_argument("--key", dest="key", default="", help="nome progetto / namespace idempotenza")
    submit.add_argument("--idem-key", dest="idem_key", default="")
    submit.set_defaults(func=cmd_submit)

    job = sub.add_parser("job", parents=[common], help="stato di un job")
    job.add_argument("job_id")
    job.set_defaults(func=cmd_job)

    verify = sub.add_parser(
        "verify", parents=[common], help="verifica completa dell'accesso remoto"
    )
    verify.add_argument(
        "--asset-id",
        default="",
        help="asset da ispezionare (default: cerca tra i primi N)",
    )
    verify.add_argument(
        "--download-id",
        default="",
        help="asset da scaricare (default: primo con file locale/Drive)",
    )
    verify.add_argument(
        "--skip-download", action="store_true", help="non scaricare file"
    )
    verify.add_argument(
        "--skip-jobs",
        action="store_true",
        help="non verificare jobs.submit (chiavi di sola lettura)",
    )
    verify.add_argument(
        "--max-assets",
        type=int,
        default=50,
        help="quanti asset scandire per trovare trascrizioni/vettori",
    )
    verify.add_argument(
        "--download-timeout", type=int, default=DEFAULT_DOWNLOAD_TIMEOUT
    )
    verify.add_argument(
        "--max-download-bytes",
        type=int,
        default=50 * 1024 * 1024,
        help="limite di sicurezza del download (0 = nessun limite)",
    )
    verify.add_argument(
        "--allow-empty-text",
        action="store_true",
        help="accetta un asset senza tracce READY (solo per cataloghi parziali)",
    )
    verify.add_argument(
        "--allow-empty-vectors",
        action="store_true",
        help="accetta un asset senza vettori (solo per cataloghi parziali)",
    )
    verify.set_defaults(func=cmd_verify)

    args = parser.parse_args()
    values: dict[str, str] = {}
    if args.env:
        if not os.path.isfile(args.env):
            die(f"file env non trovato: {args.env}")
        values = load_env(args.env)

    base_url = (
        args.url
        or values.get("VELOX_MASTER_URL", "")
        or os.environ.get("VELOX_MASTER_URL", "")
    )
    secret = (
        args.secret
        or values.get("VELOX_M2M_SECRET", "")
        or os.environ.get("VELOX_M2M_SECRET", "")
    )
    if not base_url or not secret:
        die(
            "manca la connessione: passa --env FILE (consigliato) oppure --url e --secret. "
            "Sul master: ops/jobs/remote/provision-key.sh --client-id <pc> --master-url <url>"
        )

    api = Api(base_url, secret, args.timeout)
    return int(args.func(api, args) or 0)


if __name__ == "__main__":
    raise SystemExit(main())
