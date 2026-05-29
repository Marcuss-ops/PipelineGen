"""Source-text to storyboard pipeline with script rewrite, scene images, and docs output."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

import httpx
import yaml

from playwright_client import generate_vids_image_v1_pooled
from reupload_drive_assets import upload_file_to_drive
from style_presets import STYLE_PRESETS


@dataclass
class SourceTextPipelineRequest:
    source_text: Optional[str]
    source_txt_path: Optional[str]
    script_style: str
    visual_style: str
    language: str
    scene_count: int
    output_name: Optional[str]
    project_id: Optional[str]
    video_id: str
    account: Optional[str]
    headless: bool
    drive_folder_id: Optional[str]


def _config_path() -> Path:
    return Path(__file__).resolve().with_name("text_generation.yaml")


def load_config() -> dict[str, Any]:
    path = _config_path()
    if not path.exists():
        raise FileNotFoundError(f"Missing config file: {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    if not isinstance(data, dict):
        raise ValueError("Invalid text_generation.yaml format")
    return data


def read_source_text(source_text: Optional[str], source_txt_path: Optional[str]) -> str:
    if source_text and source_text.strip():
        return source_text.strip()
    if source_txt_path:
        path = Path(source_txt_path).expanduser().resolve()
        if not path.exists():
            raise FileNotFoundError(f"Source text file not found: {path}")
        return path.read_text(encoding="utf-8").strip()
    raise ValueError("Either source_text or source_txt_path must be provided")


def _safe_name(value: str) -> str:
    return re.sub(r"[^A-Za-z0-9._-]+", "_", value).strip("_") or "script"


def _scene_chunks(text: str, limit: int) -> list[str]:
    paragraphs = [p.strip() for p in text.split("\n\n") if p.strip()]
    if len(paragraphs) >= 2:
        return paragraphs[:limit]
    sentences = [s.strip() for s in re.split(r"(?<=[.!?])\s+", text) if s.strip()]
    return (sentences or [text])[:limit]


def _build_messages(config: dict[str, Any], req: SourceTextPipelineRequest, source_text: str) -> list[dict[str, str]]:
    styles = config.get("styles") or {}
    style_cfg = styles.get(req.script_style)
    if not style_cfg:
        raise ValueError(f"Unsupported script style: {req.script_style}")

    system_prompt = (style_cfg.get("system_prompt") or "").strip()
    if not system_prompt:
        raise ValueError(f"Missing system prompt for style: {req.script_style}")

    lines = [
        f"Source text:\n{source_text}",
        f"Language: {req.language.upper()}",
        f"Target scene count: {req.scene_count}",
        f"Style: {req.script_style}",
        "Return JSON only with keys: title, script, scenes.",
        "Each scene must include: id, speaker, text, image_hint.",
        "Keep the rewritten script faithful to the source text.",
    ]
    return [
        {"role": "system", "content": system_prompt},
        {"role": "user", "content": "\n".join(lines)},
    ]


async def _call_ollama(config: dict[str, Any], messages: list[dict[str, str]]) -> dict[str, Any]:
    backend = config.get("backend") or {}
    if (backend.get("type") or "").lower() != "ollama":
        raise ValueError("Only the ollama backend is supported")

    base_url = (backend.get("base_url") or "http://127.0.0.1:11434").rstrip("/")
    model = backend.get("model") or "gemma2:9b"
    timeout_seconds = int(backend.get("timeout_seconds") or 120)
    payload = {"model": model, "messages": messages, "stream": False, "format": "json"}

    async with httpx.AsyncClient(timeout=timeout_seconds) as client:
        response = await client.post(f"{base_url}/api/chat", json=payload)
        response.raise_for_status()
        return response.json()


def _extract_payload(response: dict[str, Any]) -> dict[str, Any]:
    content = (((response.get("message") or {}).get("content")) or "").strip()
    if not content:
        raise ValueError("Empty model response")
    data = json.loads(content)
    if not isinstance(data, dict):
        raise ValueError("Model response must be a JSON object")
    return data


def _normalize_scenes(payload: dict[str, Any], source_text: str, scene_count: int) -> list[dict[str, Any]]:
    scenes = payload.get("scenes")
    if isinstance(scenes, list) and scenes:
        normalized = []
        for idx, scene in enumerate(scenes[:scene_count], start=1):
            normalized.append(
                {
                    "id": scene.get("id") or f"scene_{idx:03d}",
                    "speaker": scene.get("speaker") or "Narrator",
                    "text": scene.get("text") or "",
                    "image_hint": scene.get("image_hint") or scene.get("text") or source_text,
                }
            )
        return normalized

    chunks = _scene_chunks(source_text, scene_count)
    return [
        {
            "id": f"scene_{idx:03d}",
            "speaker": "Narrator",
            "text": chunk,
            "image_hint": chunk,
        }
        for idx, chunk in enumerate(chunks, start=1)
    ]


def _image_prompt(scene: dict[str, Any], visual_style: str) -> str:
    base = scene.get("image_hint") or scene.get("text") or ""
    suffix = STYLE_PRESETS.get(visual_style, "")
    return f"{base}, {suffix}" if suffix else base


def _docs_root(config: dict[str, Any]) -> Path:
    output_cfg = config.get("output") or {}
    root = output_cfg.get("root_dir") or "docs/generated"
    return Path(__file__).resolve().parent / root


def _build_output_paths(config: dict[str, Any], req: SourceTextPipelineRequest, title: str) -> tuple[Path, Path, Path]:
    root = _docs_root(config)
    root.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
    base = _safe_name(req.output_name or title)
    stem = f"{stamp}_{base}_{_safe_name(req.script_style)}"
    folder = root / stem
    folder.mkdir(parents=True, exist_ok=True)
    return folder, folder / "script.json", folder / "script.md"


async def _generate_images(
    scenes: list[dict[str, Any]],
    req: SourceTextPipelineRequest,
) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for scene in scenes:
        prompt = _image_prompt(scene, req.visual_style)
        local_path = await generate_vids_image_v1_pooled(
            video_id=req.video_id,
            prompt=prompt,
            account=req.account,
        )
        drive_link = ""
        drive_file_id = ""
        if local_path and req.drive_folder_id:
            drive_file_id, drive_link = upload_file_to_drive(
                req.drive_folder_id,
                Path(local_path),
                Path(local_path).name,
                "image/png" if str(local_path).lower().endswith(".png") else "image/jpeg",
            )
        results.append(
            {
                **scene,
                "image_prompt": prompt,
                "local_image_path": str(local_path) if local_path else "",
                "drive_file_id": drive_file_id,
                "drive_link": drive_link,
            }
        )
    return results


def _write_docs(folder: Path, source_text: str, payload: dict[str, Any], scenes: list[dict[str, Any]]) -> dict[str, str]:
    json_path = folder / "script.json"
    md_path = folder / "script.md"
    txt_path = folder / "source.txt"

    json_payload = {
        "title": payload.get("title") or "Generated Script",
        "script": payload.get("script") or "",
        "scenes": scenes,
    }
    json_path.write_text(json.dumps(json_payload, indent=2, ensure_ascii=False), encoding="utf-8")
    txt_path.write_text(source_text, encoding="utf-8")

    md_lines = [f"# {json_payload['title']}", "", "## Source Text", "", source_text, "", "## Scenes", ""]
    for scene in scenes:
        md_lines.append(f"### {scene['id']}")
        md_lines.append(f"- Speaker: {scene.get('speaker', '')}")
        md_lines.append(f"- Text: {scene.get('text', '')}")
        md_lines.append(f"- Image: {scene.get('drive_link') or scene.get('local_image_path') or ''}")
        md_lines.append("")
    md_path.write_text("\n".join(md_lines).rstrip() + "\n", encoding="utf-8")

    return {
        "folder": str(folder),
        "json_path": str(json_path),
        "markdown_path": str(md_path),
        "source_txt_path": str(txt_path),
    }


async def run_source_text_pipeline(req: SourceTextPipelineRequest) -> dict[str, Any]:
    config = load_config()
    source_text = read_source_text(req.source_text, req.source_txt_path)
    messages = _build_messages(config, req, source_text)
    response = await _call_ollama(config, messages)
    payload = _extract_payload(response)
    scenes = _normalize_scenes(payload, source_text, req.scene_count)
    scenes = await _generate_images(scenes, req)
    folder, _, _ = _build_output_paths(config, req, payload.get("title") or "Generated Script")
    files = _write_docs(folder, source_text, payload, scenes)
    return {"title": payload.get("title"), "script": payload.get("script"), "scenes": scenes, "files": files}

