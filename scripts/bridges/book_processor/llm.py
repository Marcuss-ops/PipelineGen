import hashlib
import json
from pathlib import Path

from scripts.core.ollama_client import generate as _ollama_generate
from .config import LANGUAGES


_DEFAULT_OPTIONS = {
    "temperature": 0.2,
    "num_predict": 4096,
    "num_ctx": 16384,
    "repeat_penalty": 1.3,
    "top_k": 40,
    "top_p": 0.9,
}


def get_cache_dir():
    cache_dir = Path(__file__).resolve().parent.parent.parent.parent / ".cache" / "book_summarizer"
    cache_dir.mkdir(parents=True, exist_ok=True)
    return cache_dir


def _cache_key(prompt: str, system_prompt: str | None, model: str, host: str) -> str:
    """Build a deterministic cache key from the request parameters."""
    raw = f"{model}|{host}|{system_prompt or ''}|{prompt}|{json.dumps(_DEFAULT_OPTIONS, sort_keys=True)}"
    return hashlib.md5(raw.encode("utf-8")).hexdigest()


def call_ollama(prompt, model="gemma4:e4b", system_prompt=None, host="http://127.0.0.1:11434",
                is_instruction_mode=False):
    """Call Ollama generate via shared client, with file-based cache."""
    # Check cache first
    key = _cache_key(prompt, system_prompt, model, host)
    cache_file = get_cache_dir() / f"{key}.txt"
    if cache_file.exists():
        try:
            with open(cache_file, "r", encoding="utf-8") as f:
                return f.read()
        except Exception:
            pass

    result = _ollama_generate(
        prompt=prompt,
        system=system_prompt,
        model=model,
        host=host,
        options=_DEFAULT_OPTIONS,
        timeout=300,
    )

    if result:
        try:
            with open(cache_file, "w", encoding="utf-8") as f:
                f.write(result)
        except Exception as e:
            print(f"  Warning: failed to write cache file: {e}")

    return result


def translate_text_ollama(text: str, target_language: str, model: str = "gemma4:e4b", fallback_model: str = "translategemma:4b", host: str = "http://127.0.0.1:11434") -> str:
    lang_name = LANGUAGES.get(target_language.lower(), target_language)

    system_prompt = (
        f"You are an expert, professional translator translating text to {lang_name}.\n"
        "MANDATORY RULES:\n"
        "1. Return ONLY the translated text. Absolutely NO conversational filler, greetings, or explanations.\n"
        "2. Do NOT say 'Here is the translated text', 'Okay', 'Revised Text', etc.\n"
        "3. Do NOT add meta-commentary, apologies, or formatting tags (like **Revised Text**).\n"
        "4. Preserve the original formatting and paragraphs.\n"
    )

    prompt = f"Translate the following text to {lang_name} directly:\n\n{text}"

    translated = call_ollama(prompt, model=model, system_prompt=system_prompt, host=host, is_instruction_mode=False)
    if not translated:
        print(f"    Translation with {model} failed, trying {fallback_model} fallback...")
        translated = call_ollama(prompt, model=fallback_model, system_prompt=system_prompt, host=host, is_instruction_mode=False)
    return translated or ""