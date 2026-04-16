"""
Entity Stack Grouper - Algoritmo per raggruppare entità vicine in stack
Implementa l'Entity Layer Stack Algorithm per creare transizioni fluide tra entità.
"""

from typing import List, Dict, Any, Optional, Tuple
import os
import random

try:
    import validators as _validators
    def _is_valid_url(value: str) -> bool:
        return bool(_validators.url(value))
except Exception:
    def _is_valid_url(value: str) -> bool:
        return isinstance(value, str) and value.startswith(("http://", "https://"))

def _is_likely_image_path(value: str) -> bool:
    """Heuristic: accept local/public paths or filenames with image extensions."""
    if not isinstance(value, str):
        return False
    s = value.strip()
    if not s:
        return False
    if s.startswith("/"):
        return True
    ext = os.path.splitext(s.lower())[1]
    return ext in (".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tiff")

def _extract_image_path(entry: Dict[str, Any], key: str = "", link_list: Optional[List[Any]] = None) -> Optional[str]:
    """Best-effort extraction of image path/URL from various entry formats."""
    # Direct fields
    for k in ("image_path", "image_url", "image", "url", "link", "src", "path"):
        v = entry.get(k)
        if isinstance(v, str) and v.strip():
            return v.strip()
        if isinstance(v, dict):
            for dk in ("image_url", "url", "link", "src", "path"):
                dv = v.get(dk)
                if isinstance(dv, str) and dv.strip():
                    return dv.strip()
    # Link immagine field (string or list)
    link_immagine = entry.get("Link immagine")
    if isinstance(link_immagine, list) and link_immagine:
        v = link_immagine[0]
        if isinstance(v, str) and v.strip():
            return v.strip()
        if isinstance(v, dict):
            for dk in ("image_url", "url", "link", "src", "path"):
                dv = v.get(dk)
                if isinstance(dv, str) and dv.strip():
                    return dv.strip()
    elif isinstance(link_immagine, str) and link_immagine.strip():
        return link_immagine.strip()
    # Link list passed from outer structure
    if link_list:
        for v in link_list:
            if isinstance(v, str) and v.strip():
                return v.strip()
            if isinstance(v, dict):
                for dk in ("image_url", "url", "link", "src", "path"):
                    dv = v.get(dk)
                    if isinstance(dv, str) and dv.strip():
                        return dv.strip()
    # If key itself looks like a URL/path, accept it
    if isinstance(key, str) and key.strip():
        ks = key.strip()
        if _is_valid_url(ks) or _is_likely_image_path(ks):
            return ks
    return None
import logging

logger = logging.getLogger(__name__)

# Soglia massima di distanza tra entità per essere raggruppate (in secondi)
MAX_ENTITY_DISTANCE_SECONDS = 4.0
# Deve matchare TRANSITION_DURATION in my-video/src/EntityStack.tsx (24)
ENTITY_STACK_TRANSITION_FRAMES = 24

# Stili disponibili per animazione casuale per layer (quando stack_layer_style == DEFAULT)
NAME_STYLES = ["CLASSIC_V2", "CLASSIC_V3", "TYPEWRITER_V1", "TYPEWRITER_V2", "TYPEWRITER_V11", "HIGHLIGHT_V1", "HIGHLIGHT_V2"]
PHRASE_STYLES = ["FADEUPWORDS", "SLIDESOFT", "ATMOSPHERIC", "OPACITYWAVE", "BLURSLIDE", "TRACKINGFOCUS", "GLOWPULSE", "TEXTFOCUS", "COLOREMPHASIS", "UNDERLINEFOLLOW", "PARTIALSCRAMBLE"]
DATE_STYLES = ["TYPEWRITER", "ZOOMIN", "SLIDEUP", "SLIDEDOWN", "DEFOCUS", "FADESLIDE", "GLASS", "SLIDEHORIZONTAL"]
NUMBER_STYLES = ["SLIDELEFT", "ZOOMIN", "SLIDEUP", "SLIDEDOWN", "DEFOCUS", "FADESLIDE", "GLASS", "SLIDEHORIZONTAL"]

_ENTITY_PRIORITY = {
    # Priorita: Nomi_Speciali > Frasi_Importanti > resto
    "Nomi_Speciali": 3,
    "Frasi_Importanti": 2,
    "Date": 1,
    "Numeri": 1,
}


# Parole numeriche (EN/IT) -> cifra per entità Numeri (one->1, uno->1, etc.)
_WORD_TO_DIGIT = {
    "one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
    "six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10",
    "eleven": "11", "twelve": "12", "thirteen": "13", "fourteen": "14", "fifteen": "15",
    "sixteen": "16", "seventeen": "17", "eighteen": "18", "nineteen": "19", "twenty": "20",
    "thirty": "30", "forty": "40", "fifty": "50", "sixty": "60", "seventy": "70",
    "eighty": "80", "ninety": "90", "hundred": "100",
    "uno": "1", "due": "2", "tre": "3", "quattro": "4", "cinque": "5",
    "sei": "6", "sette": "7", "otto": "8", "nove": "9", "dieci": "10",
    "undici": "11", "dodici": "12", "tredici": "13", "quattordici": "14", "quindici": "15",
    "sedici": "16", "diciassette": "17", "diciotto": "18", "diciannove": "19", "venti": "20",
}


