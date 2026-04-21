import re
import logging

from .llm_client import call_ollama
from .youtube_transcript import extract_vtt_from_youtube, parse_vtt_to_text

logger = logging.getLogger(__name__)


def clean_script(text: str) -> str:
    text = re.sub(r"\[.*?\]", "", text or "")
    text = re.sub(r"\(.*?\)", "", text)
    text = re.sub(r"\*\*.*?\*\*", "", text)
    text = re.sub(r"\n{3,}", "\n\n", text)
    return text.strip()


def _sanitize_input(text: str, max_len: int = 50000) -> str:
    """Sanitize input to prevent prompt injection."""
    if len(text) > max_len:
        text = text[:max_len]
    # Remove excessive newlines that could disrupt prompt structure
    text = re.sub(r"\n{4,}", "\n\n\n", text)
    return text


def generate_script_from_text(source_text: str, title: str, language: str, duration: int) -> str:
    target_words = max(150, int(duration) * 140 // 60)  # ~140 wpm (average speech rate)

    # Sanitize inputs to prevent prompt injection
    source_text = _sanitize_input(source_text)
    title = _sanitize_input(title, max_len=500)

    # Truncate source text if too long, with warning
    max_chars = 12000
    if len(source_text) > max_chars:
        logger.warning(
            "Source text truncated from %d to %d characters for title: %s",
            len(source_text), max_chars, title,
        )
        source_text = source_text[:max_chars]

    prompt = (
        f"Generate a spoken YouTube script in {language}. "
        f"Title: {title}. Target words: {target_words}. "
        f"Use this source: {source_text}"
    )
    script, err = call_ollama("gemma3:12b", prompt, timeout=180, max_tokens=7000)
    if err:
        raise Exception(f"Ollama error: {err}")
    return clean_script(script)


def generate_script_from_youtube(youtube_url: str, title: str, language: str, duration: int) -> str:
    lang_code = (language or "en").split("-")[0].lower()
    vtt = extract_vtt_from_youtube(youtube_url, lang_code)
    if not vtt and lang_code != "en":
        vtt = extract_vtt_from_youtube(youtube_url, "en")
    if not vtt:
        return generate_script_from_text(title or youtube_url, title or "Video", language, duration)
    source_text = parse_vtt_to_text(vtt)
    return generate_script_from_text(source_text, title, language, duration)
