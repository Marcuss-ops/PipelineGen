"""
Entity Classifier Module - Distingue tra Date e Numeri usando Duckling o spaCy.
Supporta multilingua basandosi sul language code rilevato da Whisper.
"""

import os
import logging
import re
from typing import List, Dict, Any, Optional, Tuple
import requests
import json

logger = logging.getLogger(__name__)

# Mapping lingue Duckling -> codice lingua Whisper
DUCKLING_LANG_MAP = {
    "en": "en_US",
    "it": "it_IT",
    "es": "es_ES",
    "fr": "fr_FR",
    "de": "de_DE",
    "pt": "pt_PT",
    "zh": "zh_CN",
    "ru": "ru_RU",
    "ar": "ar_SA",
    "ja": "ja_JP",
    "ko": "ko_KR",
    "nl": "nl_NL",
    "pl": "pl_PL",
    "tr": "tr_TR",
    "sv": "sv_SE",
    "da": "da_DK",
    "fi": "fi_FI",
    "no": "no_NO",
    "cs": "cs_CZ",
    "ro": "ro_RO",
    "hu": "hu_HU",
    "el": "el_GR",
    "he": "he_IL",
    "th": "th_TH",
    "vi": "vi_VN",
    "id": "id_ID",
    "ms": "ms_MY",
    "hi": "hi_IN",
    "bn": "bn_BD",
    "ta": "ta_IN",
    "te": "te_IN",
    "mr": "mr_IN",
    "ur": "ur_PK",
    "fa": "fa_IR",
    "uk": "uk_UA",
    "bg": "bg_BG",
    "hr": "hr_HR",
    "sk": "sk_SK",
    "sl": "sl_SI",
    "et": "et_EE",
    "lv": "lv_LV",
    "lt": "lt_LT",
}

# Fallback a en_US se lingua non supportata
DEFAULT_DUCKLING_LANG = "en_US"

# Duckling server URL (default localhost)
DUCKLING_SERVER_URL = os.getenv("DUCKLING_SERVER_URL", "http://localhost:8000")


def check_duckling_available() -> bool:
    """Verifica se il server Duckling è disponibile."""
    try:
        response = requests.get(f"{DUCKLING_SERVER_URL}/parse", timeout=2)
        return response.status_code == 400  # Duckling ritorna 400 se manca il parametro text, ma significa che è attivo
    except Exception:
        return False


def extract_entities_with_duckling(
    text: str,
    language_code: str = "en",
    status_callback: Optional[Any] = None
) -> Dict[str, List[Dict[str, Any]]]:
    """
    Estrae date e numeri dal testo usando Duckling.
    
    Args:
        text: Testo da analizzare
        language_code: Codice lingua (es. "en", "it", "es") rilevato da Whisper
        status_callback: Callback per messaggi di stato
    
    Returns:
        Dict con chiavi "Date" e "Numeri", ciascuna contenente lista di entità
        Ogni entità ha: {"text": str, "value": Any, "start": int, "end": int, "dim": str}
    """
    results = {
        "Date": [],
        "Numeri": []
    }
    
    if not text or not text.strip():
        return results
    
    # Verifica disponibilità Duckling
    if not check_duckling_available():
        if status_callback:
            status_callback("⚠️ Duckling server non disponibile, uso fallback spaCy", False)
        return extract_entities_with_spacy(text, language_code, status_callback)
    
    # Mappa codice lingua a formato Duckling
    duckling_lang = DUCKLING_LANG_MAP.get(language_code.lower(), DEFAULT_DUCKLING_LANG)
    
    try:
        # Chiamata a Duckling
        response = requests.post(
            f"{DUCKLING_SERVER_URL}/parse",
            json={
                "text": text,
                "lang": duckling_lang,
                "dims": ["time", "number", "amount-of-money", "duration", "distance"]
            },
            timeout=10
        )
        
        if response.status_code != 200:
            if status_callback:
                status_callback(f"⚠️ Duckling ritornato status {response.status_code}, uso fallback spaCy", False)
            return extract_entities_with_spacy(text, language_code, status_callback)
        
        entities = response.json()
        
        for entity in entities:
            dim = entity.get("dim", "")
            body = entity.get("body", "")
            start = entity.get("start", 0)
            end = entity.get("end", len(text))
            value = entity.get("value", {})
            
            # Classifica in Date o Numeri
            if dim == "time":
                # È una data
                results["Date"].append({
                    "text": body,
                    "value": value,
                    "start": start,
                    "end": end,
                    "dim": dim
                })
            elif dim in ["number", "amount-of-money", "duration", "distance"]:
                # È un numero (o quantità)
                results["Numeri"].append({
                    "text": body,
                    "value": value,
                    "start": start,
                    "end": end,
                    "dim": dim
                })
        
        if status_callback:
            status_callback(f"✅ Duckling: trovate {len(results['Date'])} date e {len(results['Numeri'])} numeri", False)
        
        return results
        
    except requests.exceptions.RequestException as e:
        if status_callback:
            status_callback(f"⚠️ Errore connessione Duckling: {e}, uso fallback spaCy", False)
        return extract_entities_with_spacy(text, language_code, status_callback)
    except Exception as e:
        logger.error(f"Errore estrazione Duckling: {e}")
        if status_callback:
            status_callback(f"⚠️ Errore Duckling: {e}, uso fallback spaCy", False)
        return extract_entities_with_spacy(text, language_code, status_callback)


