#!/usr/bin/env python3
# velox_client.py — stdlib-only HTTP client for the PipelineGen server.
#
# Mirrors the surface of pkg/veloxclient/client.go (Go) so workers in
# either language have the same authentication, retry, and idempotency
# semantics. Designed for the google-accounting Python sidecar and any
# other Python worker that needs to enqueue jobs over HTTP.
#
# Usage (sync, idempotent — single text item, with scene images):
#     from velox_client import VeloxClient
#     client = VeloxClient("https://pipeline.example.com", os.environ["VELOX_WORKER_TOKEN"])
#     resp = client.submit_async(
#         "api/script/generate",
#         {
#             "version": 2,
#             "preset": "custom",
#             "items": [
#                 {
#                     "id": "item-1",
#                     "title": "Hello world",
#                     "language": "en",
#                     "source": {"type": "text", "topic": "Hello world"},
#                     "script_params": {"target_words": 1500},
#                     "output": {"generate_scene_images": True},
#                 }
#             ],
#         },
#         req_id="req-2026-06-16-001",
#     )
#     print(resp["job_id"])  # → "job_..."
#
# All script generation now flows through the single canonical
# `POST /api/script/generate` endpoint with the GenerationEnvelopeV2
# shape (version: 2). The legacy per-source endpoints
# (`/api/script/generate-with-images`, `/api/script/generate-from-clips`,
# `/api/script/generate-from-catalog`, `/api/script/curate`) are retired;
# clients must declare the source under `items[].source.type` and the
# output flags under `items[].output`. See architecture/current.yaml
# for the deprecation ticket.
#
# Note: the legacy `sentences_per_image` integer (per-image density) is
# NOT a first-class field in the V2 envelope. Target word length lives
# under `items[].script_params.target_words`; per-scene image density
# is owned by the scene-synthesis postprocessor and tuned post-submit
# (see architecture/capability_inventory.yaml#scene-synthesis).
#
# Usage (async polling loop):
#     while True:
#         status = client.get_job(resp["job_id"])
#         if status["status"] in ("completed", "failed", "cancelled"):
#             break
#         time.sleep(2)
#
# Auth: standard `Authorization: Bearer <token>` header. Configure the
# SAME token as VELOX_WORKER_TOKEN in the pipelinegen env. Worker tokens
# isolate blast radius if a worker is compromised.
#
# Idempotency: pass req_id (any alphanumeric string up to 64 chars). Same
# req_id + same endpoint → server returns the SAME job_id via the
# (type, correlation_id) UNIQUE INDEX. The client retries on transient
# 5xx / network errors with the same req_id, so retries cannot create
# duplicate jobs.
"""
velox_client — stdlib-only HTTP client for the PipelineGen server.

Auth: ``Authorization: Bearer <token>`` header. Pair with server-side
``VELOX_WORKER_TOKEN`` (NOT admin token) for non-admin remote workers.

Idempotency: pass ``req_id`` (alphanumeric, ≤64 chars). Same ``req_id`` +
same endpoint → server returns SAME job_id, even if client retries on
network errors. Same X-Request-ID is forwarded on every retry.

Retry policy: 3 attempts by default, exponential backoff base 200 ms
(200 → 400 → 800 ms). 5xx + network errors retry; 4xx (validation, auth)
do not — the request is malformed, retrying won't help.

Error model: raises one of ``AuthError``, ``BadRequestError``,
``ServerError``, ``NotFoundError`` (all exceptions). Provides helpers
``is_retryable(exc)`` for callers that want to drive their own outer loop.
"""

from __future__ import annotations

import json
import os
import ssl
import time
import urllib.error
import urllib.request
from typing import Any, Dict, Optional

__all__ = [
    "VeloxClient",
    "VeloxError",
    "AuthError",
    "BadRequestError",
    "ServerError",
    "NotFoundError",
    "is_retryable",
]


# Default base URL mirrors the canonical server default defined in
# ``internal/platform/config/types.go`` (``Server.Port``: 8000,
# established by the Operational Readiness PR — June 2026). Override
# via the ``base_url`` argument to ``VeloxClient(base_url=...)`` or via
# the standard env vars in your operator scripts (PIPELINEGEN_URL /
# VELOX_MASTER_URL — see cmd/worker/main.go).
DEFAULT_BASE_URL = "http://127.0.0.1:8000"
DEFAULT_MAX_ATTEMPTS = 3
DEFAULT_RETRY_BASE_MS = 200
DEFAULT_TIMEOUT_S = 30.0

_TERMINAL_STATUSES = frozenset({"completed", "failed", "cancelled"})


class VeloxError(Exception):
    """Base class for all velox_client errors."""


class AuthError(VeloxError):
    """401/403 returned by the server. Caller must rotate the token."""


class BadRequestError(VeloxError):
    """Other 4xx (validation, payload too large, missing field). Don't retry."""


class ServerError(VeloxError):
    """5xx or transient network errors after retries are exhausted."""


class NotFoundError(VeloxError):
    """Resolved to 404 — typically a typo in job_id, not worth retrying."""


def is_retryable(exc: BaseException) -> bool:
    """True if exc is a ServerError or a transient network layer indication."""
    if isinstance(exc, ServerError):
        return True
    if isinstance(exc, (AuthError, BadRequestError, NotFoundError)):
        return False
    return False


def is_terminal(status: str) -> bool:
    """True for statuses that won't transition further (completed/failed/cancelled)."""
    return status in _TERMINAL_STATUSES


def _generate_request_id() -> str:
    """32 hex chars; collision rate is negligible for any realistic workload."""
    return os.urandom(16).hex()


