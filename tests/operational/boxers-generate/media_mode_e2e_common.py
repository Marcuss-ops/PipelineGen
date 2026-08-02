#!/usr/bin/env python3
"""Shared transport and assertions for the explicit media-mode suites."""
from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request

BOXERS = {
    "mike_tyson": ("Mike Tyson", "1jz2Y_EWtSck-CcZssDQ5PLsIO6Aa0M03"),
    "muhammad_ali": ("Muhammad Ali", "1umtTRm4Aw9_pjPl0jkWkHpsBBnwWnanJ"),
    "evander_holyfield": ("Evander Holyfield", "1dbVj0SUd0c2tBSLKHIXkKqp-4S-6LTgB"),
    "floyd_mayweather": ("Floyd Mayweather", "1EL7rpQsl-PkmhrPpFgMKGBadmAek1_fR"),
    "sugar_ray_robinson": ("Sugar Ray Robinson", "1xxnNHfperYJ6sZiLcNadgvYIR6wG_jB8"),
}


def subjects(value: str) -> list[str]:
    return list(BOXERS) if value == "all" else [value]


def post(payload: dict) -> dict:
    token = os.environ.get("VELOX_ADMIN_TOKEN")
    if not token:
        raise RuntimeError("VELOX_ADMIN_TOKEN must be loaded through scripts/with-velox-auth")
    req = urllib.request.Request(
        os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000").rstrip("/") + "/api/script/generate",
        data=json.dumps(payload).encode(), method="POST",
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json", "Idempotency-Key": f"media-mode-{time.time_ns()}"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"HTTP {exc.code}: {exc.read().decode(errors='replace')[:500]}") from exc


def wait_job(job_id: str) -> dict:
    base = os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000").rstrip("/")
    token = os.environ["VELOX_ADMIN_TOKEN"]
    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    while time.time() < deadline:
        req = urllib.request.Request(f"{base}/api/jobs/{job_id}/full", headers={"Authorization": f"Bearer {token}"})
        with urllib.request.urlopen(req, timeout=30) as response:
            body = json.loads(response.read().decode())
        status = str(body.get("status") or body.get("job", {}).get("status") or "").upper()
        if status in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "FAILED", "CANCELLED", "RETRY_WAIT", "COMPLETED"}:
            if status in {"FAILED", "CANCELLED"}:
                raise RuntimeError(f"job {job_id} ended {status}")
            if status == "RETRY_WAIT":
                raise RuntimeError(f"job {job_id} exhausted retries")
            return body
        time.sleep(2)
    raise RuntimeError(f"job timeout: {job_id}")


def job_id(response: dict) -> str:
    value = response.get("job_id") or response.get("id") or response.get("job", {}).get("id")
    if not value:
        raise RuntimeError(f"response has no job id: {response}")
    return value


def serialized_output(body: dict) -> str:
    data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
    items = data.get("items", [])
    if items:
        output = items[0].get("result", {}).get("output") or items[0].get("output") or {}
    else:
        output = data.get("output") or {}
    return json.dumps(output, ensure_ascii=False)


def scenes(body: dict) -> list[dict]:
    data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
    items = data.get("items", [])
    if items:
        output = items[0].get("result", {}).get("output") or items[0].get("output") or {}
    else:
        output = data.get("output") or {}
    return output.get("specscene", {}).get("scenes", [])


def assert_clean_document(body: dict) -> None:
    data = body.get("result", {}).get("data", {}) or body.get("job", {}).get("result", {}).get("data", {})
    items = data.get("items", [])
    assert items, "generation result has no items"
    for item in items:
        result = item.get("result", {})
        assert not result.get("warnings", []), result.get("warnings")
        document = (result.get("artifacts") or {}).get("document") or {}
        assert document.get("doc_id"), "Google Doc id is missing"
        assert document.get("doc_link", "").startswith("https://docs.google.com/document/d/"), document


def assert_stock(body: dict, folder_id: str, scenes_expected: int) -> dict:
    assert_clean_document(body)
    raw = serialized_output(body)
    assert '"clip_id"' not in raw and "/file/d/" not in raw
    assert '"folder_link"' in raw and "/drive/folders/" in raw
    found = scenes(body)
    assert len(found) == scenes_expected, len(found)
    for scene in found:
        assert scene.get("kind") == "stock"
        binding = scene.get("bindings", {}).get("stock")
        assert binding and binding.get("folder_id") == folder_id
        assert binding.get("folder_link", "").startswith("https://drive.google.com/drive/folders/")
        assert binding.get("source") == "youtube" and binding.get("fallback") is False
        assert not binding.get("asset_id") and not binding.get("drive_link")
        assert scene.get("bindings", {}).get("clip") is None
    return {"scenes": len(found), "stock_bindings": len(found), "clip_bindings": 0}


def assert_clip(body: dict, expected_ids: set[str], scenes_expected: int) -> dict:
    assert_clean_document(body)
    raw = serialized_output(body)
    assert '"folder_id"' not in raw and '"folder_link"' not in raw and "/drive/folders/" not in raw
    assert '"clip_id"' in raw and "/file/d/" in raw
    found = scenes(body)
    assert len(found) == scenes_expected, len(found)
    seen = []
    for scene in found:
        assert scene.get("kind") == "clip"
        binding = scene.get("bindings", {}).get("clip")
        assert binding and binding.get("clip_id") in expected_ids
        assert binding.get("drive_link", "").startswith("https://drive.google.com/file/d/")
        assert scene.get("bindings", {}).get("stock") is None
        seen.append(binding["clip_id"])
    assert len(set(seen)) == len(seen)
    return {"scenes": len(found), "stock_bindings": 0, "clip_bindings": len(found)}