def extract_entities_with_spacy(
    text: str,
    language_code: str = "en",
    status_callback: Optional[Any] = None
) -> Dict[str, List[Dict[str, Any]]]:
    """
    Estrae date e numeri dal testo usando spaCy (fallback).
    
    Args:
        text: Testo da analizzare
        language_code: Codice lingua (es. "en", "it", "es")
        status_callback: Callback per messaggi di stato
    
    Returns:
        Dict con chiavi "Date" e "Numeri"
    """
    results = {
        "Date": [],
        "Numeri": []
    }
    
    if not text or not text.strip():
        return results
    
    try:
        import spacy
        
        # Mappa codice lingua a modello spaCy
        spacy_model_map = {
            "en": "en_core_web_sm",
            "it": "it_core_news_sm",
            "es": "es_core_news_sm",
            "fr": "fr_core_news_sm",
            "de": "de_core_news_sm",
            "pt": "pt_core_news_sm",
            "zh": "zh_core_web_sm",
            "ru": "ru_core_news_sm",
            "nl": "nl_core_news_sm",
            "pl": "pl_core_news_sm",
            "ja": "ja_core_news_sm",
            "da": "da_core_news_sm",
            "el": "el_core_news_sm",
            "nb": "nb_core_news_sm",  # Norwegian
            "lt": "lt_core_news_sm",
            "xx": "xx_ent_wiki_sm"  # Multilingual fallback
        }
        
        model_name = spacy_model_map.get(language_code.lower(), "xx_ent_wiki_sm")
        
        try:
            nlp = spacy.load(model_name)
        except OSError:
            # Modello non disponibile, usa multilingua
            if status_callback:
                status_callback(f"⚠️ Modello spaCy {model_name} non disponibile, uso multilingua", False)
            try:
                nlp = spacy.load("xx_ent_wiki_sm")
            except OSError:
                if status_callback:
                    status_callback("⚠️ spaCy non disponibile, uso regex fallback", False)
                return extract_entities_with_regex(text, status_callback)
        
        doc = nlp(text)
        
        for ent in doc.ents:
            if ent.label_ in ["DATE", "TIME"]:
                # È una data
                results["Date"].append({
                    "text": ent.text,
                    "value": None,  # spaCy non fornisce valore strutturato
                    "start": ent.start_char,
                    "end": ent.end_char,
                    "dim": "time"
                })
            elif ent.label_ in ["MONEY", "CARDINAL", "PERCENT", "QUANTITY", "ORDINAL"]:
                # È un numero
                results["Numeri"].append({
                    "text": ent.text,
                    "value": None,
                    "start": ent.start_char,
                    "end": ent.end_char,
                    "dim": "number"
                })
        
        if status_callback:
            status_callback(f"✅ spaCy: trovate {len(results['Date'])} date e {len(results['Numeri'])} numeri", False)
        
        return results
        
    except ImportError:
        if status_callback:
            status_callback("⚠️ spaCy non installato, uso regex fallback", False)
        return extract_entities_with_regex(text, status_callback)
    except Exception as e:
        logger.error(f"Errore estrazione spaCy: {e}")
        if status_callback:
            status_callback(f"⚠️ Errore spaCy: {e}, uso regex fallback", False)
        return extract_entities_with_regex(text, status_callback)