def _number_word_to_digit(content: str) -> str:
    """Converte parole numeriche in cifre (one->1, uno->1). Se non è una parola nota, restituisce il contenuto invariato."""
    if not content or not isinstance(content, str):
        return content
    s = content.strip().lower()
    if not s:
        return content
    return _WORD_TO_DIGIT.get(s, content)


def _is_likely_real_date(content: str) -> bool:
    """Esclude Date con contenuto tipo 'recent months' (nessuna cifra). Una data reale ha almeno una cifra (anno o giorno)."""
    if not content or not isinstance(content, str):
        return False
    s = content.strip()
    if not s:
        return False
    return any(c.isdigit() for c in s)


def _normalize_text_for_containment(value: str) -> str:
    if not isinstance(value, str):
        return ""
    s = value.lower()
    out_chars = []
    prev_space = False
    for ch in s:
        if ch.isalnum():
            out_chars.append(ch)
            prev_space = False
        else:
            if not prev_space:
                out_chars.append(" ")
                prev_space = True
    normalized = "".join(out_chars).strip()
    # Collassa spazi multipli
    return " ".join(normalized.split())


def _time_close(a: Dict[str, Any], b: Dict[str, Any], max_distance_seconds: float) -> bool:
    a_start = float(a.get("start_v", 0) or 0)
    b_start = float(b.get("start_v", 0) or 0)
    a_end = a_start + float(a.get("dur_v", 0) or 0)
    b_end = b_start + float(b.get("dur_v", 0) or 0)
    if a_start < b_end and b_start < a_end:
        return True
    # distanza tra segmenti
    distance = min(abs(a_start - b_end), abs(b_start - a_end))
    return distance <= max_distance_seconds


def _filter_contained_entities(
    entities: List[Dict[str, Any]],
    max_distance_seconds: float,
) -> List[Dict[str, Any]]:
    """
    Rimuove entità il cui testo è contenuto in altre entità di priorità più alta,
    purché temporalmente vicine.
    """
    if not entities:
        return entities
    normalized: Dict[int, str] = {}
    for idx, e in enumerate(entities):
        normalized[idx] = _normalize_text_for_containment(e.get("content", "") or "")

    removed = set()
    for i, ent in enumerate(entities):
        if i in removed:
            continue
        ent_type = ent.get("type")
        ent_norm = normalized.get(i, "")
        if not ent_norm:
            continue
        ent_pri = _ENTITY_PRIORITY.get(ent_type, 0)
        for j, other in enumerate(entities):
            if i == j or j in removed:
                continue
            other_type = other.get("type")
            other_norm = normalized.get(j, "")
            if not other_norm:
                continue
            other_pri = _ENTITY_PRIORITY.get(other_type, 0)
            if other_pri <= ent_pri:
                continue
            if not _time_close(ent, other, max_distance_seconds):
                continue
            if ent_norm and ent_norm in other_norm:
                removed.add(i)
                logger.info(
                    "[EntityStack] Rimuovo entita contenuta: %s | contenuta in %s | content='%s'",
                    ent_type,
                    other_type,
                    (ent.get("content") or "")[:60],
                )
                break

    if removed:
        kept = [e for idx, e in enumerate(entities) if idx not in removed]
        logger.info(
            "[EntityStack] Filtrate %d entita contenute (rimaste %d)",
            len(removed),
            len(kept),
        )
        return kept
    return entities


