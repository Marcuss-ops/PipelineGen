import os
from typing import Optional, Tuple

import requests


def call_ollama(
    model: str,
    prompt: str,
    timeout: int = 300,
    max_tokens: int = 8000,
) -> Tuple[str, Optional[str]]:
    """Call Ollama using the non-streaming generate API."""
    ollama_url = os.environ.get("OLLAMA_ADDR", "http://localhost:11434")

    try:
        response = requests.post(
            f"{ollama_url}/api/generate",
            json={
                "model": model,
                "prompt": prompt,
                "stream": False,
                "options": {
                    "num_predict": max_tokens,
                    "temperature": 0.7,
                    "top_p": 0.9,
                    "repeat_penalty": 1.1,
                },
            },
            timeout=timeout,
        )
        if response.status_code != 200:
            return "", f"Error: {response.status_code}"

        try:
            data = response.json()
        except Exception:
            return response.text, None

        if isinstance(data, dict):
            if "response" in data:
                return data.get("response", ""), None
            if "model" in data:
                # Response has model key but missing response key - treat as error
                return "", f"Unexpected response format: missing 'response' field. Got: {data}"
        # If it's not a dict or unexpected format, treat as error
        return "", f"Unexpected response format from Ollama: {type(data).__name__}"
    except Exception as exc:
        return "", str(exc)
