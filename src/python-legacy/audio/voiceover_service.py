import os
import re
import subprocess
import sys
import tempfile
import time
from typing import List, Optional


VOICEOVER_LANGUAGES = [
    {"code": "en-US", "name": "English (US)"},
    {"code": "en-GB", "name": "English (UK)"},
    {"code": "it-IT", "name": "Italian"},
    {"code": "es-ES", "name": "Spanish"},
    {"code": "fr-FR", "name": "French"},
    {"code": "de-DE", "name": "German"},
    {"code": "pt-BR", "name": "Portuguese (Brazil)"},
    {"code": "pl-PL", "name": "Polish"},
    {"code": "nl-NL", "name": "Dutch"},
    {"code": "ru-RU", "name": "Russian"},
]

VOICE_MAP_SHORT = {
    "it": "it-IT-DiegoNeural",
    "en": "en-US-GuyNeural",
    "es": "es-ES-AlvaroNeural",
    "fr": "fr-FR-HenriNeural",
    "de": "de-DE-ConradNeural",
    "pt": "pt-BR-AntonioNeural",
    "ru": "ru-RU-DmitryNeural",
    "tr": "tr-TR-AhmetNeural",
    "id": "id-ID-AndiNeural",
    "pl": "pl-PL-MarekNeural",
}

VOICE_MAP_FULL = {
    "en-US": "en-US-AriaNeural",
    "en-GB": "en-GB-SoniaNeural",
    "it-IT": "it-IT-ElsaNeural",
    "es-ES": "es-ES-ElviraNeural",
    "fr-FR": "fr-FR-DeniseNeural",
    "de-DE": "de-DE-KatjaNeural",
}


def list_voiceover_languages() -> List[dict]:
    return list(VOICEOVER_LANGUAGES)


def _safe_drive_folder_name(name: str) -> str:
    cleaned = re.sub(r"[^\w\s\-]", "", str(name or "")).strip()
    cleaned = re.sub(r"\s+", " ", cleaned)
    return cleaned[:120] or "Voiceover"


def _extract_drive_folder_id(raw: str) -> str:
    s = str(raw or "").strip()
    if not s:
        return ""
    match = re.search(r"/folders/([a-zA-Z0-9_-]{10,})", s)
    if match:
        return match.group(1)
    match = re.search(r"[?&]id=([a-zA-Z0-9_-]{10,})", s)
    if match:
        return match.group(1)
    if re.fullmatch(r"[a-zA-Z0-9_-]{10,}", s):
        return s
    return ""


def _edge_tts_bin() -> str:
    bin_path = os.environ.get("EDGE_TTS_BIN") or os.path.join(os.path.dirname(sys.executable), "edge-tts")
    return bin_path if os.path.exists(bin_path) else "edge-tts"


def generate_voiceover_file(text: str, language: str = "en-US", filename: str = "voiceover.mp3", voice: str = "") -> str:
    with tempfile.NamedTemporaryFile(suffix=".mp3", delete=False) as handle:
        output_path = handle.name
    selected_voice = voice or VOICE_MAP_FULL.get(language, "en-US-AriaNeural")
    cmd = [_edge_tts_bin(), "--voice", selected_voice, "--write-media", output_path, "--text", text[:5000]]
    result = subprocess.run(cmd, capture_output=True, timeout=120)
    if result.returncode != 0 or not os.path.exists(output_path):
        raise Exception("Edge TTS failed")
    return output_path


def generate_and_upload_voiceover(script_text: str, language: str, title: str, drive_folder: str, sp, project_name: str = ""):
    if not sp:
        raise Exception("ScriptPython not available")
    vo_lang = (language or "en").split("-")[0].lower()
    vo_voice = VOICE_MAP_SHORT.get(vo_lang, "en-US-GuyNeural")
    parent_folder_id = _extract_drive_folder_id(drive_folder)
    target_folder_id = parent_folder_id
    target_folder_name = _safe_drive_folder_name(project_name or title or "Voiceover")

    try:
        if parent_folder_id and hasattr(sp, "GOOGLE_DRIVE_MANAGER"):
            sub_id = sp.GOOGLE_DRIVE_MANAGER.create_folder(target_folder_name, parent_folder_id)
            if sub_id:
                target_folder_id = sub_id
    except Exception:
        pass

    if not target_folder_id:
        raise Exception("voiceover_drive_folder missing or invalid")

    words = (script_text or "").split()
    chunks = [" ".join(words[i:i + 700]) for i in range(0, len(words), 700)] or [script_text]
    chunk_files = []
    for idx, chunk in enumerate(chunks):
        vo_file = f"/tmp/vo_{idx}_{int(time.time())}.mp3"
        cmd = [_edge_tts_bin(), "--voice", vo_voice, "--write-media", vo_file, "--text", chunk]
        subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if os.path.exists(vo_file):
            chunk_files.append(vo_file)
    if not chunk_files:
        raise Exception("Voiceover generation failed")

    final_file = chunk_files[0]
    if len(chunk_files) > 1:
        merged_file = f"/tmp/vo_merged_{int(time.time())}.mp3"
        concat_list = "/tmp/concat_list.txt"
        with open(concat_list, "w", encoding="utf-8") as handle:
            for cf in chunk_files:
                handle.write(f"file '{cf}'\n")
        subprocess.run(["ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", concat_list, "-c", "copy", merged_file], capture_output=True, text=True, timeout=60)
        final_file = merged_file if os.path.exists(merged_file) else chunk_files[0]

    if not hasattr(sp, "GOOGLE_DRIVE_MANAGER"):
        raise Exception("GOOGLE_DRIVE_MANAGER not available")

    safe_title = re.sub(r"[^\w\s-]", "", title or "Video")[:50]
    file_name = f"{safe_title}_{vo_lang}.mp3"
    file_id = sp.GOOGLE_DRIVE_MANAGER.upload_file(final_file, folder_id=target_folder_id, custom_name=file_name)

    for cf in chunk_files:
        try:
            os.unlink(cf)
        except Exception:
            pass
    if final_file not in chunk_files:
        try:
            os.unlink(final_file)
        except Exception:
            pass

    if not file_id:
        raise Exception("Drive upload failed")

    return [{
        "language": language,
        "drive_url": f"https://drive.google.com/file/d/{file_id}/view",
        "drive_file_id": file_id,
        "name": file_name,
        "drive_folder_id": target_folder_id,
        "drive_folder_name": target_folder_name,
        "drive_parent_folder_id": parent_folder_id,
    }]