def group_entities_for_stack(
    entities: List[Dict[str, Any]],
    max_distance_seconds: float = MAX_ENTITY_DISTANCE_SECONDS
) -> List[List[Dict[str, Any]]]:
    """
    Raggruppa entità vicine in stack per il rendering con EntityStack.
    
    IMPORTANTE: Se un'entità finisce e un'altra inizia entro max_distance_seconds,
    la prima entità viene estesa per rimanere visibile fino all'inizio della successiva.
    Questo crea transizioni fluide dove l'entità precedente "aspetta" la successiva.
    
    Args:
        entities: Lista di entità con 'start_v', 'dur_v', 'type', 'content', etc.
        max_distance_seconds: Massima distanza tra entità per essere raggruppate (default: 4.0s)
    
    Returns:
        Lista di gruppi, dove ogni gruppo è una lista di entità da renderizzare insieme
        con EntityStack. Le durate delle entità vengono estese se necessario.
    """
    if not entities:
        return []
    
    # Ordina entità per start_v
    sorted_entities = sorted(entities, key=lambda e: e.get('start_v', 0))
    
    groups: List[List[Dict[str, Any]]] = []
    current_group: List[Dict[str, Any]] = []
    
    for i, entity in enumerate(sorted_entities):
        start_v = entity.get('start_v', 0)
        dur_v = entity.get('dur_v', 0)
        end_v = start_v + dur_v
        
        if not current_group:
            # Inizia un nuovo gruppo
            current_group.append(entity)
        else:
            # Calcola distanza dall'ultima entità del gruppo corrente
            last_entity = current_group[-1]
            last_start = last_entity.get('start_v', 0)
            last_dur = last_entity.get('dur_v', 0)
            last_end = last_start + last_dur
            
            # Distanza tra fine ultima entità e inizio nuova entità
            distance = start_v - last_end
            
            if distance <= max_distance_seconds:
                # Entità vicina: estendi durata ultima entità fino all'inizio della nuova
                # L'entità precedente "aspetta" la successiva prima di sparire
                if distance > 0:
                    # Estendi la durata dell'ultima entità per coprire il gap
                    extended_dur = last_dur + distance
                    last_entity['dur_v'] = extended_dur
                    last_entity['extended_to'] = start_v  # Traccia l'estensione
                    logger.debug(
                        f"Estesa durata entità {last_entity.get('type', 'UNKNOWN')} "
                        f"da {last_dur:.2f}s a {extended_dur:.2f}s "
                        f"(aspetta entità successiva che inizia a {start_v:.2f}s)"
                    )
                
                # Aggiungi la nuova entità al gruppo
                current_group.append(entity)
            else:
                # Entità lontana: salva gruppo corrente e inizia nuovo gruppo
                if current_group:
                    groups.append(current_group)
                current_group = [entity]
    
    # Aggiungi l'ultimo gruppo se non vuoto
    if current_group:
        groups.append(current_group)
    
    logger.info(f"Raggruppate {len(entities)} entità in {len(groups)} gruppi per EntityStack")
    for i, group in enumerate(groups):
        group_types = [e.get('type', 'UNKNOWN') for e in group]
        type_counts = {}
        for t in group_types:
            type_counts[t] = type_counts.get(t, 0) + 1
        type_summary = ", ".join([f"{count}x {t}" for t, count in type_counts.items()])
        
        # Calcola durata totale del gruppo (considerando estensioni)
        group_start = group[0].get('start_v', 0)
        last_entity = group[-1]
        group_end = last_entity.get('start_v', 0) + last_entity.get('dur_v', 0)
        
        logger.info(
            f"Gruppo {i+1}: {len(group)} entità [{type_summary}] "
            f"(start: {group_start:.2f}s, end: {group_end:.2f}s, durata: {group_end - group_start:.2f}s)"
        )
        
        # Log dettagliato delle estensioni
        for j, entity in enumerate(group):
            if j < len(group) - 1:  # Non l'ultima entità
                next_entity = group[j + 1]
                entity_end = entity.get('start_v', 0) + entity.get('dur_v', 0)
                next_start = next_entity.get('start_v', 0)
                if entity_end >= next_start:
                    logger.debug(
                        f"  - Entità {j+1} ({entity.get('type', 'UNKNOWN')}): "
                        f"estesa fino a {next_start:.2f}s (inizio entità {j+2})"
                    )
    
    return groups


