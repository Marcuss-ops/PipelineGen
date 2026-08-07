"""Shared Whisper runtime helpers.

This module owns CUDA 12 library discovery and loader preparation. Both the
canonical preflight and the transcription bridge use it so CUDA policy is not
duplicated across entry points.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path


CUDA_LIBRARY_SONAMES = {
    "cublas": "libcublas.so.12",
    "cuda_nvrtc": "libnvrtc.so.12",
    "cudnn": "libcudnn.so.9",
}
DEFAULT_CUDA_LIBRARY_DIRS = (
    "/usr/local/lib/ollama/cuda_v12",
    "/usr/local/cuda-12/lib64",
    "/usr/local/cuda/lib64",
)


def cuda_library_dirs() -> list[Path]:
    """Return de-duplicated CUDA library directories in search order."""
    raw_dirs = [
        os.environ.get("VELOX_WHISPER_CUDA_LIB_DIR", ""),
        *os.environ.get("LD_LIBRARY_PATH", "").split(os.pathsep),
        *DEFAULT_CUDA_LIBRARY_DIRS,
    ]
    result: list[Path] = []
    seen: set[str] = set()
    for raw in raw_dirs:
        if not raw:
            continue
        path = Path(raw)
        key = str(path)
        if key not in seen:
            seen.add(key)
            result.append(path)
    return result


def prepare_cuda_runtime() -> None:
    """Re-exec once with a discovered CUDA 12 library directory on the loader path."""
    if os.environ.get("VELOX_WHISPER_CUDA_RUNTIME_READY") == "1":
        return

    library_path = os.environ.get("LD_LIBRARY_PATH", "")
    directories = cuda_library_dirs()
    if not any((directory / CUDA_LIBRARY_SONAMES["cublas"]).exists() for directory in directories):
        return

    existing = [item for item in library_path.split(os.pathsep) if item]
    missing = [str(directory) for directory in directories if str(directory) not in existing]
    if not missing:
        os.environ["VELOX_WHISPER_CUDA_RUNTIME_READY"] = "1"
        return

    env = os.environ.copy()
    env["LD_LIBRARY_PATH"] = os.pathsep.join([*missing, *existing])
    env["VELOX_WHISPER_CUDA_RUNTIME_READY"] = "1"
    os.execve(sys.executable, [sys.executable, *sys.argv], env)
