"""Shared device policy for Python ML sidecars.

The policy is intentionally independent from Whisper's VELOX_WHISPER_DEVICE
contract. Embedding and reranker services use their own PIPELINEGEN_*
variables so an operator can observe and control each workload separately.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Callable, Any

VALID_DEVICES = ("auto", "cpu", "cuda")


@dataclass(frozen=True)
class DeviceSelection:
    requested: str
    effective: str
    require_gpu: bool
    cuda_available: bool


def env_flag(name: str, default: bool = False) -> bool:
    """Parse a conventional boolean environment variable."""
    raw = os.environ.get(name, "")
    if raw == "":
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def cuda_is_available() -> bool:
    """Return CUDA availability without importing torch until it is needed."""
    try:
        import torch

        return bool(torch.cuda.is_available())
    except Exception:
        return False


def resolve_device(
    requested: str = "auto",
    *,
    require_gpu: bool = False,
    cuda_available: bool | None = None,
    availability: Callable[[], bool] = cuda_is_available,
) -> DeviceSelection:
    """Resolve a sidecar device and fail closed when GPU is required.

    ``cuda`` is always explicit and terminal when unavailable. ``auto`` uses
    CUDA when available and otherwise selects CPU, unless ``require_gpu`` is
    enabled. ``cpu`` is an intentional CPU choice and is rejected only when
    GPU-required mode is enabled.
    """
    normalized = (requested or "auto").strip().lower()
    if normalized not in VALID_DEVICES:
        raise ValueError(
            f"invalid device {requested!r}; expected one of {', '.join(VALID_DEVICES)}"
        )

    available = availability() if cuda_available is None else bool(cuda_available)
    if normalized == "cuda" and not available:
        raise RuntimeError("CUDA device was explicitly requested but is unavailable")
    if require_gpu and normalized == "cpu":
        raise RuntimeError("GPU-required mode cannot use an explicit CPU device")
    if require_gpu and not available:
        raise RuntimeError("GPU is required but CUDA is unavailable")

    effective = "cuda" if normalized == "cuda" or (normalized == "auto" and available) else "cpu"
    return DeviceSelection(
        requested=normalized,
        effective=effective,
        require_gpu=require_gpu,
        cuda_available=available,
    )


def model_device(model: Any) -> str:
    """Read the effective device from SentenceTransformer/CrossEncoder models."""
    candidates = [
        getattr(model, "device", None),
        getattr(model, "_target_device", None),
        getattr(getattr(model, "model", None), "device", None),
    ]
    for candidate in candidates:
        if candidate is not None:
            return str(candidate)
    return "unknown"


def assert_model_device(model: Any, selection: DeviceSelection, label: str) -> str:
    """Return the observed model device and reject silent GPU degradation."""
    observed = model_device(model)
    if selection.effective == "cuda" and not observed.lower().startswith("cuda"):
        raise RuntimeError(
            f"{label} requested CUDA but model is running on {observed}"
        )
    return observed


def embedding_health_payload(
    *,
    queue_depth: int,
    total_requests: int,
    inference_slots: int,
    text_device: str,
    visual_device: str | None,
    audio_device: str | None,
    selection: DeviceSelection,
) -> dict:
    """Build the stable embedding /health response envelope."""
    return {
        "status": "ok",
        "queue_depth": queue_depth,
        "total_requests": total_requests,
        "inference_slots": inference_slots,
        "requested_device": selection.requested,
        "device": selection.effective,
        "cuda_available": selection.cuda_available,
        "gpu_required": selection.require_gpu,
        "text_device": text_device,
        "visual_device": visual_device,
        "audio_device": audio_device,
    }


def reranker_health_payload(
    *, model_name: str, model_device_name: str, selection: DeviceSelection
) -> dict:
    """Build the stable reranker /health response envelope."""
    return {
        "ok": True,
        "model": model_name,
        "requested_device": selection.requested,
        "device": selection.effective,
        "model_device": model_device_name,
        "cuda_available": selection.cuda_available,
        "gpu_required": selection.require_gpu,
    }
