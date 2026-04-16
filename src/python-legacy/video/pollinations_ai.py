"""Module for interacting with Pollinations AI API (OpenAI-compatible)."""

import json
import logging
import time
import random
import threading
import os
import re
import uuid
import math
import unicodedata
from collections import Counter
from contextlib import contextmanager
from threading import Semaphore
from typing import Dict, Any, List, Optional, Callable, Tuple
from openai import OpenAI
from groq import Groq
from modules.utils import prompts

logger = logging.getLogger(__name__)

# Provider disable flags to avoid repeated slow failures
_POLLINATIONS_DISABLED_REASON: Optional[str] = None
_GROQ_DISABLED_REASON: Optional[str] = None

# ───────────────────────────────────────────────────────────────────────────────
# Utilità per logging dettagliato
# ───────────────────────────────────────────────────────────────────────────────

def _trace() -> str:
    return uuid.uuid4().hex[:8]

def _mask(s: Optional[str]) -> str:
    if not s:
        return ""
    return s[:4] + "…" + s[-4:] if len(s) > 10 else "***"

def _exc_info(e: Exception) -> str:
    try:
        status = getattr(getattr(e, "response", None), "status_code", None)
        body = None
        if hasattr(e, "response") and getattr(e, "response") is not None:
            try:
                body = e.response.text
            except Exception:
                pass
        return f"status={status} body={body or repr(e)}"
    except Exception:
        return repr(e)

RETRYABLE_STATUS = {408, 409, 425, 429, 500, 502, 503, 504}
def _is_retryable(e: Exception) -> bool:
    try:
        resp = getattr(e, "response", None)
        s = getattr(resp, "status_code", None)
        if s is not None:
            return s in RETRYABLE_STATUS
        from httpx import ReadTimeout, ConnectTimeout, ReadError, ConnectError, RemoteProtocolError
        return isinstance(e, (ReadTimeout, ConnectTimeout, ReadError, ConnectError, RemoteProtocolError))
    except Exception:
        return False

def _decide_action(e: Exception) -> str:
    """Decide se abortire, ritentare o passare al prossimo endpoint/modello."""
    s = getattr(getattr(e, "response", None), "status_code", None)
    body = ""
    if hasattr(e, "response") and getattr(e, "response") is not None:
        try:
            body = e.response.text or ""
        except Exception:
            pass
    txt = (str(e) + " " + body).lower()
    # Caso tipico di Pollinations quando il path è sbagliato: "Invalid URL" -> non retryare, passa oltre
    if "invalid url" in txt:
        return "switch"
    if s in (400, 401, 403, 404):
        return "abort"
    if _is_retryable(e):
        return "retry"
    return "switch"

def _sleep_backoff(attempt: int, base: float = 0.5, cap: float = 3.0) -> None:
    delay = min(cap, base * (2 ** attempt)) * (0.6 + 0.8 * random.random())
    time.sleep(delay)

# Circuit breaker globale
_CIRCUIT = {"open": False, "opened_at": 0.0, "fail_count": 0}
_CIRCUIT_THRESHOLD = 3
_CIRCUIT_COOLDOWN_SEC = 60

def _cb_should_skip() -> bool:
    if not _CIRCUIT["open"]:
        return False
    return (time.time() - _CIRCUIT["opened_at"]) < _CIRCUIT_COOLDOWN_SEC

def _cb_on_fail():
    _CIRCUIT["fail_count"] += 1
    if _CIRCUIT["fail_count"] >= _CIRCUIT_THRESHOLD:
        _CIRCUIT.update({"open": True, "opened_at": time.time()})

def _cb_on_success():
    _CIRCUIT.update({"open": False, "fail_count": 0})

# Concurrency limiter
_SEM = Semaphore(4)
@contextmanager
def _slot():
    _SEM.acquire()
    try:
        yield
    finally:
        _SEM.release()

# Configure the Pollinations AI client with token if provided.
# Never hardcode secrets in the repo; use env vars instead.
def _read_first_line(path: str) -> Optional[str]:
    try:
        if not path:
            return None
        p = path.strip()
        if not p:
            return None
        with open(p, "r", encoding="utf-8", errors="ignore") as f:
            line = f.readline().strip()
        return line or None
    except Exception:
        return None

_API_KEY_FROM_FILE = _read_first_line(os.getenv("POLLINATIONS_API_KEY_FILE", ""))
API_KEY = (
    os.getenv("POLLINATIONS_API_KEY")  # Priorità a POLLINATIONS_API_KEY (più specifico)
    or os.getenv("POLLINATIONS_TOKEN")
    or _API_KEY_FROM_FILE
    or os.getenv("OPENAI_API_KEY")  # Fallback a OPENAI_API_KEY
    or "sk_YuAyvdUdG0BvEqONZALE7QmBhxcyiM6y"  # Chiave API predefinita valida
)

# Initialize client with detailed logging
try:
    client = OpenAI(
        api_key=API_KEY,
        # New unified OpenAI-compatible API (legacy text.pollinations.ai is deprecated)
        base_url=os.getenv("POLLINATIONS_BASE_URL", "").strip() or "https://gen.pollinations.ai/v1"
    )
    logger.info(f"✅ Client Pollinations inizializzato: {getattr(client, 'base_url', 'https://gen.pollinations.ai/v1')}, key={_mask(API_KEY)}")
except Exception as e:
    logger.warning(f"❌ Fallito client Pollinations: {_exc_info(e)}")
    client = None

# Constants
TEMPERATURE = 0.2
TOP_P = 1.0
MAX_TOKENS_OUT = 4000
MAX_CHARS_RESPONSE = 200_000

# Preferred models order with fallbacks
MODEL_CANDIDATES: List[str] = [
    # Based on https://gen.pollinations.ai/v1/models (as of 2026-01)
    "pollen",          # Modello primario richiesto
    "mistral",         # 13K context, $0.15/$0.35 per M - fallback
    "openai-fast",     # 11K context, $0.06/$0.44 per M - veloce ed economico
    "openai",          # 8K context, $0.15/$0.60 per M - fallback tradizionale
    "gemini-fast",     # 12K context, $0.1/$0.4 per M - alternativa economica
    "deepseek",        # 840 context, $0.58/$1.68 per M - fallback
    "claude-fast",     # 980 context, $1.0/$5.0 per M - ultimo fallback
]

