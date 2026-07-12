"""JSONL wire-protocol types + stdio I/O for slide_worker.

This module owns the EXCLUSIVE contract between the Python worker
and the Go orchestrator. The public wire protocol is one JSON
object per line on stdout (response) and stdin (request), and stays
UNCHANGED across this refactor (per godlike/07: post-refactor output
must be byte-equivalent to pre-refactor output so any consumer of
the worker sees no diff).

What lives here:
  - GenerateRequest / WorkerResponse dataclasses (TYPED surface for
    new code; existing _generate callers may stay dict-shaped until
    Commit 4 migration).
  - parse_request(line): thin wrapper around json.loads on one stdin
    line — extracted from the inline loop in slide_worker.main() so
    the dispatch-loop body shrinks.
  - validate_generate_request(payload): dict → typed GenerateRequest
    with explicit field-by-field validation (raises ValueError on
    missing required fields).
  - write_response / write_error: the canonical stdout writer pair
    (moved from `_respond` / `_error` in slide_worker.py).

What does NOT live here:
  - DOM actions, browser lifecycle, candidate polling, image
    extraction (those remain in slide_worker.py until their
    respective commit migrates them).
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field
from typing import Optional


@dataclass(frozen=True)
class GenerateRequest:
    """Typed view of one `generate`-action wire payload.

    The wire side stays a JSON object; this dataclass is the
    forward-looking typed surface the new dispatch code consumes.
    Field names mirror the dict keys exactly so the conversion in
    `validate_generate_request` is field-by-field.

    Per AGENTS.md "do not add features" — this dataclass has no
    callers yet. It is committed as scaffolding so future commits
    can adopt typed payload handling without an additional
    dataclass-introduction commit inflating the diff.
    """
    request_id: str
    prompt: str
    prompt_original: str
    output_path: str
    negative_prompt: str
    style_id: str
    width: int
    height: int
    ratio: str
    prompt_suffix: str
    generation_id: str


@dataclass(frozen=True)
class WorkerResponse:
    """Typed view of one wire response sent on stdout.

    Wire shape:
        {
            "status":     <str>,        # "ok" | "error" | "shutdown" | ...
            "request_id": <str>,        # aka "id" — echoed when known
            "output":     <str>,        # path of saved image; "" on error
            "code":       <str>,        # typed error code; "" on success
            "error":      <str>,        # human-readable error message
            "payload":    <dict>,       # extra structured context
        }

    `status` is the canonical discriminator. SRE dashboards rotate on
    `status` + `code`; human dashboards rotate on `status` + `error`.
    """
    status: str
    request_id: str = ""
    output: str = ""
    code: str = ""
    error: str = ""
    payload: dict = field(default_factory=dict)


def parse_request(line: str) -> Optional[dict]:
    """Parse one stdin line into a request dict.

    Mirrors the inline `json.loads(line)` previously inlined in
    slide_worker.main(). Returns None on JSONDecodeError so the
    caller can route the failure to write_error with the canonical
    `unknown request_id` sentinel — same behaviour as the inline
    version.
    """
    if not line:
        return None
    try:
        return json.loads(line)
    except json.JSONDecodeError:
        return None


def validate_generate_request(payload: dict) -> GenerateRequest:
    """Construct a typed GenerateRequest from a wire payload dict.

    Required fields: request_id, prompt, output_path. Optional fields
    default to empty strings / 0 so callers don't see surprise default
    reshuffles; the wire `prompt_original` falls back to `prompt` for
    pre-P1.2 callers (backward compatibility), matching the existing
    `_build_generation_context` logic.

    Raises ValueError on missing required fields — propagates as a
    typed error through write_error. The `id` ↔ `request_id` mapping
    matches slide_worker.py::main() which reads `req.get("id", "")`
    but downstream constructs a payload keyed `id` for dispatcher
    fan-out; we accept BOTH names to keep both styles working
    during the Commit 4 typed-handler roll-out.
    """
    if not isinstance(payload, dict):
        raise ValueError("generate payload must be a JSON object")
    prompt = str(payload.get("prompt", ""))
    if not prompt:
        raise ValueError("missing prompt")
    output_path = str(payload.get("output", "") or payload.get("output_path", ""))
    if not output_path:
        raise ValueError("missing output path")
    request_id = str(payload.get("id", "") or payload.get("request_id", ""))
    return GenerateRequest(
        request_id=request_id,
        prompt=prompt,
        prompt_original=str(payload.get("prompt_original", prompt)),
        output_path=output_path,
        negative_prompt=str(payload.get("negative_prompt", "")),
        style_id=str(payload.get("style_id", "")),
        width=int(payload.get("width") or 0),
        height=int(payload.get("height") or 0),
        ratio=str(payload.get("ratio", "")),
        prompt_suffix=str(payload.get("prompt_suffix", "")),
        generation_id=str(payload.get("generation_id", "")),
    )


def write_response(obj: dict) -> None:
    """Write one JSON object line to stdout and flush.

    Moved verbatim from slide_worker.py::_respond. The wire shape
    (one-line JSON, ensure_ascii=False) is part of the public
    protocol — do NOT change the serialisation policy here without
    a godlike/07 fail-closed change against the Go-side reader.
    """
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def write_error(request_id: Optional[str], msg: str) -> None:
    """Write a canonical error response on stdout.

    Moved verbatim from slide_worker.py::_error. Preserves the
    convention that request_id is echoed under the `id` key
    (not `request_id`) — the wire field name is `id` per the
    historical contract; renaming would break the Go-side reader
    without a paired change.
    """
    payload = {"status": "error", "error": msg}
    if request_id:
        payload["id"] = request_id
    write_response(payload)