class VeloxClient:
    """HTTP client for the PipelineGen server.

    Parameters
    ----------
    base_url:
        PipelineGen root, e.g. ``"https://pipeline.example.com"`` or
        ``"http://127.0.0.1:8080"``. Trailing slash is normalised.
    token:
        Bearer credential. Must match ``VELOX_ADMIN_TOKEN`` OR
        ``VELOX_WORKER_TOKEN`` on the server. Worker tokens are
        recommended for non-admin callers.
    verify_ssl:
        If ``False``, skip TLS certificate validation. Only use this for
        internal clusters with self-signed certs.
    max_attempts:
        Total HTTP attempts including the initial one. Default 3.
    retry_base_ms:
        Initial backoff in milliseconds; doubles each retry. Default 200.
    timeout_s:
        Per-request socket timeout. Default 30.
    """

    def __init__(
        self,
        base_url: str = DEFAULT_BASE_URL,
        token: str = "",
        *,
        verify_ssl: bool = True,
        max_attempts: int = DEFAULT_MAX_ATTEMPTS,
        retry_base_ms: int = DEFAULT_RETRY_BASE_MS,
        timeout_s: float = DEFAULT_TIMEOUT_S,
    ) -> None:
        if not base_url:
            raise ValueError("base_url is required")
        self.base_url = base_url.rstrip("/")
        self.token = token or ""
        self.verify_ssl = verify_ssl
        self.max_attempts = max(1, max_attempts)
        self.retry_base_ms = max(1, retry_base_ms)
        self.timeout_s = timeout_s

    # Internal: prepared for future logging hooks. Kept minimal — Python's
    # urllib is the source of truth for HTTP mechanics.
    def _build_request(
        self,
        method: str,
        url: str,
        body: Optional[bytes],
        req_id: str,
    ) -> urllib.request.Request:
        req = urllib.request.Request(url=url, method=method, data=body)
        if self.token:
            req.add_header("Authorization", f"Bearer {self.token}")
        if req_id:
            req.add_header("X-Request-ID", req_id)
            # POST /api/script/generate (P0.B gate): the server rejects
            # submissions whose Idempotency-Key header is missing with
            # 400 IDEMPOTENCY_KEY_REQUIRED. Mirror the canonical Go
            # client (pkg/veloxclient) and send the same req_id as the
            # idempotency key so retries replay instead of duplicating.
            if method in ("POST", "PUT", "PATCH"):
                req.add_header("Idempotency-Key", req_id)
        if method in ("POST", "PUT", "PATCH"):
            req.add_header("Content-Type", "application/json")
        req.add_header("Accept", "application/json")
        return req

    def _do_once(
        self,
        method: str,
        path: str,
        body: Optional[bytes],
        req_id: str,
    ) -> bytes:
        url = self.base_url + "/" + path.lstrip("/")
        req = self._build_request(method, url, body, req_id)
        ctx = None
        if url.startswith("https://") and not self.verify_ssl:
            ctx = ssl._create_unverified_context()  # noqa: SLF001 — opt-in flag
        try:
            with urllib.request.urlopen(req, timeout=self.timeout_s, context=ctx) as resp:
                return resp.read()
        except urllib.error.HTTPError as exc:
            # Map HTTP status to error taxonomy; raise so retry loop / caller can branch.
            payload = exc.read()
            code = exc.code
            if code in (401, 403):
                raise AuthError(f"status={code} body={payload[:256]!r}") from exc
            if code == 404:
                raise NotFoundError(f"status={code} body={payload[:256]!r}") from exc
            if 400 <= code < 500:
                raise BadRequestError(
                    f"status={code} body={payload[:256]!r}"
                ) from exc
            # 5xx and any other unexpected status → ServerError (retryable).
            raise ServerError(
                f"status={code} body={payload[:256]!r}"
            ) from exc
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            # Network-level error → ServerError so the retry loop re-attempts.
            raise ServerError(f"transport: {exc}") from exc

    def submit_async(
        self,
        path: str,
        payload: Optional[Dict[str, Any]] = None,
        req_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """POST payload (serialised as JSON) and return the parsed response.

        On transient 5xx/network errors, retries with exponential backoff,
        re-using the same ``req_id`` so the server-side idempotency layer
        avoids duplicate jobs even if the second attempt succeeds.

        Raises
        ------
        AuthError, BadRequestError, NotFoundError, ServerError
        """
        if req_id is None or not req_id.strip():
            req_id = _generate_request_id()
        body = None
        if payload is not None:
            body = json.dumps(payload).encode("utf-8")
        last_err: Optional[BaseException] = None
        for attempt in range(1, self.max_attempts + 1):
            try:
                raw = self._do_once("POST", path, body, req_id)
                data = json.loads(raw)
                return {
                    "job_id": data.get("job_id", ""),
                    "status": data.get("status", ""),
                }
            except (AuthError, BadRequestError, NotFoundError):
                raise
            except ServerError as err:
                last_err = err
                if attempt == self.max_attempts:
                    break
                delay_s = (self.retry_base_ms / 1000.0) * (2 ** (attempt - 1))
                time.sleep(delay_s)
        # All attempts exhausted on transient errors.
        assert last_err is not None
        raise last_err

    def get_job(self, job_id: str) -> Dict[str, Any]:
        """GET /api/jobs/{job_id}/full (single attempt; no retry).

        Raises NotFoundError if the server returns 404. Raises other
        VeloxError subclasses for auth/4xx/transport issues.
        """
        if not job_id or not job_id.strip():
            raise ValueError("job_id is required")
        raw = self._do_once("GET", f"api/jobs/{job_id}/full", None, "")
        data = json.loads(raw)
        return {
            "id": data.get("id", ""),
            "status": data.get("status", ""),
            "type": data.get("type", ""),
            "progress": data.get("progress", 0),
            "error": data.get("error", ""),
            "result": data.get("result") or {},
        }