def _load_groq_api_keys() -> List[str]:
    """
    Load Groq keys from environment.

    Supported:
    - GROQ_API_KEYS="k1,k2,k3" (comma/space/semicolon separated)
    - GROQ_API_KEY_1, GROQ_API_KEY_2, ...
    - GROQ_API_KEY (single key)
    """
    keys: List[str] = []
    packed = (os.getenv("GROQ_API_KEYS") or "").strip()
    if packed:
        for part in re.split(r"[,\s;]+", packed):
            p = part.strip()
            if p:
                keys.append(p)
    single = (os.getenv("GROQ_API_KEY") or "").strip()
    if single:
        keys.append(single)
    for i in range(1, 21):
        v = (os.getenv(f"GROQ_API_KEY_{i}") or "").strip()
        if v:
            keys.append(v)
    # Dedup while preserving order
    seen = set()
    out: List[str] = []
    for k in keys:
        if k not in seen:
            seen.add(k)
            out.append(k)
    return out

GROQ_API_KEYS: List[str] = _load_groq_api_keys()

# Log configuration status
if GROQ_API_KEYS:
    logger.info(f"✅ Configurate {len(GROQ_API_KEYS)} chiave(i) Groq (da variabili ambiente)")
else:
    logger.warning("⚠️ Nessuna chiave Groq configurata (fallback disabilitato)")

# Preferred Groq models
GROQ_MODELS: List[str] = [
    "llama-3.3-70b-versatile",  # Modello Groq principale aggiornato
    "llama-3.1-70b-versatile",  # Secondo modello robusto
    "llama3-70b-8192",          # Terzo modello fallback
    "mixtral-8x7b-32768"        # Ultimo fallback
]

# Initialize Groq client
groq_client = None
if Groq and GROQ_API_KEYS:
    try:
        groq_client = Groq(api_key=GROQ_API_KEYS[0])
        logger.info("✅ Groq client inizializzato")
        logger.info(f"🎯 Modello Groq predefinito: {GROQ_MODELS[0]}")
    except Exception as e:
        logger.warning(f"Failed to initialize Groq client: {e}")
        groq_client = None
else:
    if not Groq:
        logger.warning("Groq library non installata")
    if not GROQ_API_KEYS:
        logger.warning("Nessuna chiave API Groq presente nel codice")

# --- Text chunking helpers ---
def _split_text_into_chunks(text: str, soft_limit: int = 6000, hard_limit: int = 6500) -> List[str]:
    """Split text into chunks respecting soft/hard character limits, cutting on whitespace when possible."""
    if not text:
        return []
    n = len(text)
    if n <= soft_limit:
        return [text]
    chunks: List[str] = []
    i = 0
    while i < n:
        end = min(i + hard_limit, n)
        segment = text[i:end]
        # try to cut at last whitespace near soft_limit within segment
        cut_pos = min(soft_limit, len(segment))
        if len(segment) > soft_limit:
            ws_idx = -1
            # search backwards from soft_limit for whitespace to avoid splitting words
            for j in range(soft_limit, max(soft_limit - 1500, 0), -1):
                if segment[j - 1:j].isspace():
                    ws_idx = j
                    break
            cut_pos = ws_idx if ws_idx != -1 else soft_limit
        chunk = segment[:cut_pos]
        chunks.append(chunk)
        i += len(chunk)
    return chunks

