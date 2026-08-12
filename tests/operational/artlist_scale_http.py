from __future__ import annotations

import json
import urllib.error
import urllib.request
from typing import Any

from artlist_scale_config import Settings


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
            with urllib.request.urlopen(request, timeout=timeout or self.settings.http_timeout) as response:
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

    def post(self, url: str, payload: dict[str, Any], *, admin: bool = False, headers: dict[str, str] | None = None) -> Any:
        return self.request("POST", url, payload=payload, admin=admin, headers=headers)