def convert_entity_to_remotion_format(
    entity: Dict[str, Any],
    entity_type: str,
    remotion_style_manager: Optional[Any] = None,
    *,
    fps: int = 30,
    stack_layer_style: Optional[str] = None,
    randomize_layer_animation: bool = False,
) -> Dict[str, Any]:
    """
    Converte un'entità Python nel formato richiesto da EntityStack Remotion.
    
    Args:
        entity: Dizionario entità Python con 'content', 'start_v', 'dur_v', etc.
        entity_type: Tipo entità ('Date', 'Nomi_Speciali', 'Numeri', 'Frasi_Importanti', 'IMAGE')
        remotion_style_manager: Opzionale, per ottenere stili Remotion
    
    Returns:
        Dizionario nel formato EntityItem per Remotion EntityStack
    """
    def _wrap_text_chars(s: str, limit: int = 25, strict: bool = False) -> str:
        """strict=True: a capo fisso ogni `limit` caratteri. strict=False: a capo a spazi (parole intere, mai spezzare una parola)."""
        try:
            flat = (s or "").replace("\n", " ").replace("\r", " ").strip()
            if not flat:
                return s
            if strict:
                return "\n".join(flat[i : i + limit] for i in range(0, len(flat), limit))
            words = flat.split()
            lines, current = [], ""
            for w in words:
                if not current:
                    current = w
                    continue
                if len(current) + 1 + len(w) <= limit:
                    current = f"{current} {w}"
                else:
                    lines.append(current)
                    current = w
            if current:
                lines.append(current)
            return "\n".join(lines)
        except Exception:
            return s
    content = entity.get('content', '')
    start_v = entity.get('start_v', 0)
    dur_v = entity.get('dur_v', 0)
    
    # Converti durata in frames (coerente con fps di render Remotion)
    safe_fps = int(fps) if int(fps) > 0 else 30
    duration_in_frames = max(1, int(round(float(dur_v) * safe_fps)))
    
    # Mappa tipi Python -> tipi Remotion
    type_mapping = {
        'Date': 'DATE',
        'Nomi_Speciali': 'NAME',
        'Numeri': 'NUMBER',
        'Frasi_Importanti': 'PHRASE',
        'Entita_Senza_Testo': 'IMAGE',
    }
    
    remotion_type = type_mapping.get(entity_type, 'PHRASE')
    
    # Crea ID univoco
    entity_id = f"{entity_type}_{start_v:.2f}_{dur_v:.2f}"
    
    # Base entity item
    # Content formatting: NAME/DATE uppercase; NUMBER e testo lasciati come arrivano (no conversione one->1: non scala per russo/altre lingue)
    if remotion_type in ('NAME', 'DATE'):
        content_out = str(content).strip().upper()
    elif remotion_type == 'NUMBER':
        content_out = str(content).strip()  # Mostra il numero come fornito (one, uno, один, 1, etc.)
    else:
        content_out = str(content).strip() if remotion_type != 'IMAGE' else content
    # Wrap: Frasi ogni 25 caratteri a spazi (mai spezzare una parola), Nomi 18
    if entity_type == 'Frasi_Importanti' and remotion_type != 'IMAGE':
        content_out = _wrap_text_chars(str(content_out), 25, strict=False)
    elif entity_type == 'Nomi_Speciali' and remotion_type == 'NAME':
        content_out = _wrap_text_chars(str(content_out), 18, strict=False)

    # Guard rails: skip entities with empty content to avoid placeholder/demo strings in Remotion components.
    if remotion_type == 'IMAGE':
        if not content_out or str(content_out).strip().lower() in ("", "none", "null"):
            raise ValueError(f"Entita IMAGE senza path valido (content='{content_out}')")
        # Skip non-URL and non-path values (e.g., search queries).
        if not (_is_valid_url(str(content_out)) or _is_likely_image_path(str(content_out))):
            raise ValueError(f"Entita IMAGE non valida (content='{content_out}')")
    else:
        if not content_out:
            raise ValueError(f"Entita {remotion_type} senza contenuto valido (content='{content_out}')")

    entity_item: Dict[str, Any] = {
        'id': entity_id,
        'type': remotion_type,
        'content': content_out,
        'duration': duration_in_frames,
        'has_background': False,  # Solo la prima entità del gruppo avrà background
    }

    # Log di debug sul mapping dell'entità
    try:
        logger.info(f"[EntityStack] Map {entity_type} -> {remotion_type} | content='{str(content_out)[:80]}' | dur={duration_in_frames}f")
    except Exception:
        pass
    
    # Aggiungi stili specifici se disponibili (stesso aspetto del test: maiuscolo, wrap 20, 3D, no flicker)
    if entity_type == 'Frasi_Importanti':
        if remotion_style_manager:
            try:
                animation_id = remotion_style_manager.get_frasi_importanti_style()
                if animation_id and 'Minimal-' in str(animation_id):
                    style_name = str(animation_id).replace('Minimal-', '').upper()
                    entity_item['phraseStyle'] = style_name
            except Exception:
                pass
        # Default PARTIALSCRAMBLE solo se non useremo random per layer (altrimenti lo imposta il blocco randomize_layer_animation)
        if not entity_item.get('phraseStyle') and not randomize_layer_animation:
            entity_item['phraseStyle'] = 'PARTIALSCRAMBLE'
    if remotion_style_manager:
        try:
            if entity_type == 'Nomi_Speciali':
                animation_id = remotion_style_manager.get_nomi_speciali_style()
                # Mappa animation_id a nameStyle
                if 'Typewriter' in animation_id:
                    if 'V1' in animation_id:
                        entity_item['nameStyle'] = 'TYPEWRITER_V1'
                    elif 'V2' in animation_id:
                        entity_item['nameStyle'] = 'TYPEWRITER_V2'
                    elif 'V11' in animation_id:
                        entity_item['nameStyle'] = 'TYPEWRITER_V11'
                elif 'Highlight' in animation_id:
                    if 'V1' in animation_id:
                        entity_item['nameStyle'] = 'HIGHLIGHT_V1'
                    elif 'V2' in animation_id:
                        entity_item['nameStyle'] = 'HIGHLIGHT_V2'
                elif 'Classic' in animation_id:
                    if 'V2' in animation_id:
                        entity_item['nameStyle'] = 'CLASSIC_V2'
                    elif 'V3' in animation_id:
                        entity_item['nameStyle'] = 'CLASSIC_V3'
            elif entity_type == 'Date':
                animation_id = remotion_style_manager.get_date_style()
                # Mappa animation_id a dateStyle
                if 'Typewriter' in animation_id:
                    entity_item['dateStyle'] = 'TYPEWRITER'
                elif 'ZoomIn' in animation_id:
                    entity_item['dateStyle'] = 'ZOOMIN'
                elif 'SlideUp' in animation_id:
                    entity_item['dateStyle'] = 'SLIDEUP'
                elif 'SlideDown' in animation_id:
                    entity_item['dateStyle'] = 'SLIDEDOWN'
                elif 'Defocus' in animation_id:
                    entity_item['dateStyle'] = 'DEFOCUS'
                elif 'FadeSlide' in animation_id:
                    entity_item['dateStyle'] = 'FADESLIDE'
                elif 'Glass' in animation_id:
                    entity_item['dateStyle'] = 'GLASS'
                elif 'SlideHorizontal' in animation_id:
                    entity_item['dateStyle'] = 'SLIDEHORIZONTAL'
            elif entity_type == 'Numeri':
                animation_id = remotion_style_manager.get_numeri_style()
                # Mappa animation_id a numberStyle
                if 'SlideLeft' in animation_id:
                    entity_item['numberStyle'] = 'SLIDELEFT'
                elif 'ZoomIn' in animation_id:
                    entity_item['numberStyle'] = 'ZOOMIN'
                elif 'SlideUp' in animation_id:
                    entity_item['numberStyle'] = 'SLIDEUP'
                elif 'SlideDown' in animation_id:
                    entity_item['numberStyle'] = 'SLIDEDOWN'
                elif 'Defocus' in animation_id:
                    entity_item['numberStyle'] = 'DEFOCUS'
                elif 'FadeSlide' in animation_id:
                    entity_item['numberStyle'] = 'FADESLIDE'
                elif 'Glass' in animation_id:
                    entity_item['numberStyle'] = 'GLASS'
                elif 'SlideHorizontal' in animation_id:
                    entity_item['numberStyle'] = 'SLIDEHORIZONTAL'
        except Exception as e:
            logger.warning(f"Errore nel recupero stile Remotion per {entity_type}: {e}")

    # Animazione casuale per ogni layer quando stack_layer_style è DEFAULT (random nameStyle/phraseStyle/dateStyle/numberStyle)
    if randomize_layer_animation:
        if entity_type == "Nomi_Speciali" and not entity_item.get("nameStyle"):
            entity_item["nameStyle"] = random.choice(NAME_STYLES)
            logger.debug("[EntityStack] Random nameStyle: %s", entity_item["nameStyle"])
        elif entity_type == "Frasi_Importanti" and not entity_item.get("phraseStyle"):
            entity_item["phraseStyle"] = random.choice(PHRASE_STYLES)
            logger.debug("[EntityStack] Random phraseStyle: %s", entity_item["phraseStyle"])
        elif entity_type == "Date" and not entity_item.get("dateStyle"):
            entity_item["dateStyle"] = random.choice(DATE_STYLES)
            logger.debug("[EntityStack] Random dateStyle: %s", entity_item["dateStyle"])
        elif entity_type == "Numeri" and not entity_item.get("numberStyle"):
            entity_item["numberStyle"] = random.choice(NUMBER_STYLES)
            logger.debug("[EntityStack] Random numberStyle: %s", entity_item["numberStyle"])

    # DATE e NUMBER: assegna sempre uno stile se mancante, indipendentemente dallo stack layer.
    # Così quando si sceglie stack 2 (direzionale) il frontend fa fallback a EntityStack e le date/numeri
    # appaiono con lo stesso tipo di animazione che con stack 1 (DEFAULT), non sempre TYPEWRITER/SLIDELEFT.
    if entity_type == "Date" and not entity_item.get("dateStyle"):
        entity_item["dateStyle"] = random.choice(DATE_STYLES)
        logger.debug("[EntityStack] dateStyle (fallback per stack non-DEFAULT): %s", entity_item["dateStyle"])
    elif entity_type == "Numeri" and not entity_item.get("numberStyle"):
        entity_item["numberStyle"] = random.choice(NUMBER_STYLES)
        logger.debug("[EntityStack] numberStyle (fallback per stack non-DEFAULT): %s", entity_item["numberStyle"])

    # Imposta bgMode solo se fornito esplicitamente.
    # Se assente, il progetto Remotion userà un fallback coerente (es. random BLACK/WHITE quando non c'è background utente).
    bg_mode = entity.get('bgMode')
    if bg_mode in ('BLACK', 'WHITE'):
        entity_item['bgMode'] = bg_mode
    
    # Per IMAGE, usa il path dell'immagine
    if remotion_type == 'IMAGE':
        # Il content dovrebbe essere il path all'immagine
        # Verifica che sia un path valido (non una URL web)
        image_path = entity.get('image_path') or content
        # Se è una URL web, potrebbe essere necessario scaricarla o convertirla
        # Per ora, usa il path così com'è - EntityStack.tsx gestirà il caricamento
        entity_item['content'] = image_path
    
    # Per DATE, gestisci subContent se presente
    if remotion_type == 'DATE' and '/' in str(content):
        parts = str(content).split('/')
        if len(parts) >= 2:
            entity_item['content'] = parts[0].strip().upper()
            entity_item['subContent'] = parts[-1].strip().upper()
    
    return entity_item


