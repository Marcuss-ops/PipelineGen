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
from drive_client import upload_file_to_drive
from config import DEFAULT_DRIVE_FOLDER_ID, DEFAULT_IMAGES_DRIVE_FOLDER_ID
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
    images_drive_folder_id: Optional[str]


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
    compact = " ".join(text.split()).strip()
    if not compact:
        return [""] * max(1, limit)

    paragraphs = [p.strip() for p in text.split("\n\n") if p.strip()]
    if len(paragraphs) >= limit:
        return paragraphs[:limit]

    candidates = paragraphs if len(paragraphs) > 1 else []
    if len(candidates) < limit:
        sentences = [s.strip() for s in re.split(r"(?<=[.!?])\s+", compact) if s.strip()]
        if len(sentences) > len(candidates):
            candidates = sentences

    if len(candidates) < limit:
        clauses = [c.strip(" ,;:-") for c in re.split(r"\s*(?:,|;|:|\s-\s|—)\s*", compact) if c.strip(" ,;:-")]
        if len(clauses) > len(candidates):
            candidates = clauses

    if not candidates:
        candidates = [compact]

    if len(candidates) >= limit:
        return candidates[:limit]

    words = compact.split()
    if len(words) > len(candidates):
        size = max(1, len(words) // limit)
        word_chunks = []
        for idx in range(0, len(words), size):
            chunk = " ".join(words[idx : idx + size]).strip()
            if chunk:
                word_chunks.append(chunk)
        if len(word_chunks) >= len(candidates):
            candidates = word_chunks

    if len(candidates) >= limit:
        return candidates[:limit]

    while len(candidates) < limit:
        candidates.append(candidates[-1])
    return candidates[:limit]


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


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    if not isinstance(value, str):
        value = str(value)
    value = value.strip()
    if value.lower() in {"null", "none", "undefined"}:
        return ""
    return value


def _derive_title(source_text: str, payload: dict[str, Any]) -> str:
    title = _clean_text(payload.get("title"))
    if title:
        return title

    compact = " ".join(source_text.split())
    if not compact:
        return "Generated Script"

    first_sentence = re.split(r"(?<=[.!?])\s+", compact, maxsplit=1)[0].strip()
    if len(first_sentence) > 72:
        cut = first_sentence[:72].rsplit(" ", 1)[0].strip()
        first_sentence = cut or first_sentence[:72].strip()
    return first_sentence.rstrip(".!?;:")


def _derive_script(source_text: str, payload: dict[str, Any], scenes: list[dict[str, Any]]) -> str:
    script = _clean_text(payload.get("script"))
    if script:
        return script

    scene_texts = [_clean_text(scene.get("text")) for scene in scenes]
    scene_texts = [text for text in scene_texts if text]
    if scene_texts:
        return "\n\n".join(scene_texts)
    return source_text.strip()


def _normalize_scenes(payload: dict[str, Any], source_text: str, scene_count: int) -> list[dict[str, Any]]:
    scenes = payload.get("scenes")
    if isinstance(scenes, list) and scenes:
        normalized = []
        for idx, scene in enumerate(scenes[:scene_count], start=1):
            fallback_chunk = _scene_chunks(source_text, scene_count)[idx - 1] if scene_count > 0 else source_text
            normalized.append(
                {
                    "id": _clean_text(scene.get("id")) or f"scene_{idx:03d}",
                    "speaker": _clean_text(scene.get("speaker")) or "Narrator",
                    "text": _clean_text(scene.get("text")) or fallback_chunk,
                    "image_hint": _clean_text(scene.get("image_hint"))
                    or _clean_text(scene.get("text"))
                    or fallback_chunk,
                }
            )
        if len(normalized) >= scene_count:
            return normalized

        fallback_chunks = _scene_chunks(source_text, scene_count)
        for idx in range(len(normalized) + 1, scene_count + 1):
            chunk = fallback_chunks[idx - 1] if idx - 1 < len(fallback_chunks) else source_text
            normalized.append(
                {
                    "id": f"scene_{idx:03d}",
                    "speaker": "Narrator",
                    "text": chunk,
                    "image_hint": chunk,
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


def _effective_drive_folder_id(req: SourceTextPipelineRequest) -> str:
    return (req.drive_folder_id or DEFAULT_DRIVE_FOLDER_ID or "").strip()


def _effective_images_drive_folder_id(req: SourceTextPipelineRequest) -> str:
    return (req.images_drive_folder_id or DEFAULT_IMAGES_DRIVE_FOLDER_ID or "").strip()


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
    drive_folder_id = _effective_images_drive_folder_id(req)
    for scene in scenes:
        prompt = _image_prompt(scene, req.visual_style)
        local_path = await generate_vids_image_v1_pooled(
            video_id=req.video_id,
            prompt=prompt,
            account=req.account,
        )
        drive_link = ""
        drive_file_id = ""
        if local_path and drive_folder_id:
            drive_file_id = upload_file_to_drive(
                drive_folder_id,
                Path(local_path),
                Path(local_path).name,
                "image/png" if str(local_path).lower().endswith(".png") else "image/jpeg",
            )
            drive_link = f"https://drive.google.com/file/d/{drive_file_id}/view" if drive_file_id else ""
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


def _upload_docs_to_drive(files: dict[str, str], drive_folder_id: str) -> dict[str, str]:
    uploaded: dict[str, str] = {}
    uploads = [
        ("json_drive_file_id", Path(files["json_path"]), "script.json", "application/json", None),
        (
            "markdown_drive_file_id",
            Path(files["markdown_path"]),
            "script",
            "text/plain",
            "application/vnd.google-apps.document",
        ),
        (
            "source_drive_file_id",
            Path(files["source_txt_path"]),
            "source",
            "text/plain",
            "application/vnd.google-apps.document",
        ),
    ]
    for key, path, filename, mime_type, target_mime_type in uploads:
        if not path.exists():
            raise FileNotFoundError(f"Missing docs file for Drive upload: {path}")
        file_id = upload_file_to_drive(
            drive_folder_id,
            path,
            filename,
            mime_type,
            drive_mime_type=target_mime_type,
        )
        uploaded[key] = file_id
        uploaded[f"{key.replace('_file_id', '')}_drive_link"] = f"https://drive.google.com/file/d/{file_id}/view"
    return uploaded


async def run_source_text_pipeline(req: SourceTextPipelineRequest) -> dict[str, Any]:
    config = load_config()
    source_text = read_source_text(req.source_text, req.source_txt_path)
    messages = _build_messages(config, req, source_text)
    response = await _call_ollama(config, messages)
    payload = _extract_payload(response)
    scenes = _normalize_scenes(payload, source_text, req.scene_count)
    title = _derive_title(source_text, payload)
    script = _derive_script(source_text, payload, scenes)
    scenes = await _generate_images(scenes, req)
    folder, _, _ = _build_output_paths(config, req, title)
    files = _write_docs(folder, source_text, {"title": title, "script": script}, scenes)
    drive_docs = _upload_docs_to_drive(files, _effective_drive_folder_id(req))
    return {"title": title, "script": script, "scenes": scenes, "files": files, "drive_docs": drive_docs}
