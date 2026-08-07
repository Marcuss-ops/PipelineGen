#!/usr/bin/env python3
"""Canonical Whisper runtime preflight.

This is the single runtime gate used by systemd, the worker image probe, CI,
and operator diagnostics. It never downloads a model and never transcribes.

Checks:
* the Python interpreter currently executing the script;
* faster-whisper and CTranslate2 imports and versions;
* pinned NVIDIA CUDA 12 package versions when a lockfile is available;
* CUDA device visibility through CTranslate2;
* loadable CUDA 12 cuBLAS, cuNVRTC, and cuDNN libraries;
* a valid, configured Whisper model name or local model path.

``VELOX_WHISPER_DEVICE=auto`` may fall back to CPU/int8 when CUDA is not
usable. ``cuda`` fails closed instead of silently falling back.

Exit codes:
    0: runtime is usable for the requested device
    1: package, model, lockfile, or environment error
    2: CUDA was explicitly requested but is unusable
    3: invalid configuration
"""

from __future__ import annotations

import ctypes
import importlib.metadata as metadata
import json
import os
import re
import sys
from pathlib import Path
from typing import Any

try:
    from whisper_runtime import (
        CUDA_LIBRARY_SONAMES,
        cuda_library_dirs,
        prepare_cuda_runtime,
    )
except ImportError:
    from scripts.tools.whisper_runtime import (
        CUDA_LIBRARY_SONAMES,
        cuda_library_dirs,
        prepare_cuda_runtime,
    )


CORE_PACKAGES = ("faster-whisper", "ctranslate2")
CUDA_PACKAGES = (
    "nvidia-cublas-cu12",
    "nvidia-cuda-nvrtc-cu12",
    "nvidia-cudnn-cu12",
)
LOCK_PACKAGES = CORE_PACKAGES + CUDA_PACKAGES
CUDA_LIBRARIES = CUDA_LIBRARY_SONAMES
MODEL_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]*$")
KNOWN_MODEL_NAMES = frozenset(
    {
        "tiny",
        "base",
        "small",
        "medium",
        "large-v1",
        "large-v2",
        "large-v3",
        "large-v3-turbo",
        "distil-large-v2",
        "distil-medium.en",
        "distil-small.en",
        "distil-large-v3",
    }
)


def _report(ok: bool, code: int, **fields: Any) -> int:
    """Print exactly one machine-readable JSON document and return its code."""
    document = {"ok": ok, "python": sys.executable}
    document.update(fields)
    print(json.dumps(document, sort_keys=True))
    return code


def _model_check() -> tuple[str | None, dict[str, Any], str | None]:
    """Validate model configuration without downloading model weights."""
    raw = os.environ.get("VELOX_WHISPER_MODEL")
    model = "base" if raw is None else raw.strip()
    if not model:
        return None, {"configured": False}, "VELOX_WHISPER_MODEL must not be empty"

    path_like = model.startswith(("/", "./", "../"))
    if path_like:
        path = Path(model)
        if not path.is_dir():
            return None, {"configured": True, "path_exists": False}, (
                f"Whisper model path does not exist: {model}"
            )
        return model, {
            "configured": True,
            "kind": "path",
            "path_exists": True,
        }, None

    if not MODEL_NAME_RE.fullmatch(model):
        return None, {"configured": False}, (
            "VELOX_WHISPER_MODEL contains unsupported characters"
        )
    if "/" not in model and model not in KNOWN_MODEL_NAMES:
        return None, {"configured": False, "kind": "name"}, (
            f"unsupported Whisper model name: {model}"
        )
    return model, {
        "configured": True,
        "kind": "name",
        "known_name": model in KNOWN_MODEL_NAMES,
        "weights_checked": False,
    }, None


def _library_dirs() -> list[Path]:
    """Compatibility wrapper around the shared CUDA directory resolver."""
    return cuda_library_dirs()


def _check_cuda_libraries() -> dict[str, Any]:
    """Check CUDA 12 library files and dynamic-loader visibility."""
    directories = _library_dirs()
    result: dict[str, Any] = {}
    for label, soname in CUDA_LIBRARIES.items():
        candidates = [directory / soname for directory in directories]
        present = any(candidate.exists() for candidate in candidates)
        loaded = False
        load_error = ""
        try:
            ctypes.CDLL(soname)
            loaded = True
        except OSError as exc:
            load_error = str(exc)
        result[label] = {
            "soname": soname,
            "present": present,
            "loaded": loaded,
        }
        if load_error and not present:
            result[label]["load_error"] = load_error
    result["ok"] = all(
        result[label]["present"] and result[label]["loaded"]
        for label in CUDA_LIBRARIES
    )
    return result


