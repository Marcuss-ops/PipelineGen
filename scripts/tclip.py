#!/usr/bin/env python3
"""tclip — person-by-person interview clip harvester for PipelineGen.

Thin operator CLI over the canonical live endpoints. No logic is
re-implemented: every path/shape mirrors internal/capabilities/youtube/dto/types.go
and pkg/veloxclient/routes.go. No external model is involved — the operator (or
the agent) chooses the moments and writes the descriptions; the server acquires
the subtitles and Argos handles the translation.

  search  GET  /api/clips/search     discover + rank YouTube interviews
  subs    GET  /api/clips/transcript readable text + per-cue timings (no download)
  clip    POST /api/clips/process    cut the chosen moments, upload to Drive,
                                     register media_assets + index (async job)
  job     GET  /api/jobs/{id}/full   canonical single poller
  drive   GET  /api/drive/files      list a Drive folder's entries
  rename  POST /api/drive/rename     rename a Drive file/folder (used to de-dupe
                                     a stale per-video staging folder)

Auth: VELOX_ADMIN_TOKEN (loaded from refactored/.env when present).
Base: VELOX_BASE_URL (default http://127.0.0.1:8000).

Two tiers per interview (segments file supports both via `"kind"`):

  * hook   — <= 15s, punchy, used as the opening bumper of the video.
             Written with a "Hook: " name prefix + the same marker in topics.
  * moment — <= 60s, the full editorial beat (anecdote / revelation / emotion).

Example segments file entry:

    {"kind":"hook","start":"00:14:03","end":"00:14:16",
     "name":"Non poteva alzarsi dal letto","summary":"...","description":"...",
     "tags":["..."],"topics":["..."],"mentioned_people":["..."]}

    tclip.py clip --url <URL> --folder-id <FOLDER> \
        --segments-file data/tclip/persons/<slug>/01-<vid>.json \
        --with-transcript --source-title "..." --source-channel "..."
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_BASE = os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000"


def _load_token() -> str:
    tok = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()
    if tok:
        return tok
    env = ROOT / ".env"
    if env.is_file():
        for line in env.read_text(errors="ignore").splitlines():
            line = line.strip()
            if line.startswith("VELOX_ADMIN_TOKEN="):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def _request(method, path, *, params=None, body=None, headers=None, timeout=120):
    url = DEFAULT_BASE.rstrip("/") + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = None
    hdrs = {"X-Velox-Admin-Token": _load_token()}
    if body is not None:
        data = json.dumps(body).encode()
        hdrs["Content-Type"] = "application/json"
    hdrs.update(headers or {})
    req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode()
    except urllib.error.HTTPError as e:
        raise SystemExit(f"HTTP {e.code} {method} {path}\n{e.read().decode(errors='ignore')}") from None
    return json.loads(raw) if raw.strip() else {}


def _ts(ms: int) -> str:
    s = max(0, ms // 1000)
    h, s = divmod(s, 3600)
    m, s = divmod(s, 60)
    return f"{h:02d}:{m:02d}:{s:02d}"


def _hms_to_sec(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def _dedup_append(acc, words, max_overlap=90):
    """Append cue words dropping the overlap with the accumulated tail.

    YouTube auto-captions roll (each cue repeats the previous line), so a naive
    concatenation inflates the text ~2-3x. Trim the longest suffix of acc that
    equals a prefix of the incoming cue.
    """
    n = min(len(words), len(acc), max_overlap)
    for L in range(n, 0, -1):
        if acc[-L:] == words[:L]:
            acc.extend(words[L:])
            return
    acc.extend(words)


def _transcript_for_window(video_url, start="", end=""):
    """Fetch the actual spoken words for a window (no download). Returns (lang, text)."""
    params = {"url": video_url}
    if start and end:
        params["start"] = _hms_to_sec(start)
        params["end"] = _hms_to_sec(end)
    tr = _request("GET", "/api/clips/transcript", params=params)
    acc = []
    for c in tr.get("cues", []):
        _dedup_append(acc, c["text"].split())
    text = " ".join(acc).strip() or (tr.get("text") or "").strip()
    return (tr.get("language") or "en"), text


def cmd_search(a):
    r = _request("GET", "/api/clips/search", params={"q": a.query, "limit": a.limit})
    print(f"query={r.get('query')!r} source={r.get('source')} count={r.get('count')}")
    for i, v in enumerate(r.get("results", []), 1):
        caps = "caps" if v.get("has_captions") else "no-caps"
        print(f"{i:2d}. {v['video_id']}  {v['duration']:>5}s  {caps}  "
              f"{v.get('channel_name','')[:22]:<22} {v['title'][:78]}")
    return r


def cmd_subs(a):
    params = {"url": a.url}
    if a.start or a.end:
        params["start"], params["end"] = a.start, a.end
    r = _request("GET", "/api/clips/transcript", params=params)
    cues = r.get("cues", [])
    print(f"video_id={r.get('video_id')} lang={r.get('language')} "
          f"source_type={r.get('source_type')} cue_count={r.get('cue_count')} "
          f"is_original={r.get('is_original')}")
    chunk_ms = int(a.chunk * 1000)
    cur_start, cur_end, acc = None, None, []
    for c in cues:
        ms = int(c["start_ms"])
        if cur_start is None:
            cur_start = ms
        if ms // chunk_ms != cur_start // chunk_ms:
            print(f"[{_ts(cur_start)}-{_ts(cur_end)}] {' '.join(acc)}")
            acc, cur_start = [], ms
        _dedup_append(acc, c["text"].split())
        cur_end = int(c["end_ms"])
    if acc:
        print(f"[{_ts(cur_start)}-{_ts(cur_end)}] {' '.join(acc)}")
    return r


def _normalize_segment(seg, a):
    out = {"start": seg["start"], "end": seg["end"], "name": seg["name"]}
    out["category"] = seg.get("category") or a.category
    if seg.get("summary"):
        out["summary"] = seg["summary"]
    if seg.get("source_title") or a.source_title:
        out["source_title"] = seg.get("source_title") or a.source_title
    if seg.get("source_channel") or a.source_channel:
        out["source_channel"] = seg.get("source_channel") or a.source_channel
    tags = seg.get("tags")
    if tags:
        out["tags"] = tags if isinstance(tags, list) else [t.strip() for t in tags.split(",") if t.strip()]
    for extra in ("topics", "mentioned_people", "hook", "quality_score"):
        if seg.get(extra):
            out[extra] = seg[extra]
    # Two tiers: "hook" (<=15s, opening bumper) vs the default editorial moment.
    if (seg.get("kind") or "").lower() == "hook":
        out["kind"] = "hook"
        out["category"] = seg.get("category") or "hook"
        plain = out["name"][5:].strip() if out["name"].lower().startswith("hook:") else out["name"]
        out["name"] = "Hook: " + plain
        topics = list(out.get("topics") or [])
        marker = "Hook: " + plain
        if marker not in topics:
            topics.insert(0, marker)
        out["topics"] = topics
        tags = list(out.get("tags") or [])
        if "hook" not in [t.lower() for t in tags]:
            tags.insert(0, "hook")
        out["tags"] = tags
    if seg.get("texts"):
        out["texts"] = seg["texts"]
    elif seg.get("description"):
        out["texts"] = [{
            "language_code": seg.get("language_code", "it"),
            "source_type": "provided", "is_original": True,
            "description": seg["description"],
            "summary": out.get("summary", ""), "title": out["name"],
        }]
    return out


def _build_payload(a):
    if a.segments_file:
        raw = json.loads(Path(a.segments_file).read_text())
        if isinstance(raw, dict):
            raw = raw.get("segments", [])
    else:
        raw = [{"start": a.start, "end": a.end, "name": a.name, "summary": a.summary,
                "source_title": a.source_title, "source_channel": a.source_channel,
                "tags": a.tags}]
    segments = [_normalize_segment(s, a) for s in raw]
    dest = {"folder_id": a.folder_id, "create_subfolder": bool(a.create_subfolder)}
    if a.subfolder:
        dest["subfolder_name"] = a.subfolder
    payload = {"url": a.url, "strategy": a.strategy, "concurrency": a.concurrency,
               "keep_audio": True, "write_summary": True,
               "destination": dest, "segments": segments}
    if a.force_keyframes:
        payload["force_keyframes"] = True
    return payload


def _poll(job_id, timeout, interval, quiet=False):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        body = _request("GET", f"/api/jobs/{job_id}/full")
        status = body.get("status") or body.get("job", {}).get("status") or "UNKNOWN"
        if status != last and not quiet:
            print(f"  {job_id} -> {status}")
            last = status
        if status in ("SUCCEEDED", "COMPLETED", "SUCCESS", "DONE"):
            return body, True
        if status in ("FAILED", "ERROR", "CANCELLED", "REJECTED", "DEAD_LETTER"):
            return body, False
        time.sleep(interval)
    return {"status": "TIMEOUT"}, False


def cmd_clip(a):
    payload = _build_payload(a)
    # Store the real spoken words of each window as a transcript track: this is
    # what the texttracks materializer rebuilds media_assets.search_text from.
    if a.with_transcript:
        for seg in payload["segments"]:
            lang, text = _transcript_for_window(a.url, seg["start"], seg["end"])
            if text:
                seg.setdefault("texts", []).insert(0, {
                    "language_code": lang, "source_type": "provided",
                    "is_original": True, "transcript": text})
    if a.idempotency:
        idem = a.idempotency
    else:
        stamp = "|".join(f"{s['start']}-{s['end']}" for s in payload["segments"])
        idem = f"tclip:{_video_key(a.url)}:{stamp}"
    if len(idem) > 255:
        # The server caps Idempotency-Key at 255 chars: keep the key stable but
        # fold the (long) segment stamp into a short digest.
        digest = hashlib.sha256(stamp.encode()).hexdigest()[:40]
        idem = f"tclip:{_video_key(a.url)}:{len(payload['segments'])}segs:{digest}"
    ack = _request("POST", "/api/clips/process", body=payload,
                   headers={"Idempotency-Key": idem})
    job_id = ack.get("job_id")
    if isinstance(job_id, dict):
        job_id = job_id.get("id")
    print(f"submitted job_id={job_id} idem={idem}")
    for s in payload["segments"]:
        print(f"  · {s.get('kind','moment'):<6} {s['start']}-{s['end']}  {s['name']}")
    if a.no_wait or not job_id:
        return ack
    body, ok = _poll(job_id, a.timeout, a.interval)
    status = body.get("status") or body.get("job", {}).get("status")
    print(f"status={status}")
    result = body.get("result") or body.get("job", {}).get("result") or {}
    for item in (result.get("items") or []):
        print(f"  [{item.get('status')}] {item.get('start')}-{item.get('end')} "
              f"{item.get('name')} {item.get('drive_link','')}")
    if not ok:
        print(json.dumps(body.get("error") or body.get("job", {}).get("error") or "", indent=2))
        raise SystemExit(1)
    return body


def _video_key(url):
    q = urllib.parse.urlparse(url)
    if q.netloc.endswith("youtu.be"):
        return q.path.strip("/")
    return urllib.parse.parse_qs(q.query).get("v", ["unknown"])[0]


def _summarize(body):
    status = body.get("status")
    err = body.get("error") or ""
    if isinstance(err, dict):
        err = json.dumps(err)
    err = str(err)
    if len(err) > 400:
        err = err[:400] + "…[truncated]"
    result = body.get("result") or {}
    stats = result.get("stats") or {}
    items = result.get("items") or []
    bad = [i.get("name") for i in items if i.get("status") not in ("processed", "succeeded", "skipped")]
    return status, err, stats, len(items), bad


def cmd_job(a):
    body = _request("GET", f"/api/jobs/{a.job_id}/full")
    status, err, stats, n, bad = _summarize(body)
    print(f"status={status}" + (f"  stats={stats}" if stats else ""))
    if err:
        print(f"  error: {err}")
    if bad:
        print(f"  failed items ({len(bad)}): {', '.join(bad[:12])}")
    if a.verbose:
        print(json.dumps(body, indent=2)[:4000])
    return body


def cmd_drive(a):
    r = _request("GET", "/api/drive/files", params={"folder_id": a.folder_id})
    print(f"count={r.get('count')}")
    for f in r.get("files", []):
        kind = "DIR " if f.get("mime_type") == "application/vnd.google-apps.folder" else "file"
        print(f"  {kind} {f['name']}  {f['id']}")
    return r


def cmd_rename(a):
    r = _request("POST", "/api/drive/rename",
                 body={"file_id": a.file_id, "new_name": a.new_name})
    print(json.dumps(r, indent=2))
    return r


def main(argv=None):
    global DEFAULT_BASE
    p = argparse.ArgumentParser(prog="tclip", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--base", default=DEFAULT_BASE)
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("search"); s.add_argument("query"); s.add_argument("--limit", type=int, default=8)
    s.set_defaults(fn=cmd_search)

    s = sub.add_parser("subs"); s.add_argument("url")
    s.add_argument("--chunk", type=float, default=30, help="group cues into N-second blocks")
    s.add_argument("--start", type=int, default=0); s.add_argument("--end", type=int, default=0)
    s.set_defaults(fn=cmd_subs)

    s = sub.add_parser("clip")
    s.add_argument("--url", required=True); s.add_argument("--folder-id", required=True)
    s.add_argument("--start", default=""); s.add_argument("--end", default="")
    s.add_argument("--name", default=""); s.add_argument("--category", default="celebrity_interview")
    s.add_argument("--summary", default="")
    s.add_argument("--segments-file", default="",
                   help="JSON list of {kind,start,end,name,summary,description,tags,...}; one download, N cuts")
    s.add_argument("--tags", default="")
    s.add_argument("--source-title", default=""); s.add_argument("--source-channel", default="")
    s.add_argument("--subfolder", default=""); s.add_argument("--create-subfolder", action="store_true")
    s.add_argument("--strategy", default="verify"); s.add_argument("--concurrency", type=int, default=2)
    s.add_argument("--force-keyframes", action="store_true")
    s.add_argument("--with-transcript", action="store_true",
                   help="store the real spoken words of each window as a transcript track")
    s.add_argument("--idempotency", default="")
    s.add_argument("--timeout", type=int, default=1800); s.add_argument("--interval", type=int, default=8)
    s.add_argument("--no-wait", action="store_true")
    s.set_defaults(fn=cmd_clip)

    s = sub.add_parser("job"); s.add_argument("job_id")
    s.add_argument("--verbose", action="store_true"); s.set_defaults(fn=cmd_job)
    s = sub.add_parser("drive"); s.add_argument("--folder-id", required=True); s.set_defaults(fn=cmd_drive)
    s = sub.add_parser("rename")
    s.add_argument("--file-id", required=True); s.add_argument("--new-name", required=True)
    s.set_defaults(fn=cmd_rename)

    a = p.parse_args(argv)
    DEFAULT_BASE = a.base
    a.fn(a)


if __name__ == "__main__":
    main()