def extract_entities_with_regex(
    text: str,
    status_callback: Optional[Any] = None
) -> Dict[str, List[Dict[str, Any]]]:
    """
    Estrae date e numeri usando regex (fallback ultimo livello).
    Meno preciso ma sempre disponibile.
    """
    results = {
        "Date": [],
        "Numeri": []
    }
    
    if not text or not text.strip():
        return results
    
    # Pattern per date (formati comuni)
    date_patterns = [
        r'\b\d{1,2}[/-]\d{1,2}[/-]\d{2,4}\b',  # DD/MM/YYYY, DD-MM-YYYY
        r'\b\d{4}[/-]\d{1,2}[/-]\d{1,2}\b',     # YYYY/MM/DD, YYYY-MM-DD
        r'\b\d{1,2}\s+(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre|january|february|march|april|may|june|july|august|september|october|november|december)\s+\d{2,4}\b',  # Date con mese
        r'\b(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre|january|february|march|april|may|june|july|august|september|october|november|december)\s+\d{1,2},?\s+\d{2,4}\b',  # Mese giorno anno
        r'\b\d{4}\b',  # Anno singolo (solo se contesto suggerisce data)
    ]
    
    # Pattern per numeri (soldi, quantità, etc.)
    number_patterns = [
        r'\$\d+(?:\.\d+)?\s*(?:million|billion|thousand|miliardi|milioni|migliaia)?\b',  # Soldi
        r'\b\d+(?:\.\d+)?\s*(?:dollari|euro|dollars|euros|€|\$)\b',  # Valute
        r'\b\d+(?:\.\d+)?\s*(?:percent|%|per cento|prozent)\b',  # Percentuali (aggiunto tedesco)
        r'\b\d{1,3}(?:[.,]\d{3})+\b',  # Numeri grandi con separatori
        r'\b\d{2,}\b',  # Numeri con almeno 2 cifre (più aggressivo)
        r'\b\d+(?:\.\d+)?\b',  # Numeri generici (solo se non sono date)
    ]
    
    # Cerca date
    for pattern in date_patterns:
        for match in re.finditer(pattern, text, re.IGNORECASE):
            # Evita duplicati
            if not any(d["start"] == match.start() and d["end"] == match.end() for d in results["Date"]):
                results["Date"].append({
                    "text": match.group(),
                    "value": None,
                    "start": match.start(),
                    "end": match.end(),
                    "dim": "time"
                })
    
    # Cerca numeri (escludendo quelli già trovati come date)
    date_positions = {(d["start"], d["end"]) for d in results["Date"]}
    date_texts = {d["text"] for d in results["Date"]}
    
    for pattern in number_patterns:
        for match in re.finditer(pattern, text, re.IGNORECASE):
            pos = (match.start(), match.end())
            matched_text = match.group()
            
            # Escludi se è già una data o se è solo un anno (probabilmente una data)
            # MA: se l'anno è già stato trovato come data, escludilo; altrimenti includilo come numero
            is_year_only = re.match(r'^\d{4}$', matched_text)
            is_already_date = pos in date_positions or matched_text in date_texts
            
            # Se è un anno a 4 cifre E non è già stato trovato come data, potrebbe essere un numero
            # (es. "2021" potrebbe essere un anno o un numero, dipende dal contesto)
            # Per ora, se è un anno a 4 cifre e non è già una data, lo includiamo come numero
            if not is_already_date:
                # Evita duplicati
                if not any(n["start"] == match.start() and n["end"] == match.end() for n in results["Numeri"]):
                    results["Numeri"].append({
                        "text": matched_text,
                        "value": None,
                        "start": match.start(),
                        "end": match.end(),
                        "dim": "number"
                    })
    
    if status_callback:
        status_callback(f"✅ Regex: trovate {len(results['Date'])} date e {len(results['Numeri'])} numeri", False)
    
    return results