def _distribute_counts(total: int, parts: int) -> List[int]:
    """Distribute total items across parts as evenly as possible (earlier parts get the remainder)."""
    if parts <= 0:
        return []
    base = max(total // parts, 0)
    rem = max(total % parts, 0)
    return [base + (1 if i < rem else 0) for i in range(parts)]

def _merge_dedup(sequences: List[List[str]], max_items: Optional[int] = None) -> List[str]:
    """Merge lists preserving order and removing duplicates (case-insensitive)."""
    seen = set()
    out: List[str] = []
    for seq in sequences:
        for item in seq:
            key = item.strip().lower()
            if key and key not in seen:
                seen.add(key)
                out.append(item)
                if max_items is not None and len(out) >= max_items:
                    return out
    return out

def _extract_json(s: str) -> str:
    """Estrae il blocco JSON più plausibile da una stringa (supporta ```json ... ```)."""
    if not isinstance(s, str):
        return s
    m = re.search(r"```json\s*(.*?)\s*```", s, re.DOTALL | re.IGNORECASE)
    if m:
        return m.group(1)
    start = s.find("{")
    end = s.rfind("}")
    return s[start:end + 1] if start != -1 and end != -1 and end > start else s


# ───────────────────────────────────────────────────────────────────────────────
# Offline (free) extraction fallback (no network)
# ───────────────────────────────────────────────────────────────────────────────

_STOPWORDS: Dict[str, set] = {
    "it": {
        "a", "ad", "al", "allo", "ai", "agli", "alla", "alle", "anche", "ancora", "che", "chi", "ci", "coi", "come",
        "con", "contro", "da", "dal", "dallo", "dai", "dagli", "dalla", "dalle", "de", "degli", "della", "delle",
        "dei", "del", "dello", "di", "e", "è", "era", "erano", "essere", "fa", "fra", "già", "gli", "ha", "hai",
        "hanno", "ho", "il", "in", "io", "la", "le", "lei", "lo", "loro", "lui", "ma", "me", "mi", "ne",
        "nel", "nello", "nella", "nelle", "nei", "noi", "non", "o", "per", "perché", "più", "poi", "prima", "quale",
        "quali", "quando", "quasi", "questo", "questa", "quelli", "quello", "se", "senza", "si", "sia", "sono",
        "su", "sul", "sullo", "sulla", "sulle", "sui", "tra", "tu", "un", "una", "uno", "vi", "voi",
    },
    "en": {
        "a", "an", "and", "are", "as", "at", "be", "by", "but", "for", "from", "has", "have", "he", "her", "his",
        "i", "in", "into", "is", "it", "its", "me", "my", "no", "not", "of", "on", "or", "our", "she", "so",
        "that", "the", "their", "them", "then", "there", "these", "they", "this", "to", "was", "we", "were", "with",
        "you", "your",
    },
    "es": {"a", "al", "algo", "como", "con", "de", "del", "el", "ella", "ellas", "ellos", "en", "es", "esta", "este", "la", "las", "lo", "los", "no", "o", "para", "por", "que", "se", "su", "sus", "un", "una", "y"},
    "fr": {"à", "a", "au", "aux", "avec", "ce", "ces", "dans", "de", "des", "du", "elle", "en", "et", "eux", "il", "je", "la", "le", "les", "leur", "lui", "ma", "mais", "me", "même", "mes", "moi", "mon", "ne", "nos", "notre", "nous", "on", "ou", "par", "pas", "pour", "qu", "que", "qui", "sa", "se", "ses", "son", "sur", "ta", "te", "tes", "toi", "ton", "tu", "un", "une", "vos", "votre", "vous", "y"},
    "de": {"aber", "als", "am", "an", "auch", "auf", "aus", "bei", "bin", "bis", "bist", "da", "dadurch", "daher", "darum", "das", "dass", "dein", "deine", "dem", "den", "der", "des", "die", "dies", "dieser", "dieses", "doch", "du", "durch", "ein", "eine", "einem", "einen", "einer", "eines", "er", "es", "euer", "eure", "für", "hatte", "hatten", "hattest", "hattet", "hier", "hinter", "ich", "ihr", "ihre", "im", "in", "ist", "ja", "jede", "jedem", "jeden", "jeder", "jedes", "jener", "jenes", "jetzt", "kann", "kannst", "können", "könnt", "machen", "mein", "meine", "mit", "muß", "mußt", "musst", "müssen", "müßt", "nach", "nachdem", "nein", "nicht", "nun", "oder", "seid", "sein", "seine", "sich", "sie", "sind", "soll", "sollen", "sollst", "sollt", "sonst", "soweit", "sowie", "und", "unser", "unsere", "unter", "vom", "von", "vor", "wann", "warum", "was", "weiter", "weitere", "wenn", "wer", "werde", "werden", "werdet", "weshalb", "wie", "wieder", "wieso", "wir", "wird", "wirst", "wo", "woher", "wohin", "zu", "zum", "zur", "über"},
    "pt": {"a", "ao", "aos", "as", "com", "da", "das", "de", "do", "dos", "e", "ela", "elas", "ele", "eles", "em", "entre", "era", "eram", "essa", "esse", "esta", "este", "eu", "foi", "foram", "há", "isso", "isto", "já", "la", "lá", "mais", "mas", "me", "mesmo", "meu", "minha", "muito", "na", "nas", "não", "no", "nos", "nós", "o", "os", "ou", "para", "por", "que", "se", "sem", "ser", "seu", "sua", "são", "também", "te", "tem", "têm", "um", "uma", "você", "vocês"},
}

def _normalize_lang(lang: str) -> str:
    if not lang:
        return "it"
    return (lang or "").split("-")[0].split("_")[0].lower().strip() or "it"

def _split_sentences(text: str) -> List[str]:
    if not text:
        return []
    parts = re.split(r"(?<=[.!?])\s+", text.strip())
    return [p.strip() for p in parts if p and p.strip()]

def _tokenize_words(text: str) -> List[str]:
    if not text:
        return []
    return re.findall(r"[A-Za-zÀ-ÖØ-öø-ÿ0-9][A-Za-zÀ-ÖØ-öø-ÿ0-9'’_-]*", text)

def _extract_keywords_offline(text: str, lang: str, limit: int) -> List[str]:
    lang = _normalize_lang(lang)
    stop = _STOPWORDS.get(lang, set())
    words = _tokenize_words(text)
    filtered: List[str] = []
    for w in words:
        lw = w.lower()
        if lw in stop:
            continue
        if len(lw) < 3:
            continue
        if lw.isdigit():
            continue
        filtered.append(lw)

    counts = Counter(filtered)
    scored = sorted(counts.items(), key=lambda kv: (kv[1], len(kv[0])), reverse=True)
    out: List[str] = []
    for w, _c in scored:
        out.append(w)
        if len(out) >= max(0, int(limit)):
            break
    return out

def _extract_capitalized_phrases(text: str, limit: int) -> List[str]:
    if not text:
        return []
    pattern = r"\b(?:[A-ZÀ-ÖØ-Þ][\w'’.-]{1,}|[A-Z]{2,})(?:\s+(?:of|de|di|da|del|della|la|le|el|los|las|van|von|[A-ZÀ-ÖØ-Þ][\w'’.-]{1,}|[A-Z]{2,}))*\b"
    matches = re.findall(pattern, text)
    cleaned: List[str] = []
    for m in matches:
        s = (m or "").strip()
        if len(s) <= 2:
            continue
        cleaned.append(s)
    counts = Counter([c.lower() for c in cleaned])
    unique: Dict[str, str] = {}
    for c in cleaned:
        key = c.lower()
        if key not in unique:
            unique[key] = c
    scored = sorted(unique.items(), key=lambda kv: (counts.get(kv[0], 1), len(kv[1])), reverse=True)
    out = [v for _k, v in scored[: max(0, int(limit))]]
    return out

def _score_sentence(sentence: str) -> float:
    if not sentence:
        return 0.0
    length = len(sentence)
    nums = len(re.findall(r"\d", sentence))
    caps = len(re.findall(r"\b[A-ZÀ-ÖØ-Þ][a-zà-öø-ÿ]{2,}\b", sentence))
    length_score = math.log(1 + min(220, length))
    return length_score + 0.15 * nums + 0.10 * caps

def _extract_important_sentences_offline(text: str, limit: int) -> List[str]:
    sentences = _split_sentences(text)
    if not sentences:
        return []
    candidates = [s.strip() for s in sentences if 25 <= len(s.strip()) <= 280]
    if not candidates:
        candidates = sentences[:]
    scored = sorted(candidates, key=_score_sentence, reverse=True)
    out: List[str] = []
    seen = set()
    for s in scored:
        key = s.strip().lower()
        if key in seen:
            continue
        seen.add(key)
        out.append(s.strip())
        if len(out) >= max(0, int(limit)):
            break
    return out

def _best_mention_in_text(text: str, entity: str) -> Optional[str]:
    if not text or not entity:
        return None
    m = re.search(re.escape(entity), text, flags=re.IGNORECASE)
    if m:
        return text[m.start():m.end()]

    tokens = [t for t in _tokenize_words(entity) if len(t) >= 4]
    if not tokens:
        return None
    tokens = sorted(tokens, key=len, reverse=True)
    for t in tokens[:3]:
        m2 = re.search(re.escape(t), text, flags=re.IGNORECASE)
        if m2:
            return text[m2.start():m2.end()]
    return None

def _associate_entities_to_urls_offline(text: str, entities_to_urls: Dict[str, str]) -> Dict[str, str]:
    out: Dict[str, str] = {}
    for entity, url in (entities_to_urls or {}).items():
        mention = _best_mention_in_text(text, entity) or entity
        out[mention] = url
    return out

# Language mapping for Pollinations AI extraction
language_mapping = {
    'it': ('Italiano', 'Rispondi sempre in italiano. Fornisci output in formato JSON valido.'),
    'en': ('English', 'Always respond in English. Provide output in valid JSON format.'),
    'es': ('Español', 'Responde siempre en español. Proporciona salida en formato JSON válido.'),
    'fr': ('Français', 'Répondez toujours en français. Fournissez une sortie au format JSON valide.'),
    'de': ('Deutsch', 'Antworten Sie immer auf Deutsch. Geben Sie die Ausgabe im gültigen JSON-Format an.'),
    'pt': ('Português', 'Responda sempre em português. Forneça saída em formato JSON válido.'),
    'ru': ('Русский', 'Всегда отвечайте на русском языке. Предоставьте вывод в действительном формате JSON.'),
    'ja': ('日本語', '常に日本語で回答してください。有効なJSON形式で出力を提供してください。'),
    'ko': ('한국어', '항상 한국어로 응답하세요. 유효한 JSON 형식으로 출력을 제공하세요.'),
    'zh': ('中文', '始终用中文回答。以有效的JSON格式提供输出。'),
    'ar': ('العربية', 'أجب دائماً باللغة العربية. قدم الإخراج بتنسيق JSON صالح.'),
    'hi': ('हिन्दी', 'हमेशा हिंदी में उत्तर दें। वैध JSON प्रारूप में आउटपुट प्रदान करें।')
}

def get_language_info(language_code: str) -> tuple:
    return language_mapping.get(language_code.lower(), language_mapping['it'])

# Basic cleanup for sentence-like outputs from LLMs.
def _clean_frase_importante(frase: str) -> str:
    s = (frase or "").strip()
    if not s:
        return ""
    # Remove common heading prefixes produced by LLMs.
    s = re.sub(r"^(important|key)\s+concepts?\s*:\s*", "", s, flags=re.IGNORECASE)
    s = re.sub(r"^concetti\s+importanti\s*:\s*", "", s, flags=re.IGNORECASE)
    s = re.sub(r"^concepts?\s*:\s*", "", s, flags=re.IGNORECASE)
    s = re.sub(r"^frasi?\s+importanti\s*:\s*", "", s, flags=re.IGNORECASE)
    s = re.sub(r"^[-•]+\s*", "", s)
    return s.strip()

def _normalize_entity_text(value: str, single_word: bool = False) -> str:
    s = (value or "").replace("\uFFFD", "")
    if not s:
        return ""
    s = unicodedata.normalize("NFKC", s)
    # Drop control characters and normalize whitespace
    s = "".join(ch for ch in s if not unicodedata.category(ch).startswith("C"))
    s = re.sub(r"\s+", " ", s).strip()
    if not s:
        return ""
    # Strip common wrapping punctuation/quotes
    s = s.strip(" \t\r\n\"'`“”‘’[]{}()<>•*-–—.,;:|/\\")
    s = re.sub(r"\s+", " ", s).strip()
    if not s:
        return ""
    if single_word:
        s = s.split(" ")[0].strip(" \t\r\n\"'`“”‘’[]{}()<>•*-–—.,;:|/\\")
    return s

def _normalize_entity_map(data: Dict[str, Any]) -> Dict[str, Any]:
    cleaned: Dict[str, Any] = {}
    for raw_key, raw_val in (data or {}).items():
        if not isinstance(raw_key, str):
            continue
        key = _normalize_entity_text(raw_key, single_word=False)
        if not key:
            continue
        cleaned[key] = raw_val
    return cleaned

def _is_valid_frase_importante(frase: str, min_words: int = 5, max_words: int = 20) -> bool:
    s = (frase or "").strip()
    if not s:
        return False
    low = s.lower().strip(":").strip()
    # Drop heading-like or generic outputs.
    if low in {"important concept", "important concepts", "key concept", "key concepts", "concetti importanti"}:
        return False
    words = [w for w in re.split(r"\s+", s) if w]
    if len(words) < min_words or len(words) > max_words:
        return False
    return True

# Prompt templates for entity extraction
PROMPT_NOMI_SPECIALI = """
Analista testi {lingua_testo}. {istruzioni_lingua}

Estrai {numero_entita} nomi speciali dal testo:

REGOLE:
1. SOLO nomi propri, persone, luoghi, organizzazioni, termini tecnici
2. NO parole comuni/aggettivi/verbi
3. Forma originale
4. Ordina per importanza

TESTO:
{testo}

Rispondi SOLO array JSON:
"""

PROMPT_FRASI_IMPORTANTI = """
Analista testi {lingua_testo}. {istruzioni_lingua}

Estrai {numero_entita} frasi importanti:

REGOLE:
1. Frasi complete (5-15 parole)
2. Rilevanti e informative
3. NO nomi già estratti
4. Significato originale
5. Ordina per importanza

TESTO:
{testo}

Rispondi SOLO array JSON:
"""

PROMPT_PAROLE_IMPORTANTI = """
Analista testi {lingua_testo}. {istruzioni_lingua}

Estrai {numero_entita} parole chiave:

REGOLE:
1. SOLO singole parole
2. NO parole comuni/articoli
3. NO nomi già estratti: {nomi_speciali_esclusi}
4. Termini tecnici/concetti chiave
5. Forma originale
6. Ordina per rilevanza
7. Assicurati che le parole estratte siano diverse da quelle nella lista esclusa

FORMATO OUTPUT: Array JSON (lunghezza ≤ {numero_entita})

TESTO DA ANALIZZARE:
{testo}

Rispondi SOLO con l'array JSON:
"""

PROMPT_ENTITA_PER_IMMAGINI = """
Sei un esperto analista di testi in {lingua_testo}. {istruzioni_lingua}

Associsci ogni entità fornita alla corrispondente immagine dal catalogo fornito, basandoti sul contenuto del testo.

CATALOGO ENTITÀ CON IMMAGINI:
{entita_json}

REGOLE PRINCIPALI:
1. ANALIZZA il testo fornito per trovare ogni entità nel catalogo
2. Ogni volta che trovi un'entità nel testo, usa l'URL immagine ESATTO fornito nel catalogo
3. NON cercare nuovi URL - usa SOLO gli URL dal catalogo fornito
4. TROVA TUTTE le entità menzionate nel testo (anche con variazioni minori)
5. Se un'entità del catalogo NON viene trovata nel testo, restituisci comunque l'URL originale
6. Mantieni TUTTE le coppie entità-URL dal catalogo nel risultato finale

TESTO DA ANALIZZARE:
{testo}

FORMATO OUTPUT: Oggetto JSON {{"entità": "url", ...}}; usa SOLO le entità del catalogo.
"""

def chat_with_pollinations(messages: List[Dict[str, str]], model: str = "pollen") -> Optional[str]:
    """
    Send a chat request to Pollinations AI and return the response content.
    Includes concurrency limiting and detailed logging.
    """
    if not client:
        logger.error("❌ No Pollinations client available")
        return None

    tid = _trace()
    logger.info(f"[{tid}] Pollinations request start; model={model}")

    with _slot():
        try:
            start = time.time()
            # Some upstream providers behind Pollinations may not accept custom sampling params.
            # We start with them, then fall back to omitting them if needed.
            kwargs = {"model": model, "messages": messages, "max_tokens": MAX_TOKENS_OUT}
            if os.environ.get("POLLINATIONS_DISABLE_SAMPLING_PARAMS", "").strip() != "1":
                kwargs.update({"temperature": TEMPERATURE, "top_p": TOP_P})
            try:
                response = client.chat.completions.create(**kwargs)
            except Exception as e:
                msg = _exc_info(e).lower()
                if "temperature" in msg and ("unsupported" in msg or "does not support" in msg):
                    kwargs.pop("temperature", None)
                    kwargs.pop("top_p", None)
                    response = client.chat.completions.create(**kwargs)
                else:
                    raise
            dur = int((time.time() - start) * 1000)
            content = response.choices[0].message.content if response and response.choices else ""
            content = (content or "")[:MAX_CHARS_RESPONSE]  # MAX_CHARS_RESPONSE non definito, ma nel codice originale c'è
            logger.info(f"[{tid}] ok model={model} in {dur}ms len={len(content)}")
            return content
        except Exception as e:
            msg = _exc_info(e)
            logger.warning(f"[{tid}] fail model={model}: {msg}")
            # Non rilanciamo per permettere fallback
            return None

# ───────────────────────────────────────────────────────────────────────────────
# Core: Pollinations con fallback Groq
# ───────────────────────────────────────────────────────────────────────────────

def send_json_prompt_with_groq_fallback(prompt: str, model: str = "pollen", max_retries: int = 3) -> Optional[Any]:
    """
    Send a prompt using Pollinations AI with Groq fallback if Pollinations fails.
    This provides robust JSON prompt parsing with multiple fallback layers.
    """
    global _GROQ_DISABLED_REASON
    tid = _trace()
    logger.info(f"[{tid}] Starting JSON prompt with Groq fallback")

    # Prima prova Pollinations AI
    result = send_json_prompt_to_pollinations(prompt, model, max_retries)
    if result is not None:
        logger.info(f"[{tid}] SUCCESS from Pollinations AI")
        return result

    logger.warning(f"[{tid}] Pollinations AI failed, trying Groq fallback")

    # Fallback su Groq se Pollinations fallisce
    if os.environ.get("DISABLE_GROQ_FALLBACK", "").strip() == "1":
        logger.warning(f"[{tid}] Groq fallback disabled via DISABLE_GROQ_FALLBACK=1")
        return None
    if _GROQ_DISABLED_REASON:
        logger.warning(f"[{tid}] Groq fallback disabled: {_GROQ_DISABLED_REASON}")
        return None
    if groq_client and GROQ_API_KEYS:
        for key_idx, api_key in enumerate(GROQ_API_KEYS):
            groq_client_fallback = Groq(api_key=api_key)
            for model_candidate in GROQ_MODELS:
                logger.info(f"[{tid}] Trying Groq fallback with key {key_idx+1}, model {model_candidate}")
                for attempt in range(max_retries):
                    try:
                        start = time.time()
                        messages = [{"role": "user", "content": prompt}]
                        response = groq_client_fallback.chat.completions.create(
                            model=model_candidate,
                            messages=messages,
                            temperature=TEMPERATURE,
                            top_p=TOP_P,
                            max_tokens=MAX_TOKENS_OUT,
                        )
                        dur = int((time.time() - start) * 1000)
                        content = response.choices[0].message.content if response and response.choices else ""
                        content = (content or "")[:MAX_CHARS_RESPONSE]

                        logger.info(f"[{tid}] Groq response in {dur}ms, len={len(content)}")

                        if content and content.strip():
                            logger.info(f"[{tid}] Got response from Groq {model_candidate}: {content[:100]}...")

                            # Extract JSON from markdown code blocks if present
                            json_match = re.search(r'```json\s*(.*?)\s*```', content, re.DOTALL | re.IGNORECASE)
                            if json_match:
                                json_str = json_match.group(1).strip()
                            else:
                                json_str = content.strip()

                            # Try to parse JSON
                            try:
                                parsed = json.loads(json_str)
                                # If the response is a single string instead of array, wrap it in a list
                                if isinstance(parsed, str):
                                    parsed = [parsed]
                                logger.info(f"[{tid}] SUCCESS from Groq {model_candidate}")
                                return parsed
                            except json.JSONDecodeError as je:
                                logger.warning(f"[{tid}] Groq JSON parse error: {je}")
                                continue  # Try next attempt/model

                        else:
                            logger.warning(f"[{tid}] Empty response from Groq {model_candidate}")

                    except Exception as e:
                        msg = _exc_info(e)
                        logger.warning(f"[{tid}] Groq attempt {attempt + 1} failed: {msg}")
                        # Avoid retry storms if account/org is blocked
                        if "organization_restricted" in msg or "org" in msg and "restricted" in msg:
                            _GROQ_DISABLED_REASON = "organization_restricted"
                            logger.error(f"[{tid}] Groq disabled: organization_restricted")
                            break
                        if attempt < max_retries - 1:
                            _sleep_backoff(attempt)
                if _GROQ_DISABLED_REASON:
                    break
            if _GROQ_DISABLED_REASON:
                break

    logger.error(f"[{tid}] ALL methods failed (Pollinations + Groq)")
    return None

def _is_pollinations_notice(text: str) -> bool:
    t = (text or "").lower()
    return (
        "important notice" in t
        or "legacy" in t and "deprecated" in t
        or "this is our legacy api" in t
        or "enter.pollinations.ai" in t
    )

def send_json_prompt_to_pollinations(prompt: str, model: str = "pollen", max_retries: int = 3) -> Optional[Any]:
    """
    Send a prompt to Pollinations AI and attempt to parse the response as JSON.
    Tries multiple models in order if the primary one fails.
    """
    global _POLLINATIONS_DISABLED_REASON
    if _cb_should_skip():
        logger.warning("Circuit breaker active, skipping Pollinations AI")
        return None
    if _POLLINATIONS_DISABLED_REASON:
        logger.warning(f"Pollinations disabled: {_POLLINATIONS_DISABLED_REASON}")
        return None

    # Validate prompt length to prevent API errors
    if len(prompt) > 6500:
        logger.warning(f"Prompt too long ({len(prompt)} chars), truncating to 6500 chars")
        prompt = prompt[:6500] + "..."

    tid = _trace()
    attempts: List[Dict[str, Any]] = []
    logger.info(f"[{tid}] Pollinations JSON prompt start; models={len(MODEL_CANDIDATES)+1}, prompt_len={len(prompt)}")

    # Try all models in preference order
    models_to_try = [model] + MODEL_CANDIDATES
    models_to_try = list(dict.fromkeys(models_to_try))  # Remove duplicates while preserving order

    with _slot():
        for model_candidate in models_to_try:
            logger.info(f"[{tid}] Trying Pollinations model: {model_candidate}")
            for attempt in range(max_retries):
                try:
                    start = time.time()
                    messages = [{"role": "user", "content": prompt}]
                    kwargs = {"model": model_candidate, "messages": messages, "max_tokens": MAX_TOKENS_OUT}
                    if os.environ.get("POLLINATIONS_DISABLE_SAMPLING_PARAMS", "").strip() != "1":
                        kwargs.update({"temperature": TEMPERATURE, "top_p": TOP_P})
                    try:
                        response = client.chat.completions.create(**kwargs)
                    except Exception as e:
                        msg = _exc_info(e).lower()
                        if "temperature" in msg and ("unsupported" in msg or "does not support" in msg):
                            kwargs.pop("temperature", None)
                            kwargs.pop("top_p", None)
                            response = client.chat.completions.create(**kwargs)
                        else:
                            raise
                    dur = int((time.time() - start) * 1000)
                    content = response.choices[0].message.content if response and response.choices else ""
                    content = (content or "")[:MAX_CHARS_RESPONSE]

                    attempts.append({"model": model_candidate, "attempt": attempt+1, "status": "success", "duration_ms": dur})
                    logger.info(f"[{tid}] Pollinations ok model={model_candidate} in {dur}ms len={len(content)}")

                    if content and content.strip():
                        logger.info(f"[{tid}] Got response from {model_candidate}: {content[:100]}...")
                        if _is_pollinations_notice(content):
                            _POLLINATIONS_DISABLED_REASON = "pollinations_legacy_notice_or_deprecated"
                            logger.error(f"[{tid}] Pollinations appears deprecated/blocked; disabling provider for this run")
                            _cb_on_fail()
                            return None

                        # Extract JSON from markdown code blocks if present
                        json_match = re.search(r'```json\s*(.*?)\s*```', content, re.DOTALL | re.IGNORECASE)
                        if json_match:
                            json_str = json_match.group(1).strip()
                        else:
                            json_str = content.strip()

                        try:
                            parsed = json.loads(json_str)
                            # If the response is a single string instead of array, wrap it in a list
                            if isinstance(parsed, str):
                                parsed = [parsed]
                            _cb_on_success()
                            logger.info(f"[{tid}] JSON parsed successfully from {model_candidate}")
                            return parsed
                        except json.JSONDecodeError as je:
                            logger.warning(f"[{tid}] JSON parse error: {je}")
                            continue  # Try next attempt

                    else:
                        logger.warning(f"[{tid}] Empty response from {model_candidate}")
                        continue

                except Exception as e:
                    msg = _exc_info(e)
                    attempts.append({"model": model_candidate, "attempt": attempt+1, "status": "error", "error": msg})
                    logger.warning(f"[{tid}] Pollinations fail model={model_candidate} attempt={attempt+1}: {msg}")

                    action = _decide_action(e)
                    if action == "abort":
                        _cb_on_fail()
                        break
                    elif action == "retry":
                        _cb_on_fail()
                        _sleep_backoff(attempt)
                    else:  # switch
                        _cb_on_fail()
                        break  # Try next model

            # If all retries for this model failed, move to next model

    logger.warning(f"[{tid}] All Pollinations models failed, attempts={len(attempts)}")
    return None

def create_text_chunks(text: str, max_size: int = 85000) -> List[str]:
    """Divide il testo in chunk preservando frasi ove possibile. 
    Ottimizzato per mistral (13K token): chunk size 85K caratteri
    per massimizzare l'utilizzo della capacità con margine di sicurezza."""
    if len(text) <= max_size:
        return [text]

    chunks = []
    sentences = re.split(r'([.!?]+)\s+', text)
    current = ""

    for i in range(0, len(sentences), 2):
        sentence = sentences[i].strip()
        punct = sentences[i+1] if i + 1 < len(sentences) else ""
        piece = (sentence + punct).strip()

        if not piece:
            continue

        if len(current) + len(piece) + 1 <= max_size:
            current = (current + " " + piece).strip()
        else:
            if current:
                chunks.append(current)
            current = piece

    if current:
        chunks.append(current)

    return chunks

def estrai_annotazioni_da_pollinations(
    testo: str,
    lingua: str = "it",
    numero_entita: Optional[int] = None,  # None = usa prompts.NUMERO_ENTITA (sempre aggiornato)
    entita_immagini_json_input: Optional[Dict[str, Any]] = None,
    max_retries: int = 3,
    status_callback: Optional[Callable[[str, bool], None]] = None
) -> Dict[str, Any]:
    # IMPORTANTE: Usa sempre prompts.NUMERO_ENTITA direttamente per evitare problemi di cache
    if numero_entita is None:
        from modules.utils import prompts  # Import locale per ottenere sempre la versione aggiornata
        numero_entita = prompts.NUMERO_ENTITA
    """
    Estrae annotazioni e associazioni da testo utilizzando Pollinations AI.
    Restituisce un dizionario con i risultati delle estrazioni.
    """
    if status_callback is None:
        status_callback = lambda msg, is_error=False: None

    tid = _trace()
    start_time = time.time()
    force_offline = os.environ.get("FORCE_OFFLINE_ENTITY_EXTRACTION", "").strip() == "1"

    lingua_nome, istruzioni_lingua = get_language_info(lingua)
    status_callback(f"🤖 Avvio estrazione Pollinations AI in {lingua_nome}", False)
    logger.info(f"[{tid}] Pollinations extraction start; lang={lingua}, entities={numero_entita}, text_len={len(testo)}")

    results = {
        "Nomi_Speciali_Pollinations": [],
        "Frasi_Importanti_Pollinations": [],
        "Parole_Importanti_Pollinations": [],
        "Associazione_Entita_Immagini_Pollinations": {}
    }

    text_chunks = create_text_chunks(testo)
    status_callback(f"📝 Testo diviso in {len(text_chunks)} chunk", False)
    logger.info(f"[{tid}] Text chunked: {len(text_chunks)} chunks, chunk_size_avg={len(testo)//max(1,len(text_chunks))}")

    # Estrazione Nomi Speciali
    status_callback("🔍 Elaborazione Nomi Speciali…", False)
    all_nomi_speciali = []

    entities_per_chunk = max(1, numero_entita // max(1, len(text_chunks)))

    for chunk in text_chunks:
        remaining = numero_entita - len(all_nomi_speciali)
        if remaining <= 0:
            break

        quota = min(entities_per_chunk, remaining)

        prompt = PROMPT_NOMI_SPECIALI.format(
            lingua_testo=lingua_nome,
            istruzioni_lingua=istruzioni_lingua,
            numero_entita=quota,
            testo=chunk
        )

        res = None if force_offline else send_json_prompt_with_groq_fallback(prompt, "pollen", max_retries)
        if isinstance(res, list):
            all_nomi_speciali.extend([s for s in res if isinstance(s, str)])
        else:
            candidates: List[str] = []
            candidates.extend(_extract_capitalized_phrases(chunk, limit=max(3, quota)))
            candidates.extend(_extract_important_sentences_offline(chunk, limit=max(3, quota)))
            all_nomi_speciali.extend(candidates[:quota])

    # Deduplicazione e limitazione
    seen = set()
    nomi_speciali_final = []
    for nome in all_nomi_speciali:
        cleaned = _normalize_entity_text(nome, single_word=False)
        if cleaned and cleaned.lower() not in seen:
            seen.add(cleaned.lower())
            nomi_speciali_final.append(cleaned)

    results["Nomi_Speciali_Pollinations"] = nomi_speciali_final[:numero_entita]
    status_callback(f"✅ Nomi Speciali: {len(results['Nomi_Speciali_Pollinations'])}", False)

    # Estrazione Frasi Importanti
    status_callback("🔍 Elaborazione Frasi Importanti…", False)
    all_frasi_importanti = []

    for chunk in text_chunks:
        remaining = numero_entita - len(all_frasi_importanti)
        if remaining <= 0:
            break

        quota = min(entities_per_chunk, remaining)

        prompt = PROMPT_FRASI_IMPORTANTI.format(
            lingua_testo=lingua_nome,
            istruzioni_lingua=istruzioni_lingua,
            numero_entita=quota,
            testo=chunk
        )

        res = None if force_offline else send_json_prompt_with_groq_fallback(prompt, "pollen", max_retries)
        if isinstance(res, list):
            all_frasi_importanti.extend([s for s in res if isinstance(s, str)])
        else:
            all_frasi_importanti.extend(_extract_important_sentences_offline(chunk, limit=quota))

    # Deduplicazione e limitazione
    seen = set()
    frasi_importanti_final = []
    for frase in all_frasi_importanti:
        cleaned = _clean_frase_importante(frase)
        cleaned = _normalize_entity_text(cleaned, single_word=False)
        key = cleaned.strip().lower()
        if cleaned and key not in seen and _is_valid_frase_importante(cleaned):
            seen.add(key)
            frasi_importanti_final.append(cleaned)

    results["Frasi_Importanti_Pollinations"] = frasi_importanti_final[:numero_entita]
    status_callback(f"✅ Frasi Importanti: {len(results['Frasi_Importanti_Pollinations'])}", False)

    # Estrazione Parole Importanti (escludendo Nomi Speciali)
    status_callback("🔍 Elaborazione Parole Importanti…", False)
    frasi_importanti_low = [f.lower() for f in results["Frasi_Importanti_Pollinations"] if isinstance(f, str)]
    ns_excl = ", ".join(results["Nomi_Speciali_Pollinations"]) if results["Nomi_Speciali_Pollinations"] else "Nessuno"
    all_parole_importanti = []

    # Applica regola: Parole_Importanti = 5x numero_entita selezionato dall'utente
    target_parole_count = max(0, int(numero_entita) + 5)

    for chunk in text_chunks:
        remaining = target_parole_count - len(all_parole_importanti)
        if remaining <= 0:
            break

        quota = min(entities_per_chunk, remaining)

        prompt = PROMPT_PAROLE_IMPORTANTI.format(
            lingua_testo=lingua_nome,
            istruzioni_lingua=istruzioni_lingua,
            numero_entita=quota,
            nomi_speciali_esclusi=ns_excl,
            testo=chunk
        )

        res = None if force_offline else send_json_prompt_with_groq_fallback(prompt, "pollen", max_retries)
        if isinstance(res, list):
            all_parole_importanti.extend([s for s in res if isinstance(s, str)])
        else:
            all_parole_importanti.extend(_extract_keywords_offline(chunk, lang=lingua, limit=quota))

    # Deduplicazione e limitazione (escludendo anche Nomi Speciali)
    seen = set(nome.lower() for nome in results["Nomi_Speciali_Pollinations"])
    parole_importanti_final = []

    for parola in all_parole_importanti:
        cleaned = _normalize_entity_text(parola, single_word=True)
        key = cleaned.lower() if cleaned else ""
        if key and frasi_importanti_low:
            try:
                pattern = r"\b" + re.escape(key) + r"\b"
                if any(re.search(pattern, frase) for frase in frasi_importanti_low):
                    continue
            except Exception:
                if any(key in frase for frase in frasi_importanti_low):
                    continue
        if cleaned and key not in seen:
            seen.add(key)
            parole_importanti_final.append(cleaned)

    results["Parole_Importanti_Pollinations"] = parole_importanti_final[:target_parole_count]
    status_callback(f"✅ Parole Importanti: {len(results['Parole_Importanti_Pollinations'])} (target {target_parole_count})", False)

    # Associazioni Entità Immagini
    if entita_immagini_json_input:
        try:
            status_callback("🖼️ Elaborazione associazioni immagini…", False)

            entita_list = list(entita_immagini_json_input.items())
            batches = [dict(entita_list[i:i+10]) for i in range(0, len(entita_list), 10)]

            merged = {}

            for batch_idx, batch in enumerate(batches):
                batch_json = json.dumps(batch, ensure_ascii=False)

                for chunk_idx, chunk in enumerate(text_chunks):
                    prompt = PROMPT_ENTITA_PER_IMMAGINI.format(
                        lingua_testo=lingua_nome,
                        istruzioni_lingua=istruzioni_lingua,
                        entita_json=batch_json,
                        testo=chunk
                    )

                    res = None if force_offline else send_json_prompt_with_groq_fallback(prompt, "pollen", max_retries)
                    if isinstance(res, dict):
                        merged.update(res)
                        break

                # Offline fallback: best-effort associations by searching mentions in the text
                if not any(entity in merged for entity in batch.keys()):
                    merged.update(_associate_entities_to_urls_offline(" ".join(text_chunks), batch))

                # Se non abbiamo trovato associazioni per questo batch, mantieni gli URL originali
                for entity, url in batch.items():
                    if entity not in merged or not merged[entity]:
                        merged[entity] = url

            results["Associazione_Entita_Immagini_Pollinations"] = _normalize_entity_map(merged)
            status_callback(f"✅ Associazioni immagini: {len(merged)}", False)

        except Exception as e:
            logger.error(f"Errore associazioni immagini: {e}")
            # Fallback: mantieni tutti gli URL originali
            results["Associazione_Entita_Immagini_Pollinations"] = _normalize_entity_map(entita_immagini_json_input)

    # Riepilogo finale
    status_callback("📊 Riepilogo estrazione Pollinations AI:", False)
    status_callback(f"  • Nomi Speciali: {len(results['Nomi_Speciali_Pollinations'])}", False)
    status_callback(f"  • Frasi Importanti: {len(results['Frasi_Importanti_Pollinations'])}", False)
    status_callback(f"  • Parole Importanti: {len(results['Parole_Importanti_Pollinations'])}", False)
    status_callback(f"  • Associazioni Immagini: {len(results['Associazione_Entita_Immagini_Pollinations'])}", False)

    total_time = int((time.time() - start_time) * 1000)
    logger.info(f"[{tid}] Pollinations extraction complete in {total_time}ms: nomi={len(results['Nomi_Speciali_Pollinations'])}, frasi={len(results['Frasi_Importanti_Pollinations'])}, parole={len(results['Parole_Importanti_Pollinations'])}, img={len(results['Associazione_Entita_Immagini_Pollinations'])}")

    return results
