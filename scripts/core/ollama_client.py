#!/usr/bin/env python3
"""
Shared Ollama client — single source of truth for all LLM calls in PipelineGen.

Supports both /api/generate and /api/chat endpoints with consistent error
handling, timeout management, and JSON parsing.

Usage::

    from scripts.ollama_client import generate, chat, generate_json

    # Raw text generation (like book_processor)
    text = generate("Rewrite this: ...", system="You are a writer...",
                     options={"temperature": 0.2, "num_predict": 4096})

    # Chat-style generation (like agent_writer)
    text = chat([{"role": "user", "content": "..."}],
                options={"temperature": 0.35})

    # Structured JSON extraction (like semantic_tagger)
    data = generate_json("Return JSON with keys...",
                         cleanup_markdown=True)
"""

__all__ = ["generate", "chat", "generate_json", "OllamaError"]

import json
import re
import urllib.request
import urllib.error


# ── Defaults ───────────────────────────────────────────────────────────────

DEFAULT_OLLAMA_URL = "http://localhost:11434"
DEFAULT_MODEL = "gemma4:e4b"
DEFAULT_TIMEOUT = 300


# ── Custom exception ───────────────────────────────────────────────────────

class OllamaError(Exception):
    """Raised when Ollama returns an error or is unreachable."""
    pass


# ── Low-level request ──────────────────────────────────────────────────────

def _request(
    url_path: str,
    payload: dict,
    host: str = DEFAULT_OLLAMA_URL,
    timeout: int = DEFAULT_TIMEOUT,
) -> dict:
    """POST to Ollama endpoint and return the parsed JSON response."""
    url = f"{host.rstrip('/')}{url_path}"
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")[:500]
        raise OllamaError(f"HTTP {e.code} from {url_path}: {body}") from e
    except urllib.error.URLError as e:
        raise OllamaError(f"Connection failed to {host}{url_path}: {e.reason}") from e
    except TimeoutError as e:
        raise OllamaError(f"Timeout ({timeout}s) on {url_path}") from e
    except json.JSONDecodeError as e:
        raise OllamaError(f"Invalid JSON response from {url_path}: {e}") from e


# ── Generate (raw prompt + optional system) ────────────────────────────────

def generate(
    prompt: str,
    system: str | None = None,
    model: str = DEFAULT_MODEL,
    host: str = DEFAULT_OLLAMA_URL,
    images: list[str] | None = None,
    options: dict | None = None,
    timeout: int = DEFAULT_TIMEOUT,
) -> str:
    """
    Call /api/generate and return the response text.

    Parameters
    ----------
    prompt : str
        The user prompt / input text.
    system : str, optional
        System prompt prepended to the generation context.
    model : str
        Ollama model name (default: gemma4:e4b).
    host : str
        Ollama server URL (default: http://localhost:11434).
    images : list[str], optional
        Base64-encoded images for vision models.
    options : dict, optional
        Generation parameters (temperature, num_predict, num_ctx, etc.).
    timeout : int
        Request timeout in seconds (default: 300).

    Returns
    -------
    str
        The generated text, or empty string on error.
    """
    payload: dict = {
        "model": model,
        "prompt": prompt,
        "stream": False,
    }
    if system is not None:
        payload["system"] = system
    if images:
        payload["images"] = images
    if options:
        payload["options"] = options

    response = _request("/api/generate", payload, host=host, timeout=timeout)
    return response.get("response", "").strip()


# ── Chat (structured messages) ─────────────────────────────────────────────

def chat(
    messages: list[dict],
    model: str = DEFAULT_MODEL,
    host: str = DEFAULT_OLLAMA_URL,
    options: dict | None = None,
    timeout: int = DEFAULT_TIMEOUT,
) -> str:
    """
    Call /api/chat and return the response text.

    Parameters
    ----------
    messages : list[dict]
        Chat messages, e.g. [{"role": "user", "content": "..."}].
    model, host, options, timeout
        Same as :func:`generate`.

    Returns
    -------
    str
        The assistant response text, or empty string on error.
    """
    payload: dict = {
        "model": model,
        "messages": messages,
        "stream": False,
    }
    if options:
        payload["options"] = options

    response = _request("/api/chat", payload, host=host, timeout=timeout)
    return response.get("message", {}).get("content", "").strip()


# ── Generate JSON (structured output from /api/generate) ───────────────────

def generate_json(
    prompt: str,
    system: str | None = None,
    model: str = DEFAULT_MODEL,
    host: str = DEFAULT_OLLAMA_URL,
    options: dict | None = None,
    timeout: int = DEFAULT_TIMEOUT,
    cleanup_markdown: bool = True,
) -> dict:
    """
    Call /api/generate and parse the response as JSON.

    Parameters
    ----------
    prompt, system, model, host, options, timeout
        Same as :func:`generate`.
    cleanup_markdown : bool
        Strip markdown code fences (```json ... ```) before parsing (default: True).

    Returns
    -------
    dict
        Parsed JSON dict, or empty dict on failure.
    """
    text = generate(
        prompt, system=system, model=model, host=host,
        options=options, timeout=timeout,
    )
    if not text:
        return {}

    if cleanup_markdown:
        text = re.sub(r"^```(?:json)?\s*\n?", "", text)
        text = re.sub(r"\n?\s*```$", "", text)

    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return {}