def map_entities_to_timestamps(
    entities: Dict[str, List[Dict[str, Any]]],
    transcription_segments: List[Any],
    status_callback: Optional[Any] = None
) -> Dict[str, Dict[str, Any]]:
    """
    Mappa le entità trovate (con start/end caratteri) ai timestamp di Whisper.
    
    Args:
        entities: Dict con "Date" e "Numeri", ciascuna lista di entità con start/end
        transcription_segments: Segmenti trascritti da Whisper con word-level timestamps
        status_callback: Callback per messaggi
    
    Returns:
        Dict nel formato associazioni_finali: {"Date": {...}, "Numeri": {...}}
    """
    results = {
        "Date": {},
        "Numeri": {}
    }

    def _seg_get(seg: Any, key: str, default: Any = None) -> Any:
        """Compat: support both dict segments and Whisper Segment objects."""
        try:
            if isinstance(seg, dict):
                return seg.get(key, default)
            if hasattr(seg, key):
                return getattr(seg, key, default)
            getter = getattr(seg, "get", None)
            if callable(getter):
                return getter(key, default)
        except Exception:
            return default
        return default
    
    # Costruisci mappa carattere -> timestamp
    char_to_timestamp = {}
    current_char = 0
    
    for segment in transcription_segments:
        segment_text = _seg_get(segment, "text", "") or ""
        segment_start = float(_seg_get(segment, "start", 0.0) or 0.0)
        segment_end = float(_seg_get(segment, "end", 0.0) or 0.0)
        
        # Se ci sono word-level timestamps, usali
        words = _seg_get(segment, "words", []) or []
        if words:
            for word in words:
                word_text = _seg_get(word, "word", "") or ""
                word_start = float(_seg_get(word, "start", segment_start) or segment_start)
                word_end = float(_seg_get(word, "end", segment_end) or segment_end)
                
                for i, char in enumerate(word_text):
                    char_to_timestamp[current_char + i] = (word_start, word_end)
                current_char += len(word_text) + 1  # +1 per spazio
        else:
            # Fallback: distribuisci uniformemente nel segmento
            for i, char in enumerate(segment_text):
                ratio = i / max(len(segment_text), 1)
                timestamp = segment_start + (segment_end - segment_start) * ratio
                char_to_timestamp[current_char + i] = (timestamp, timestamp)
            current_char += len(segment_text) + 1
    
    # Mappa entità a timestamp
    for category in ["Date", "Numeri"]:
        for entity in entities[category]:
            start_char = entity["start"]
            end_char = entity["end"]
            entity_text = entity["text"]
            
            # Trova timestamp per inizio e fine
            start_time = None
            end_time = None
            
            # Cerca il timestamp più vicino
            for char_pos in range(start_char, min(end_char, len(char_to_timestamp))):
                if char_pos in char_to_timestamp:
                    ts_start, ts_end = char_to_timestamp[char_pos]
                    if start_time is None:
                        start_time = ts_start
                    end_time = ts_end
            
            # Fallback: usa il timestamp del segmento che contiene l'entità
            if start_time is None:
                for segment in transcription_segments:
                    segment_text = _seg_get(segment, "text", "") or ""
                    segment_start = float(_seg_get(segment, "start", 0.0) or 0.0)
                    segment_end = float(_seg_get(segment, "end", 0.0) or 0.0)
                    
                    # Verifica se l'entità è in questo segmento
                    full_text = " ".join([(_seg_get(s, "text", "") or "") for s in transcription_segments])
                    if entity_text.lower() in segment_text.lower():
                        start_time = segment_start
                        end_time = segment_end
                        break
            
            if start_time is not None and end_time is not None:
                # Aggiungi un po' di padding per visibilità
                padding = 0.5
                start_time = max(0, start_time - padding)
                end_time = end_time + padding
                
                # Salva nel formato associazioni_finali (compatibile con workflow.py)
                # Formato: lista di dict con timestamp_start e timestamp_end
                if entity_text not in results[category]:
                    results[category][entity_text] = []
                
                results[category][entity_text].append({
                    "timestamp_start": start_time,
                    "timestamp_end": end_time
                })
    
    if status_callback:
        total_dates = len(results["Date"])
        total_numeri = len(results["Numeri"])
        status_callback(f"✅ Mappate {total_dates} date e {total_numeri} numeri ai timestamp", False)
    
    return results


def extract_and_classify_entities(
    text: str,
    transcription_segments: List[Dict[str, Any]],
    language_code: str = "en",
    status_callback: Optional[Any] = None,
    use_duckling: bool = True
) -> Dict[str, Dict[str, Any]]:
    """
    Funzione principale: estrae e classifica entità (Date vs Numeri) dal testo.
    
    Args:
        text: Testo completo trascritto
        transcription_segments: Segmenti trascritti con timestamps
        language_code: Codice lingua rilevato da Whisper
        status_callback: Callback per messaggi
        use_duckling: Se True, prova Duckling prima di spaCy
    
    Returns:
        Dict nel formato associazioni_finali: {"Date": {...}, "Numeri": {...}}
    """
    if status_callback:
        status_callback(f"🔍 Estrazione entità (Date/Numeri) in lingua {language_code}...", False)
    
    # Estrai entità
    if use_duckling and check_duckling_available():
        entities = extract_entities_with_duckling(text, language_code, status_callback)
    else:
        entities = extract_entities_with_spacy(text, language_code, status_callback)
    
    # LIMITAZIONE TEMP PER DEBUG (Richiesta utente per velocizzare test)
    # Limita a 1 entità per tipo
    if entities:
        if len(entities.get("Date", [])) > 1:
            entities["Date"] = entities["Date"][:1]
            if status_callback:
                status_callback("⚠️ DEBUG: Limite Date impostato a 1", False)
                
        if len(entities.get("Numeri", [])) > 1:
            entities["Numeri"] = entities["Numeri"][:1]
            if status_callback:
                status_callback("⚠️ DEBUG: Limite Numeri impostato a 1", False)
    
    # Mappa a timestamp
    results = map_entities_to_timestamps(entities, transcription_segments, status_callback)
    
    return results