def _read_lock_pins() -> tuple[dict[str, str], str | None, str | None]:
    """Read package pins from the configured image lockfile, if present."""
    configured = os.environ.get("VELOX_WHISPER_LOCKFILE", "").strip()
    candidates = [Path(configured)] if configured else [
        Path("/opt/whisper/requirements.lock.txt"),
    ]
    lockfile = next((path for path in candidates if path.is_file()), None)
    if lockfile is None:
        if configured:
            return {}, configured, f"Whisper lockfile is missing: {configured}"
        return {}, None, None

    pins: dict[str, str] = {}
    for line in lockfile.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "==" not in line:
            continue
        name, version = line.split("==", 1)
        if name in LOCK_PACKAGES:
            pins[name] = version
    missing = [name for name in LOCK_PACKAGES if name not in pins]
    if missing:
        return pins, str(lockfile), (
            "Whisper lockfile misses required pins: " + ", ".join(missing)
        )
    return pins, str(lockfile), None


def _package_versions() -> tuple[dict[str, str], dict[str, str]]:
    """Return installed versions and import/metadata errors."""
    versions: dict[str, str] = {}
    errors: dict[str, str] = {}
    for package in LOCK_PACKAGES:
        try:
            if package in CORE_PACKAGES:
                __import__("faster_whisper" if package == "faster-whisper" else "ctranslate2")
            versions[package] = metadata.version(package)
        except Exception as exc:  # noqa: BLE001 - machine-readable fail-closed result
            errors[package] = str(exc)
    return versions, errors


def main() -> int:
    helper_dir = Path(__file__).resolve().parent
    sys.path.insert(0, str(helper_dir))
    python_version = ".".join(str(part) for part in sys.version_info[:3])
    if sys.version_info < (3, 9):
        return _report(
            False,
            1,
            error="Python 3.9 or newer is required",
            python_version=python_version,
        )

    device_choice = (
        os.environ.get("VELOX_WHISPER_DEVICE", "auto").strip().lower() or "auto"
    )
    if device_choice not in ("auto", "cuda", "cpu"):
        return _report(
            False,
            3,
            error=f"invalid VELOX_WHISPER_DEVICE: {device_choice}",
            device=device_choice,
        )

    model, model_check, model_error = _model_check()
    if model_error:
        return _report(False, 3, error=model_error, model=model, model_check=model_check)

    lock_pins, lockfile, lock_error = _read_lock_pins()
    if lock_error:
        return _report(False, 1, error=lock_error, model=model, model_check=model_check)

    try:
        prepare_cuda_runtime()
    except Exception as exc:  # noqa: BLE001 - machine-readable fail-closed result
        return _report(
            False,
            1,
            error=f"CUDA runtime preparation failed: {exc}",
            model=model,
            model_check=model_check,
            python_version=python_version,
        )

    versions, package_errors = _package_versions()
    package_status = {
        package: {
            "installed": package in versions,
            "version": versions.get(package),
        }
        for package in LOCK_PACKAGES
    }
    if package_errors:
        missing_core = [package for package in CORE_PACKAGES if package in package_errors]
        missing_cuda = [package for package in CUDA_PACKAGES if package in package_errors]
        if missing_core or (device_choice == "cuda" and missing_cuda):
            return _report(
                False,
                1,
                error="required Whisper package check failed",
                model=model,
                model_check=model_check,
                python_version=python_version,
                packages=package_status,
                package_errors=package_errors,
            )

    pin_mismatches = {
        package: {"expected": expected, "installed": versions.get(package)}
        for package, expected in lock_pins.items()
        if versions.get(package) != expected
    }
    if pin_mismatches:
        return _report(
            False,
            1,
            error="installed Whisper packages do not match lockfile",
            model=model,
            model_check=model_check,
            lockfile=lockfile,
            packages=package_status,
            pin_mismatches=pin_mismatches,
        )

    try:
        import ctranslate2

        cuda_devices = ctranslate2.get_cuda_device_count()
    except Exception:
        cuda_devices = 0
    cuda_libraries = _check_cuda_libraries()
    cuda_usable = cuda_devices > 0 and bool(cuda_libraries["ok"])

    if device_choice == "cuda" and not cuda_usable:
        return _report(
            False,
            2,
            error="VELOX_WHISPER_DEVICE=cuda but CUDA/cuBLAS/cuDNN is unusable",
            device="cuda",
            compute_type=None,
            cuda_devices=cuda_devices,
            cuda_libraries=cuda_libraries,
            model=model,
            model_check=model_check,
            lockfile=lockfile,
            python_version=python_version,
            packages=package_status,
        )

    if device_choice == "cpu":
        device, compute_type = "cpu", "int8"
    elif cuda_usable:
        device, compute_type = "cuda", "float16"
    else:
        device, compute_type = "cpu", "int8"

    return _report(
        True,
        0,
        device=device,
        requested_device=device_choice,
        compute_type=compute_type,
        cuda_devices=cuda_devices,
        cuda_usable=cuda_usable,
        cuda_libraries=cuda_libraries,
        model=model,
        model_check=model_check,
        lockfile=lockfile,
        python_version=python_version,
        packages=package_status,
        warnings=(
            ["CUDA unavailable; auto selected CPU/int8"]
            if device_choice == "auto" and not cuda_usable
            else []
        ),
    )


if __name__ == "__main__":
    sys.exit(main())
