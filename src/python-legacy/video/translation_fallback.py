import logging
import os
import subprocess
import sys
from typing import Callable, List, Optional

import requests


logger = logging.getLogger(__name__)


def _pip_install(package: str) -> bool:
    try:
        subprocess.run(
            [sys.executable, "-m", "pip", "install", package],
            check=True,
            capture_output=True,
            text=True,
        )
        return True
    except Exception as e:
        logger.warning(f"[TranslationFallback] pip install failed for {package}: {e}")
        return False


def _detect_lang(text: str) -> Optional[str]:
    try:
        from langdetect import detect
    except Exception:
        return None
    try:
        return detect(text)
    except Exception:
        return None


def _get_libretranslate_translator(
    source_language: str,
    target_language: str,
    status_callback: Optional[Callable[[str], None]] = None,
) -> Optional[Callable[[str], str]]:
    base_url = os.environ.get("LIBRETRANSLATE_URL", "").strip()
    if not base_url:
        return None
    url = base_url.rstrip("/") + "/translate"
    if status_callback:
        status_callback(f"[TranslationFallback] Using LibreTranslate: {base_url}")

    def _translate(text: str) -> str:
        payload = {
            "q": text,
            "source": source_language,
            "target": target_language,
            "format": "text",
        }
        resp = requests.post(url, json=payload, timeout=15)
        resp.raise_for_status()
        data = resp.json() or {}
        return data.get("translatedText") or text

    return _translate


def _get_argos_translator(
    source_language: str,
    target_language: str,
    status_callback: Optional[Callable[[str], None]] = None,
    allow_auto_install: bool = True,
    allow_model_download: bool = True,
) -> Optional[Callable[[str], str]]:
    try:
        import argostranslate.package  # type: ignore
        import argostranslate.translate  # type: ignore
    except Exception:
        if not allow_auto_install:
            return None
        if status_callback:
            status_callback("[TranslationFallback] Installing Argos Translate...")
        if not _pip_install("argostranslate"):
            return None
        try:
            import argostranslate.package  # type: ignore
            import argostranslate.translate  # type: ignore
        except Exception:
            return None

    src = source_language
    if src == "auto":
        # Default fallback; per-text detection will try to override.
        src = "en"

    if allow_model_download:
        try:
            packages = argostranslate.package.get_available_packages()
            pkg = next(
                (p for p in packages if p.from_code == src and p.to_code == target_language),
                None,
            )
            if pkg is None and source_language == "auto":
                # if auto, try to find any package to target and fallback to en->target
                pkg = next(
                    (p for p in packages if p.from_code == "en" and p.to_code == target_language),
                    None,
                )
                src = "en"
            if pkg is not None:
                if status_callback:
                    status_callback(
                        f"[TranslationFallback] Downloading Argos model {pkg.from_code}->{pkg.to_code}..."
                    )
                pkg_path = pkg.download()
                argostranslate.package.install_from_path(pkg_path)
        except Exception as e:
            logger.warning(f"[TranslationFallback] Argos model download failed: {e}")

    def _translate(text: str) -> str:
        nonlocal src
        if source_language == "auto":
            detected = _detect_lang(text)
            if detected:
                src = detected
        try:
            installed_languages = argostranslate.translate.get_installed_languages()
            from_lang = next((l for l in installed_languages if l.code == src), None)
            to_lang = next((l for l in installed_languages if l.code == target_language), None)
            if from_lang is None or to_lang is None:
                return text
            translation = from_lang.get_translation(to_lang)
            return translation.translate(text)
        except Exception:
            return text

    if status_callback:
        status_callback("[TranslationFallback] Using Argos Translate")
    return _translate


def get_fallback_translator(
    source_language: str,
    target_language: str,
    status_callback: Optional[Callable[[str], None]] = None,
    allow_auto_install: bool = True,
    allow_model_download: bool = True,
) -> Optional[Callable[[str], str]]:
    translator = _get_libretranslate_translator(
        source_language=source_language,
        target_language=target_language,
        status_callback=status_callback,
    )
    if translator:
        return translator
    return _get_argos_translator(
        source_language=source_language,
        target_language=target_language,
        status_callback=status_callback,
        allow_auto_install=allow_auto_install,
        allow_model_download=allow_model_download,
    )


def translate_texts_google_with_fallback(
    texts: List[str],
    source_language: str,
    target_language: str,
    status_callback: Optional[Callable[[str], None]] = None,
) -> List[str]:
    from deep_translator import GoogleTranslator

    translated: List[str] = []
    translator = GoogleTranslator(source=source_language, target=target_language)
    fallback_translator = None
    use_fallback = False

    for idx, text in enumerate(texts):
        if not text or not text.strip():
            translated.append(text)
            continue
        try:
            if not use_fallback:
                translated.append(translator.translate(text))
            else:
                if fallback_translator is None:
                    fallback_translator = get_fallback_translator(
                        source_language=source_language,
                        target_language=target_language,
                        status_callback=status_callback,
                    )
                if fallback_translator is None:
                    translated.append(text)
                else:
                    translated.append(fallback_translator(text))
        except Exception as e:
            logger.warning(f"[TranslationFallback] Google translate failed at {idx}: {e}")
            use_fallback = True
            if fallback_translator is None:
                fallback_translator = get_fallback_translator(
                    source_language=source_language,
                    target_language=target_language,
                    status_callback=status_callback,
                )
            if fallback_translator is None:
                translated.append(text)
            else:
                translated.append(fallback_translator(text))

    return translated