def prepare_entity_stack_groups(
    associazioni_finali_con_timestamp: Dict[str, Any],
    map_audio_to_video: callable,
    video_duration_limit: float,
    remotion_style_manager: Optional[Any] = None,
    background_path: Optional[str] = None,
    max_distance_seconds: float = MAX_ENTITY_DISTANCE_SECONDS,
    *,
    fps: int = 30,
    equalize_durations: bool = True,
    hold_seconds: float = 2.0,
    min_entity_seconds: float = 2.0,
    stack_layer_style: Optional[str] = None,
) -> List[Dict[str, Any]]:
    """
    Prepara tutti i gruppi di entità per il rendering con EntityStack.
    
    Args:
        associazioni_finali_con_timestamp: Dizionario con tutte le entità e timestamp
        map_audio_to_video: Funzione per mappare timestamp audio -> video
        video_duration_limit: Durata massima del video
        remotion_style_manager: Opzionale, per ottenere stili Remotion
        background_path: Opzionale, path al background per la prima entità
        max_distance_seconds: Massima distanza tra entità per raggruppamento
    
    Returns:
        Lista di gruppi, dove ogni gruppo contiene:
        - 'entities': Lista di EntityItem in formato Remotion
        - 'start_v': Tempo di inizio del gruppo (minimo start_v delle entità)
        - 'total_duration': Durata totale del gruppo (somma durate entità + transizioni)
        - 'total_frames': Durata totale del gruppo in frames (per debug/log)
        - 'background_path': Path al background (solo per primo gruppo)
    """
    all_entities: List[Dict[str, Any]] = []
    
    # Estrai tutte le entità da tutte le categorie
    entity_categories = ["Date", "Nomi_Speciali", "Numeri", "Frasi_Importanti", "Entita_Senza_Testo"]
    
    for category in entity_categories:
        category_data = associazioni_finali_con_timestamp.get(category, {})
        
        if not category_data:
            continue
        
        # Gestisci diversi formati di dati
        if isinstance(category_data, dict):
            # Formato: { "chiave": [{"timestamp_start": ..., "timestamp_end": ..., "text": ...}, ...] }
            # OPPURE: { "chiave": {"Link immagine": [...], "Timestamps": [...]} } per Entita_Senza_Testo
            for key, value in category_data.items():
                # Gestisci formato Entita_Senza_Testo con struttura {"Link immagine": [...], "Timestamps": [...]}
                if category == "Entita_Senza_Testo" and isinstance(value, dict):
                    link_list = value.get("Link immagine", [])
                    timestamps_list = value.get("Timestamps", [])
                    
                    if not isinstance(link_list, list):
                        link_list = [link_list] if link_list else []
                    if not isinstance(timestamps_list, list):
                        timestamps_list = []
                    
                    # Associa ogni timestamp al suo path immagine
                    for entry_idx, entry in enumerate(timestamps_list):
                        try:
                            s = float(entry.get("timestamp_start", -1))
                            e = float(entry.get("timestamp_end", -1))
                        except (ValueError, TypeError):
                            continue
                        
                        if s < 0 or e <= s:
                            continue
                        
                        start_v = map_audio_to_video(s)
                        if start_v > video_duration_limit:
                            continue
                        
                        max_dur = video_duration_limit - start_v
                        dur_v = min(e - s, max_dur)
                        if dur_v < 0.1:
                            continue
                        
                        # Trova il path immagine corrispondente
                        image_path = _extract_image_path(entry, key, link_list=link_list)
                        if not image_path and link_list:
                            # Usa l'indice dell'entry per trovare il path corrispondente
                            if entry_idx < len(link_list):
                                image_path = link_list[entry_idx]
                            else:
                                # Se ci sono più timestamp che immagini, usa la prima immagine
                                image_path = link_list[0] if link_list else None
                        
                        if not image_path:
                            logger.warning(f"Immagine non trovata per entità {key} entry {entry_idx}")
                            continue
                        
                        entity = {
                            'type': category,
                            'content': image_path,
                            'start_v': start_v,
                            'dur_v': dur_v,
                            'original_entry': entry,
                            'image_path': image_path,  # Salva anche qui per riferimento
                        }
                        
                        all_entities.append(entity)
                    continue
                
                # Formato standard: lista di entry
                if not isinstance(value, list):
                    continue
                
                entries = value
                for entry in entries:
                    try:
                        s = float(entry.get("timestamp_start", -1))
                        e = float(entry.get("timestamp_end", -1))
                    except (ValueError, TypeError):
                        continue
                    
                    if s < 0 or e <= s:
                        continue
                    
                    start_v = map_audio_to_video(s)
                    if start_v > video_duration_limit:
                        continue
                    
                    max_dur = video_duration_limit - start_v
                    dur_v = min(e - s, max_dur)
                    if dur_v < 0.1:
                        continue
                    
                    # Estrai contenuto
                    # Per Nomi_Speciali e Frasi_Importanti usare la chiave (nome/frase da mostrare),
                    # non entry["text"] che può essere contesto/trascrizione completa
                    if category == "Nomi_Speciali":
                        content = key
                    elif category == "Frasi_Importanti":
                        content = key
                    else:
                        content = entry.get("text", key)
                    # Escludi Date non valide (es. "recent months" senza cifre)
                    if category == "Date" and not _is_likely_real_date(content):
                        logger.info(
                            "[EntityStack] Salto Date non valida (nessuna cifra): content='%s'",
                            (content or "")[:50],
                        )
                        continue
                    if category == "Entita_Senza_Testo":
                        # Per immagini, cerca il path in diversi formati
                        # Formato 1: image_path nell'entry
                        image_path = _extract_image_path(entry, key)
                        
                        if not image_path:
                            logger.warning(f"Immagine non trovata per entità {key} entry {entry}")
                            continue
                        content = image_path
                    
                    entity = {
                        'type': category,
                        'content': content,
                        'start_v': start_v,
                        'dur_v': dur_v,
                        'original_entry': entry,
                    }
                    
                    if category == "Entita_Senza_Testo":
                        entity['image_path'] = content  # Salva anche qui per riferimento
                    
                    all_entities.append(entity)
        elif isinstance(category_data, list):
            # Formato alternativo: lista diretta
            for entry in category_data:
                try:
                    s = float(entry.get("timestamp_start", -1))
                    e = float(entry.get("timestamp_end", -1))
                except (ValueError, TypeError):
                    continue
                
                if s < 0 or e <= s:
                    continue
                
                start_v = map_audio_to_video(s)
                if start_v > video_duration_limit:
                    continue
                
                max_dur = video_duration_limit - start_v
                dur_v = min(e - s, max_dur)
                if dur_v < 0.1:
                    continue
                
                # Formato lista diretta: ci aspettiamo che il testo sia dentro l'entry.
                # Se manca, lasciamo vuoto (meglio che usare placeholder).
                content = entry.get("text") or entry.get("content") or ""
                if category == "Date" and not _is_likely_real_date(content):
                    logger.info("[EntityStack] Salto Date non valida (lista, nessuna cifra): content='%s'", (content or "")[:50])
                    continue
                if category == "Entita_Senza_Testo":
                    # Per immagini, cerca il path in diversi formati
                    image_path = _extract_image_path(entry, "")
                    
                    if not image_path:
                        logger.warning(f"Immagine non trovata per entry {entry}")
                        continue
                    content = image_path
                
                entity = {
                    'type': category,
                    'content': content,
                    'start_v': start_v,
                    'dur_v': dur_v,
                    'original_entry': entry,
                }
                
                all_entities.append(entity)
    
    # Raggruppa entità vicine
    all_entities = _filter_contained_entities(all_entities, max_distance_seconds)
    entity_groups = group_entities_for_stack(all_entities, max_distance_seconds)
    
    # Converti ogni gruppo in formato Remotion
    stack_groups: List[Dict[str, Any]] = []
    
    safe_fps = int(fps) if int(fps) > 0 else 30
    safe_hold_seconds = float(hold_seconds) if hold_seconds is not None else 2.0
    safe_min_entity_seconds = float(min_entity_seconds) if min_entity_seconds is not None else 2.0
    desired_hold_frames = max(0, int(round(safe_hold_seconds * safe_fps)))
    min_entity_frames = max(1, int(round(safe_min_entity_seconds * safe_fps)))

    for group_idx, group in enumerate(entity_groups):
        if not group:
            continue
        
        # Calcola start_v e durata totale del gruppo (considerando estensioni)
        start_v = min(e['start_v'] for e in group)
        last_entity = group[-1]
        last_start = last_entity['start_v']
        last_dur = last_entity['dur_v']
        total_duration = max(0.0, (last_start - start_v) + last_dur)
        total_frames = max(1, int(round(total_duration * safe_fps)))

        # Converti entità in formato Remotion
        remotion_entities: List[Dict[str, Any]] = []
        n_entities = len(group)
        slot_frames = total_frames // n_entities if n_entities > 0 else total_frames
        remainder = total_frames % n_entities if n_entities > 0 else 0

        def _overlaps(a_start: float, a_dur: float, b_start: float, b_dur: float) -> bool:
            a_end = a_start + a_dur
            b_end = b_start + b_dur
            return a_start < b_end and b_start < a_end

        # Animazione casuale per ogni layer quando stile stack è DEFAULT
        randomize_layer = (stack_layer_style or "").upper() in ("DEFAULT", "")
        for entity_idx, entity in enumerate(group):
            # Se è un numero e sullo stesso asse temporale c'è un'altra entità (nome/frase/data/immagine),
            # non mostrare il numero per evitare di "rompere" lo stack: si mostra l'altra entità.
            if entity.get('type') == 'Numeri':
                es = entity.get('start_v', 0)
                ed = entity.get('dur_v', 0)
                has_overlap = False
                for other in group:
                    if other.get('type') == 'Numeri':
                        continue
                    os = other.get('start_v', 0)
                    od = other.get('dur_v', 0)
                    if _overlaps(es, ed, os, od):
                        has_overlap = True
                        break
                if has_overlap:
                    logger.debug(
                        "[EntityStack] Skip NUMBER (overlap con altra entità): "
                        "content=%s start=%.2f dur=%.2f", entity.get('content', '')[:30], es, ed
                    )
                    continue

            try:
                remotion_entity = convert_entity_to_remotion_format(
                    entity,
                    entity['type'],
                    remotion_style_manager,
                    fps=safe_fps,
                    stack_layer_style=stack_layer_style,
                    randomize_layer_animation=randomize_layer,
                )
            except ValueError as ve:
                logger.warning(f"[EntityStack] Skip entità senza contenuto valido: {ve}")
                continue
            
            # Solo la prima entità del primo gruppo ha background
            if group_idx == 0 and entity_idx == 0:
                remotion_entity['has_background'] = True
            else:
                remotion_entity['has_background'] = False

            # Equalizza la durata delle entità all'interno del timeframe del gruppo:
            # ogni entità resta visibile ~ (durata_gruppo / N)
            if equalize_durations and n_entities > 0:
                d = slot_frames + (1 if entity_idx < remainder else 0)
                # Enforce a minimum per-entity duration (to avoid ultra-short flashes)
                remotion_entity['duration'] = max(1, int(d), int(min_entity_frames))

            # "Fermo immagine" negli ultimi secondi: blocca l'animazione negli ultimi holdFrames
            # Clamp per evitare di congelare durante l'entrata/transizione.
            max_hold = max(0, int(remotion_entity.get('duration', 1)) - ENTITY_STACK_TRANSITION_FRAMES)
            # Keep hold short relative to each entity to avoid one dominating the merge
            max_hold_by_ratio = int(max(0, round(float(remotion_entity.get('duration', 1)) * 0.35)))
            hold_frames = min(desired_hold_frames, max_hold, max_hold_by_ratio)
            if hold_frames > 0:
                remotion_entity['holdFrames'] = int(hold_frames)
            
            remotion_entities.append(remotion_entity)

        # Se tutte le entità del gruppo sono state scartate, salta il gruppo
        if not remotion_entities:
            logger.warning(f"[EntityStack] Gruppo {group_idx+1} vuoto dopo filtri (contenuto mancante), salto.")
            continue

        # Recompute total duration from actual entity durations to keep overlay timing consistent
        try:
            total_frames = sum(int(e.get('duration', 0) or 0) for e in remotion_entities)
            total_frames = max(1, int(total_frames))
            total_duration = float(total_frames) / float(safe_fps)
        except Exception:
            pass
        # Clamp to available video duration to avoid overrun.
        try:
            max_allowed = max(0.0, float(video_duration_limit) - float(start_v))
            if max_allowed > 0:
                total_duration = min(total_duration, max_allowed)
                total_frames = max(1, int(round(total_duration * safe_fps)))
        except Exception:
            pass
        
        logger.debug(
            f"Gruppo {group_idx}: start={start_v:.2f}s, "
            f"ultima entità start={last_start:.2f}s dur={last_dur:.2f}s, "
            f"total_duration={total_duration:.2f}s, fps={safe_fps}, total_frames={total_frames}, "
            f"equalize_durations={equalize_durations}, hold_seconds={safe_hold_seconds}, "
            f"min_entity_seconds={safe_min_entity_seconds}"
        )
        
        stack_group = {
            'entities': remotion_entities,
            'start_v': start_v,
            'total_duration': total_duration,
            'total_frames': total_frames,
            # Usa sempre il background utente se disponibile. Il TSX farà fallback alla griglia se non c'è.
            'background_path': background_path,
            # Stile multi-layer stack (scelto una volta per video) per render_entity_stack
            'stack_layer_style': stack_layer_style,
        }
        
        try:
            summary = " | ".join([
                f"{e.get('type')} dur={e.get('duration')}f content='{str(e.get('content'))[:60]}'"
                for e in remotion_entities
            ])
            logger.info(f"[EntityStack] Gruppo {group_idx+1} -> {summary}")
        except Exception:
            pass

        stack_groups.append(stack_group)
    
    # Log dettagliato delle entità estratte per categoria
    entity_counts_by_type = {}
    for entity in all_entities:
        entity_type = entity.get('type', 'UNKNOWN')
        entity_counts_by_type[entity_type] = entity_counts_by_type.get(entity_type, 0) + 1
    
    logger.info(
        f"Preparati {len(stack_groups)} gruppi EntityStack da {len(all_entities)} entità totali: "
        f"{', '.join([f'{count} {t}' for t, count in entity_counts_by_type.items()])}"
    )
    
    # Log dettagliato dei gruppi finali
    for i, stack_group in enumerate(stack_groups):
        entities = stack_group.get('entities', [])
        if len(entities) > 1:
            entity_types = [e.get('type', 'UNKNOWN') for e in entities]
            logger.info(
                f"Stack Group {i+1}: {len(entities)} entità - "
                f"Tipi: {', '.join(entity_types)} "
                f"(start: {stack_group.get('start_v', 0):.2f}s, "
                f"durata: {stack_group.get('total_duration', 0):.2f}s)"
            )
    
    return stack_groups
