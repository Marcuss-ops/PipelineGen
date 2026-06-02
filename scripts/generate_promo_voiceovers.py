#!/usr/bin/env python3
"""
Generate promotional voiceovers in 10 languages.
- Translates text using Ollama (translategemma:4b or gemma3:4b)
- Generates voiceover using the voiceover API
- Uploads to Google Drive folder
"""

import argparse
import os
import sys
import json
import requests
from pathlib import Path

# Configuration
OLLAMA_URL = "http://localhost:11434"
VOICEOVER_API = "http://127.0.0.1:8081/api/media/voiceover/generate"

# 13 languages for voiceover
LANGUAGES = [
    ("en-US", "English", "English"),
    ("es-ES", "Spanish", "Spanish"),
    ("fr-FR", "French", "French"),
    ("de-DE", "German", "German"),
    ("it-IT", "Italian", "Italian"),
    ("pt-BR", "Portuguese", "Portuguese"),
    ("pl-PL", "Polish", "Polish"),
    ("nl-NL", "Dutch", "Dutch"),
    ("ja-JP", "Japanese", "Japanese"),
    ("ko-KR", "Korean", "Korean"),
    ("ru-RU", "Russian", "Russian"),
    ("tr-TR", "Turkish", "Turkish"),
    ("id-ID", "Indonesian", "Indonesian"),
]


def translate_text_ollama(text: str, target_language: str) -> str:
    """Translate text using Ollama with translategemma or gemma3:4b."""
    # Try translategemma model first, fall back to gemma3:4b
    model = "translategemma:4b"
    
    prompt = f"Translate to {target_language}. Return ONLY the translated text, nothing else.\n\nText: {text}"
    
    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {"temperature": 0.3}
    }
    
    try:
        response = requests.post(f"{OLLAMA_URL}/api/generate", json=payload, timeout=60)
        response.raise_for_status()
        result = response.json()
        translated = result.get("response", "").strip()
        
        # If empty, try gemma3:4b as fallback
        if not translated:
            payload["model"] = "gemma3:4b"
            response = requests.post(f"{OLLAMA_URL}/api/generate", json=payload, timeout=60)
            response.raise_for_status()
            result = response.json()
            translated = result.get("response", "").strip()
        
        return translated
    except Exception as e:
        print(f"    Translation error: {e}")
        return None


def generate_voiceover(text: str, language: str, filename: str, drive_folder_id: str) -> dict:
    """Generate voiceover via API and upload to Drive."""
    try:
        # Call the voiceover generate API
        # The API handles translation internally if needed, but we'll pass translated text
        response = requests.post(
            VOICEOVER_API,
            headers={"Content-Type": "application/json"},
            json={
                "text": text,
                "language": language,
                "filename": filename
            },
            timeout=120
        )
        response.raise_for_status()
        result = response.json()
        
        if result.get("result", {}).get("ok"):
            return {
                "success": True,
                "language": language,
                "filename": filename,
                "text": text[:80] + "..." if len(text) > 80 else text,
                "path": result["result"].get("path"),
                "drive_link": result["result"].get("drive_link"),
                "drive_file_id": result["result"].get("drive_file_id")
            }
        else:
            return {
                "success": False,
                "language": language,
                "filename": filename,
                "error": result.get("error", "Unknown error")
            }
    except Exception as e:
        return {
            "success": False,
            "language": language,
            "filename": filename,
            "error": str(e)
        }


def main():
    parser = argparse.ArgumentParser(description="Generate promotional voiceovers")
    parser.add_argument("--text", default="Hello friends, do you want to live a simple and prosperous life like ours? Look at this cash. My book will teach you how. Click the link in the description.", help="Text to voiceover")
    parser.add_argument("--drive-folder-id", default="1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A", help="Google Drive folder ID")
    parser.add_argument("--dry-run", action="store_true", help="Only translate, don't generate voiceovers")
    args = parser.parse_args()
    
    base_text = args.text
    drive_folder_id = args.drive_folder_id
    
    print("="*60)
    print("PROMO VOICEOVER GENERATOR")
    print("="*60)
    print(f"\nSource text: {base_text}")
    print(f"Drive folder: {drive_folder_id}")
    print(f"Mode: {'DRY RUN' if args.dry_run else 'LIVE'}\n")
    
    translations = []
    
    print("STEP 1: Translating text to 10 languages...")
    print("-"*60)
    
    for lang_code, lang_name, target_lang in LANGUAGES:
        print(f"\n[{lang_code}] {lang_name}")
        print(f"  Translating to {target_lang}...")
        
        translated = translate_text_ollama(base_text, target_lang)
        
        if translated:
            print(f"  ✓ Translated: {translated[:60]}..." if len(translated) > 60 else f"  ✓ Translated: {translated}")
            translations.append({
                "code": lang_code,
                "name": lang_name,
                "original": base_text,
                "translated": translated
            })
        else:
            print(f"  ✗ Translation failed, skipping")
    
    print("\n" + "="*60)
    print(f"TRANSLATION COMPLETE: {len(translations)}/10 languages")
    print("="*60)
    
    if args.dry_run:
        print("\nDry run - skipping voiceover generation")
        for t in translations:
            print(f"\n{t['code']} ({t['name']}):")
            print(f"  {t['translated']}")
        return
    
    print("\nSTEP 2: Generating voiceovers...")
    print("-"*60)
    
    results = []
    for t in translations:
        lang_code = t["code"]
        lang_name = t["name"]
        translated_text = t["translated"]
        
        filename = f"promo_{lang_code.replace('-', '_')}.mp3"
        print(f"\n[{lang_code}] {lang_name}")
        print(f"  Text: {translated_text[:60]}..." if len(translated_text) > 60 else f"  Text: {translated_text}")
        print(f"  Generating voiceover...")
        
        result = generate_voiceover(translated_text, lang_code, filename, drive_folder_id)
        
        if result["success"]:
            print(f"  ✓ Generated: {result.get('path', 'unknown')}")
            print(f"  ✓ Drive: {result.get('drive_link', 'unknown')}")
        else:
            print(f"  ✗ Error: {result.get('error', 'Unknown')}")
        
        results.append(result)
    
    print("\n" + "="*60)
    print("SUMMARY")
    print("="*60)
    
    success_count = sum(1 for r in results if r["success"])
    print(f"\nTotal: {success_count}/{len(results)} voiceovers generated successfully\n")
    
    for r in results:
        status = "✓" if r["success"] else "✗"
        print(f"{status} {r['language']} ({r['filename']})")
        if r["success"]:
            print(f"   Link: {r.get('drive_link', 'N/A')}")
        else:
            print(f"   Error: {r.get('error', 'Unknown')}")


if __name__ == "__main__":
    main()