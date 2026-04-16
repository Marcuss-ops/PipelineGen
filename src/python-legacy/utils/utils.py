"""Utility functions for VideoCollegaTanti application."""

import os
import shutil
import logging
import tempfile
import time
import hashlib
import random
import requests
import validators
from typing import List, Optional, Callable, Any
from pathlib import Path
from PIL import Image
from urllib.parse import urlparse, unquote
import re
import json
import unicodedata
from datetime import datetime
from rapidfuzz import fuzz

logger = logging.getLogger(__name__)


def safe_remove(path: str) -> None:
    """Safely removes a file, ignoring errors if it doesn't exist."""
    try:
        if path and os.path.exists(path):
            os.remove(path)
    except OSError:
        pass  # Log error if needed


def cleanup_temp_files(files_and_dirs: List[str], status_callback: Callable[[str, bool], None]) -> None:
    """Cleans up a list of temporary files and directories."""
    unique_items = sorted(list(set(files_and_dirs)), key=len, reverse=True)

    for item_path in unique_items:
        try:
            if os.path.isdir(item_path):
                shutil.rmtree(item_path)
                status_callback(f"🗑️ Rimosso directory temp e contenuto: {item_path}", False)
            elif os.path.isfile(item_path):
                os.remove(item_path)
                status_callback(f"🗑️ Rimosso file temp: {item_path}", False)
        except FileNotFoundError:
            pass  # Already gone, no issue
        except Exception as e_clean:
            status_callback(f"⚠️ Errore rimozione '{item_path}': {e_clean}", True)
            logger.warning(f"Cleanup error for '{item_path}':", exc_info=True)
    
    # Pulisci anche la cache delle transizioni
    try:
        from video_processing import cleanup_transition_cache  # type: ignore
        cleanup_transition_cache()
        status_callback("🗑️ Cache transizioni pulita", False)
    except ImportError:
        pass  # video_processing non disponibile
    except Exception as e:
        status_callback(f"⚠️ Errore pulizia cache transizioni: {e}", True)


def download_image(image_urls: List[str], dest_path: str, status_callback: Callable[[str], None]) -> Optional[str]:
    """
    Downloads an image from a list of URLs and saves it to dest_path.
    Uses cache manager to avoid re-downloading images.

    Args:
        image_urls: List of URLs of the images to download.
        dest_path: The file path to save the downloaded image.
        status_callback: Function to report status updates.

    Returns:
        str: Path to the downloaded image if successful, None otherwise.
    """
    try:
        from modules.utils.cache_manager import get_cache_manager
    except ImportError:
        try:
            from .cache_manager import get_cache_manager
        except ImportError:
            from cache_manager import get_cache_manager
    
    cache_manager = get_cache_manager()
    max_retries = 5  # Increased retries
    retry_delay = 2  # Increased delay
    # Enhanced headers to better mimic a browser
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
        "Accept": "image/webp,image/apng,image/*,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.9",
        "Accept-Encoding": "gzip, deflate, br",
        "Referer": "https://duckduckgo.com/",
        "Connection": "keep-alive",
        "Sec-Fetch-Dest": "image",
        "Sec-Fetch-Mode": "no-cors",
        "Sec-Fetch-Site": "cross-site"
    }

    for url in image_urls:
        # Handle DuckDuckGo image proxy URLs specifically
        if "duckduckgo.com/iu/" in url:
            # Try to extract the actual image URL from the DuckDuckGo proxy URL
            try:
                from urllib.parse import parse_qs, urlparse
                parsed = urlparse(url)
                query_params = parse_qs(parsed.query)
                if 'u' in query_params:
                    # This is the actual image URL
                    actual_url = query_params['u'][0]
                    logger.info(f"Extracted actual image URL from DuckDuckGo proxy: {actual_url}")
                    # Try the actual URL first
                    result = _download_single_image(actual_url, dest_path, status_callback, headers, max_retries, retry_delay, cache_manager)
                    if result:
                        return result
            except Exception as e:
                logger.warning(f"Could not extract actual URL from DuckDuckGo proxy: {e}")
        
        if not validators.url(url):
            status_callback(f"❌ Invalid URL: {url}")
            logger.error(f"Invalid URL: {url}")
            continue

        # Try downloading with the original URL
        result = _download_single_image(url, dest_path, status_callback, headers, max_retries, retry_delay, cache_manager)
        if result:
            return result
            
    return None


def _download_single_image(url: str, dest_path: str, status_callback: Callable[[str], None], 
                          headers: dict, max_retries: int, retry_delay: int, cache_manager) -> Optional[str]:
    """Helper function to download a single image."""
    # Check cache first
    cached_path = cache_manager.get_cached_image(url)
    if cached_path:
        status_callback(f"✅ Image found in cache: {url}")
        # Copy from cache to destination
        try:
            shutil.copy2(cached_path, dest_path)
            return dest_path
        except Exception as e:
            status_callback(f"⚠️ Error copying from cache, downloading fresh: {e}")
            # Continue to download if cache copy fails

    for attempt in range(max_retries):
        try:
            status_callback(f"ℹ️ Downloading image from {url} to {dest_path} (Attempt {attempt + 1}/{max_retries})")
            response = requests.get(url, headers=headers, timeout=30, stream=True)  # Increased timeout
            response.raise_for_status()

            # Check if we got an image
            content_type = response.headers.get('content-type', '')
            if not content_type.startswith('image/'):
                status_callback(f"⚠️ URL does not appear to be an image (content-type: {content_type})")
                if attempt < max_retries - 1:
                    time.sleep(retry_delay)
                    continue
                return None

            # Write the image to disk
            with open(dest_path, "wb") as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if chunk:
                        f.write(chunk)

            # Verify the image
            try:
                with Image.open(dest_path) as img:
                    img.verify()  # Check if the file is a valid image
                status_callback(f"✅ Image downloaded and verified: {dest_path}")
                
                # Check and enhance image quality if needed
                enhanced_path = _enhance_image_if_needed(dest_path, status_callback)
                final_path = enhanced_path if enhanced_path else dest_path
                
                # Cache the final image (enhanced or original)
                try:
                    cache_manager.cache_image(url, final_path)
                    status_callback(f"📦 Image cached for future use")
                except Exception as e:
                    logger.warning(f"Could not cache image {url}: {e}")
                
                return final_path
            except Exception as e:
                status_callback(f"⚠️ Downloaded file is not a valid image: {dest_path}, error: {e}")
                logger.warning(f"Invalid image file: {dest_path}, error: {e}")
                try:
                    os.remove(dest_path)
                    logger.info(f"Removed invalid image file: {dest_path}")
                except OSError:
                    pass
                if attempt < max_retries - 1:
                    time.sleep(retry_delay)
                    continue
                return None

        except requests.exceptions.RequestException as e:
            status_callback(f"❌ Download error ({url}): {e}")
            logger.error(f"Download error ({url}): {e}")
            if attempt < max_retries - 1:
                time.sleep(retry_delay)
                continue
            return None
        except IOError as e:
            status_callback(f"❌ File write error ({dest_path}): {e}")
            logger.error(f"File write error ({dest_path}): {e}")
            return None
        except Exception as e:
            status_callback(f"❌ Unexpected error downloading image ({url}): {e}")
            logger.error(f"Unexpected error downloading image ({url}): {e}", exc_info=True)
            if attempt < max_retries - 1:
                time.sleep(retry_delay)
                continue
            return None
    return None


def scarica_immagine(url, cartella=r"C:\Users\pater\OneDrive\Documenti\cupcaut\Background\Bkgd"):
    """
    Scarica l'immagine dall'URL specificato e la salva in alta qualità.
    Utilizza il cache manager per evitare di ri-scaricare immagini.
    
    Se 'url' è un dizionario, tenta di estrarre il valore associato alla chiave "image".
    Viene generato un nome file unico aggiungendo un numero casuale.
    
    Per i file JPEG, vengono utilizzati quality=100, subsampling=0 e optimize=True.
    Per i file PNG, viene usato compress_level=0 per ridurre la compressione.
    """
    import io
    
    try:
        from modules.utils.cache_manager import get_cache_manager
    except ImportError:
        try:
            from .cache_manager import get_cache_manager
        except ImportError:
            from cache_manager import get_cache_manager
    
    cache_manager = get_cache_manager()
    
    # Se 'url' è un dizionario, estrai il valore associato alla chiave "image"
    if isinstance(url, dict):
        url = url.get("image")
        if not url:
            logging.error("❌ Nessun URL trovato nel dizionario.")
            return None

    # Se il parametro "url" è già un percorso di file locale, non tentare di scaricarlo di nuovo
    if os.path.isfile(url):
        logging.info(f"✅ L'immagine è già locale: {url}")
        return url

    try:
        # Controlla prima nella cache
        cached_path = cache_manager.get_cached_image(url)
        if cached_path:
            # Genera il nome del file per la destinazione
            parsed_url = urlparse(url)
            nome_file = os.path.basename(parsed_url.path)
            nome_file = unquote(nome_file)
            nome_file = re.sub(r'[\\/*?:"<>|]', "_", nome_file)
            
            # Aggiungi un numero casuale per garantire l'univocità
            numero_casuale = random.randint(100000, 999999)
            if not nome_file:
                nome_file = f"immagine_HQ_{numero_casuale}.jpg"
            else:
                estensioni_valide = ['.png', '.jpg', '.jpeg']
                if not any(nome_file.lower().endswith(ext) for ext in estensioni_valide):
                    nome_file += ".jpg"
                base, ext = os.path.splitext(nome_file)
                nome_file = f"{base}_{numero_casuale}{ext}"
                
            # Trunca il nome se troppo lungo
            nome_file = nome_file[:200]
            
            # Crea la cartella se non esiste
            if not os.path.exists(cartella):
                os.makedirs(cartella)
                
            percorso_completo = os.path.join(cartella, nome_file)
            
            # Copia dalla cache alla destinazione
            try:
                shutil.copy2(cached_path, percorso_completo)
                logging.info(f"✅ Immagine copiata dalla cache: {percorso_completo}")
                return percorso_completo
            except Exception as e:
                logging.warning(f"⚠️ Errore nella copia dalla cache, scarico di nuovo: {e}")
                # Continua con il download se la copia dalla cache fallisce
        
        headers = {
            'User-Agent': ('Mozilla/5.0 (Windows NT 10.0; Win64; x64) '
                           'AppleWebKit/537.36 (KHTML, like Gecko) '
                           'Chrome/107.0.0.0 Safari/537.36'),
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8',
            'Accept-Language': 'en-US,en;q=0.9',
            'Referer': 'https://duckduckgo.com/'
        }
        
        # Crea la cartella se non esiste
        if not os.path.exists(cartella):
            os.makedirs(cartella)
            
        response = requests.get(url, timeout=15, headers=headers)
        response.raise_for_status()
        
        # Apri l'immagine con Pillow per determinare il formato
        img = Image.open(io.BytesIO(response.content))
        formato = img.format.lower()
        
        # Genera il nome del file partendo dall'URL
        parsed_url = urlparse(url)
        nome_file = os.path.basename(parsed_url.path)
        nome_file = unquote(nome_file)
        nome_file = re.sub(r'[\\/*?:"<>|]', "_", nome_file)
        
        # Aggiungi un numero casuale per garantire l'univocità
        numero_casuale = random.randint(100000, 999999)
        if not nome_file:
            nome_file = f"immagine_HQ_{numero_casuale}.{formato}"
        else:
            estensioni_valide = ['.png', '.jpg', '.jpeg']
            if not any(nome_file.lower().endswith(ext) for ext in estensioni_valide):
                # Se l'estensione non c'è, la aggiunge in base al formato rilevato
                nome_file += f".{formato}" if formato in ['png', 'jpeg', 'jpg'] else ".jpg"
            base, ext = os.path.splitext(nome_file)
            nome_file = f"{base}_{numero_casuale}{ext}"
            
        # Trunca il nome se troppo lungo
        nome_file = nome_file[:200]
        percorso_completo = os.path.join(cartella, nome_file)
        
        # Salva l'immagine con i parametri che garantiscono la massima qualità
        if formato in ["jpeg", "jpg"]:
            # Per JPEG: quality=100, subsampling=0 e optimize=True
            img.save(percorso_completo, format=img.format, quality=100, subsampling=0, optimize=True)
        elif formato == "png":
            # Per PNG: compress_level=0 per evitare compressione eccessiva (lossless)
            img.save(percorso_completo, format=img.format, compress_level=0)
        else:
            # Per altri formati, un default che prova a mantenere la qualità
            img.save(percorso_completo, quality=100)
            
        # Mette in cache l'immagine scaricata
        try:
            cache_manager.cache_image(url, percorso_completo)
            logging.info(f"✅ Immagine HQ salvata e messa in cache: {percorso_completo}")
        except Exception as e:
            logging.warning(f"⚠️ Impossibile mettere in cache l'immagine {url}: {e}")
            logging.info(f"✅ Immagine HQ salvata in: {percorso_completo}")
        
        return percorso_completo
    
    except Exception as e:
        logging.error(f"❌ Errore durante il download: {str(e)}")
        return None


def clean_text(text: str) -> str:
    """Clean and normalize text by removing extra whitespace."""
    return ' '.join(text.strip().split())


def generate_unique_filename(base_name: str, extension: str, directory: str) -> str:
    """Generate a unique filename in the given directory."""
    counter = 1
    filename = f"{base_name}.{extension}"
    full_path = os.path.join(directory, filename)
    
    while os.path.exists(full_path):
        filename = f"{base_name}_{counter}.{extension}"
        full_path = os.path.join(directory, filename)
        counter += 1
    
    return full_path


def create_temp_directory(prefix: str = "video_processing_") -> str:
    """Create a temporary directory with the given prefix."""
    return tempfile.mkdtemp(prefix=prefix)


def get_file_hash(file_path: str) -> str:
    """Calculate MD5 hash of a file."""
    hash_md5 = hashlib.md5()
    try:
        with open(file_path, "rb") as f:
            for chunk in iter(lambda: f.read(4096), b""):
                hash_md5.update(chunk)
        return hash_md5.hexdigest()
    except Exception as e:
        logger.error(f"Error calculating hash for {file_path}: {e}")
        return ""


def ensure_directory_exists(directory: str) -> bool:
    """Ensure a directory exists, create it if it doesn't."""
    try:
        os.makedirs(directory, exist_ok=True)
        return True
    except Exception as e:
        logger.error(f"Failed to create directory {directory}: {e}")
        return False


def format_duration(seconds: float) -> str:
    """Format duration in seconds to human-readable format."""
    hours = int(seconds // 3600)
    minutes = int((seconds % 3600) // 60)
    secs = int(seconds % 60)
    
    if hours > 0:
        return f"{hours:02d}:{minutes:02d}:{secs:02d}"
    else:
        return f"{minutes:02d}:{secs:02d}"


def validate_file_path(file_path: str, extensions: List[str] = None) -> bool:
    """Validate if a file path exists and has the correct extension."""
    if not file_path or not os.path.exists(file_path):
        return False
    
    if extensions:
        file_ext = os.path.splitext(file_path)[1].lower()
        return file_ext in [ext.lower() for ext in extensions]
    
    return True


def get_available_memory() -> float:
    """Get available system memory in GB."""
    try:
        import psutil
        return psutil.virtual_memory().available / (1024**3)
    except ImportError:
        logger.warning("psutil not available, cannot get memory info")
        return 0.0


def normalize_text(text: str) -> str:
    """Normalizza il testo per il matching."""
    # Converti in minuscolo
    text = text.lower()
    
    # Rimuovi accenti
    text = unicodedata.normalize('NFD', text)
    text = ''.join(c for c in text if unicodedata.category(c) != 'Mn')
    
    # Rimuovi punteggiatura e caratteri speciali
    text = re.sub(r'[^\w\s]', ' ', text)
    
    # Rimuovi spazi multipli
    text = re.sub(r'\s+', ' ', text).strip()
    
    return text


def _segment_get(segment: Any, key: str, default: Any = None) -> Any:
    """
    Best-effort accessor for transcription segments.

    Supports:
    - dict-like segments: {"start": ..., "end": ..., "text": ...}
    - dataclass/objects with attributes: segment.start, segment.end, segment.text
    """
    try:
        if isinstance(segment, dict):
            return segment.get(key, default)
        if hasattr(segment, key):
            return getattr(segment, key, default)
        getter = getattr(segment, "get", None)
        if callable(getter):
            return getter(key, default)
    except Exception:
        return default
    return default


def match_date_with_timestamp(segments: List[Any], segment_text: str) -> dict:
    """Cerca una data nei segmenti di trascrizione e restituisce timestamp."""
    # Pattern per date comuni
    date_patterns = [
        r'\b\d{1,2}[/-]\d{1,2}[/-]\d{2,4}\b',  # 12/31/2023, 31-12-23
        r'\b\d{1,2}\s+[a-zA-Z]+\s+\d{2,4}\b',  # 31 dicembre 2023
        r'\b[a-zA-Z]+\s+\d{1,2},?\s+\d{2,4}\b',  # dicembre 31, 2023
        r'\b(19|20)\d{2}\b',  # Anni isolati: 1984, 2023
        r'\b(19|20)\d{2}s\b',  # Decenni: 1970s, 1980s
        r'\bearly\s+(19|20)\d{2}s\b',  # early 1980s
        r'\blate\s+(19|20)\d{2}s\b',  # late 1970s
        r'\bmid\s+(19|20)\d{2}s\b'  # mid 1980s
    ]
    
    # Cerca la data nel testo del segmento
    for pattern in date_patterns:
        match = re.search(pattern, segment_text, re.IGNORECASE)
        if match:
            # Usa la data trovata come valore
            found_date = match.group(0)
            
            # Trova il segmento corrispondente
            for segment in segments:
                text = _segment_get(segment, "text", "") or ""
                if found_date in text:
                    return {
                        'date': found_date,  # Usa la data effettivamente trovata
                        'start_time': _segment_get(segment, "start", 0) or 0,
                        'end_time': _segment_get(segment, "end", 0) or 0,
                        'segment_text': text
                    }
    
    return {}


def perform_full_association(entities_json: str, phrases_json: str, words_json: str, 
                           entities_no_text_json: str, segments: List[Any], 
                           nomi_speciali_json: str = "") -> dict:
    """Esegue l'associazione completa tra entità e timestamp."""
    try:
        # Parse JSON inputs
        entities = json.loads(entities_json) if entities_json else []
        phrases = json.loads(phrases_json) if phrases_json else []
        words = json.loads(words_json) if words_json else []
        entities_no_text = json.loads(entities_no_text_json) if entities_no_text_json else {}
        nomi_speciali = json.loads(nomi_speciali_json) if nomi_speciali_json else []
        
        result = {
            'Frasi_Importanti': {},
            'Parole_Importanti': {},
            'Nomi_Speciali': {},
            'Nomi_Con_Testo': [],
            'Entita_Senza_Testo': {},
            'Date': {}
        }
        
        # Match phrases, words, and nomi_speciali with segments using improved matching
        for item_list, result_key in [(phrases, 'Frasi_Importanti'),
                                     (words, 'Parole_Importanti'),
                                     (nomi_speciali, 'Nomi_Speciali')]:
            for item in item_list:
                matched = False
                
                # For short items (words), use simple matching
                if len(item.split()) <= 3:
                    normalized_item = normalize_text(item)
                    for segment in segments:
                        segment_text = normalize_text(_segment_get(segment, "text", "") or "")
                        if normalized_item in segment_text:
                            if item not in result[result_key]:
                                result[result_key][item] = []
                            result[result_key][item].append({
                                'timestamp_start': _segment_get(segment, "start", 0) or 0,
                                'timestamp_end': _segment_get(segment, "end", 0) or 0,
                                'segment_text': _segment_get(segment, "text", "") or ""
                            })
                            matched = True
                            break
                else:
                    # For longer phrases, use fuzzy matching and keyword-based approach
                    
                    # Try fuzzy matching first
                    best_match = None
                    best_score = 0
                    
                    for segment in segments:
                        segment_text = _segment_get(segment, "text", "") or ""
                        if segment_text:
                            # Calculate fuzzy similarity
                            score = fuzz.partial_ratio(item.lower(), segment_text.lower())
                            if score > best_score and score >= 75:  # Threshold for fuzzy matching
                                best_score = score
                                best_match = segment
                    
                    if best_match:
                        if item not in result[result_key]:
                            result[result_key][item] = []
                        result[result_key][item].append({
                            'timestamp_start': _segment_get(best_match, "start", 0) or 0,
                            'timestamp_end': _segment_get(best_match, "end", 0) or 0,
                            'segment_text': _segment_get(best_match, "text", "") or ""
                        })
                        matched = True
                    else:
                        # If fuzzy matching fails, try keyword-based matching
                        item_keywords = [word.lower() for word in item.split() if len(word) >= 4]
                        
                        if item_keywords:
                            for segment in segments:
                                segment_text = str(_segment_get(segment, "text", "") or "").lower()
                                keyword_matches = sum(1 for keyword in item_keywords if keyword in segment_text)
                                
                # Require at least 60% of keywords to match
                                if keyword_matches >= len(item_keywords) * 0.6:
                                    if item not in result[result_key]:
                                        result[result_key][item] = []
                                    result[result_key][item].append({
                                        'timestamp_start': _segment_get(segment, "start", 0) or 0,
                                        'timestamp_end': _segment_get(segment, "end", 0) or 0,
                                        'segment_text': _segment_get(segment, "text", "") or ""
                                    })
                                    matched = True
                                    break

                # If no match found, still add the item but with empty timestamps
                if not matched:
                    if item not in result[result_key]:
                        result[result_key][item] = []

        # Handle entities without text
        for entity_key, entity_data in entities_no_text.items():
            # Ensure proper structure for Entita_Senza_Testo
            if isinstance(entity_data, str):
                # If entity_data is a URL string, wrap it in the expected structure
                result['Entita_Senza_Testo'][entity_key] = {
                    "Link immagine": [entity_data],
                    "Timestamps": []
                }
            elif isinstance(entity_data, dict):
                # If it's already a dict, ensure it has the required keys
                result['Entita_Senza_Testo'][entity_key] = {
                    "Link immagine": entity_data.get("Link immagine", []),
                    "Timestamps": entity_data.get("Timestamps", [])
                }
            else:
                # Fallback for other types
                result['Entita_Senza_Testo'][entity_key] = {
                    "Link immagine": [],
                    "Timestamps": []
                }

        # Handle dates - extract dates from segments
        for segment in segments:
            segment_text = _segment_get(segment, "text", "") or ""
            date_match = match_date_with_timestamp(segments, segment_text)
            if date_match and date_match.get('date'):
                date_value = date_match['date']
                # Evita duplicati usando la data come chiave
                if date_value not in result['Date']:
                    result['Date'][date_value] = {
                        'Timestamps': [{
                            'timestamp_start': date_match['start_time'],
                            'timestamp_end': date_match['end_time']
                        }]
                    }
                    logger.info(f"Data trovata: '{date_value}' al timestamp {date_match['start_time']:.2f}s")

        return result

    except Exception as e:
        logger.error(f"Errore in perform_full_association: {e}")
        return {'error': str(e)}


def download_and_test_multiple_images():
    """
    Funzione principale per testare il download di diverse immagini.
    Utilizzo: python utils.py [url1] [url2] ...
    """
    import sys

    # URL di test diversi (rimossi URL specifici della WWE)
    default_test_urls = [
        "https://picsum.photos/400/300",  # Immagine casuale da Lorem Picsum
        "https://via.placeholder.com/300x200.png",  # Placeholder image
        "https://httpbin.org/image/png",  # Test image
    ]

    # Se vengono forniti URL come argomenti, usa quelli invece dei default
    test_urls = sys.argv[1:] if len(sys.argv) > 1 else default_test_urls

    print(f"🚀 Test Download Immagini - Testando {len(test_urls)} immagini")
    print("=" * 60)

    def status_callback(msg):
        print(f"📝 {msg}")

    results = []
    total_time = 0

    try:
        # Crea un percorso temporaneo per il test
        temp_dir = tempfile.mkdtemp(prefix="multi_image_test_")

        for i, test_url in enumerate(test_urls, 1):
            print(f"\n🔄 Test {i}/{len(test_urls)}: {test_url}")
            print("-" * 40)

            # Genera nome file unico per ogni immagine
            file_name = f"test_image_{i}_{hash(test_url) % 1000}.jpg"
            dest_path = os.path.join(temp_dir, file_name)

            # Test download
            start_time = time.time()
            result = download_image([test_url], dest_path, status_callback)
            end_time = time.time()
            duration = end_time - start_time
            total_time += duration

            if result:
                # Mostra informazioni sul file scaricato
                file_size = os.path.getsize(result)

                print("   ✅ SUCCESSO!")
                print(f"   📁 File salvato: {os.path.basename(result)}")
                print(f"   📊 Dimensioni: {file_size} bytes")
                print(f"   ⏱️  Tempo impiegato: {duration:.2f} secondi")

                # Verifica se è un'immagine valida
                try:
                    with Image.open(result) as img:
                        width, height = img.size
                        print(f"   🖼️ Risoluzione: {width}x{height}")
                        print(f"   🎨 Formato: {img.format}")
                        if width > 0 and height > 0:
                            print("   ✅ Immagine valida e completa!")
                            results.append(True)
                        else:
                            print(f"   ⚠️ Immagine con dimensioni invalide")
                            results.append(False)
                except Exception as e:
                    print(f"   ⚠️ File scaricato ma immagine non valida: {e}")
                    results.append(False)
            else:
                print(f"   ❌ FALLIMENTO - Impossibile scaricare l'immagine")
                results.append(False)

        # Riepilogo finale
        successful = sum(results)
        total = len(results)

        print(f"\n🎯 RIEPILOGO FINALE")
        print("=" * 40)
        print(f"Immagini testate: {total}")
        print(f"Download riusciti: {successful}")
        print(f"Download falliti: {total - successful}")
        print(f"⏱️ Tempo totale: {total_time:.2f} secondi")
        print(f"⏱️ Tempo medio: {total_time/max(total, 1):.2f} secondi per immagine")

        if successful > 0:
            print(f"✅ SUCCESSO COMPLETALE - {successful}/{total} immagini scaricate correttamente!")
            return True
        else:
            print(f"❌ TUTTI I DOWNLOAD FALLITI")
            print("Possibili cause:")
            print("  • Nessuno degli URL è raggiungibile")
            print("  • Problemi di rete")
            print("  • Immagini non più disponibili")
            return False

    except Exception as e:
        print(f"\n❌ ERRORE CRITICO durante il test: {e}")
        import traceback
        traceback.print_exc()
        return False

    finally:
        # Pulizia
        try:
            if 'temp_dir' in locals() and os.path.exists(temp_dir):
                import shutil
                shutil.rmtree(temp_dir)
                print(f"\n🧹 Directory temporanea pulita: {temp_dir}")
        except Exception as cleanup_error:
            print(f"⚠️ Errore durante la pulizia: {cleanup_error}")


def _enhance_image_if_needed(image_path: str, status_callback: Callable[[str], None]) -> Optional[str]:
    """
    Check image quality and enhance if needed using CPU-based algorithms.
    
    Args:
        image_path: Path to the downloaded image
        status_callback: Function to report status updates
        
    Returns:
        Path to enhanced image if enhancement was applied, None if no enhancement needed
    """
    try:
        # Temporary safety: allow disabling the enhancer entirely
        if os.environ.get("VELOX_DISABLE_IMAGE_ENHANCER", "1").strip() == "1":
            status_callback("🛑 Image enhancement disabled (VELOX_DISABLE_IMAGE_ENHANCER=1)")
            return None

        # Import the quality enhancer
        try:
            from modules.video.image_quality_enhancer import ImageQualityEnhancer, should_enhance_image
        except ImportError:
            try:
                from .image_quality_enhancer import ImageQualityEnhancer, should_enhance_image  # type: ignore
            except ImportError:
                from image_quality_enhancer import ImageQualityEnhancer, should_enhance_image  # type: ignore
        
        # Quick check if enhancement is needed
        if not should_enhance_image(image_path):
            status_callback("🎯 Image quality is already good, no enhancement needed")
            return None
        
        # Create enhanced version
        enhancer = ImageQualityEnhancer()
        
        # Generate enhanced filename
        base_name, ext = os.path.splitext(image_path)
        enhanced_path = f"{base_name}_enhanced{ext}"
        
        status_callback("🔧 Enhancing image quality...")
        
        # Enhance the image
        result = enhancer.enhance_image(image_path, enhanced_path, 'auto')
        
        if result.get('success', False):
            improvement = result.get('improvement', 0)
            applied_enhancements = result.get('applied_enhancements', [])
            
            status_callback(f"✨ Image enhanced successfully! Quality improved by {improvement:.1f} points")
            status_callback(f"🛠️ Applied: {', '.join(applied_enhancements)}")
            
            # Remove original and rename enhanced
            try:
                os.remove(image_path)
                os.rename(enhanced_path, image_path)
                return image_path
            except Exception as e:
                status_callback(f"⚠️ Error replacing original image: {e}")
                return enhanced_path
        else:
            status_callback(f"⚠️ Image enhancement failed: {result.get('error', 'Unknown error')}")
            return None
            
    except Exception as e:
        status_callback(f"⚠️ Error during image quality enhancement: {e}")
        logger.warning(f"Image enhancement error for {image_path}: {e}")
        return None


def _try_fallback_image_search(entity_name: str, original_path: str, status_callback: Callable[[str], None]) -> Optional[str]:
    """
    Try to find a better quality alternative image when the original is poor quality.
    
    Args:
        entity_name: Name of the entity to search alternative images for
        original_path: Path to the original poor quality image
        status_callback: Function to report status updates
        
    Returns:
        Path to alternative image if found and downloaded, None otherwise
    """
    try:
        # Import the fallback searcher
        try:
            from modules.video.image_fallback_search import ImageFallbackSearcher, should_search_alternative
        except ImportError:
            try:
                from .image_fallback_search import ImageFallbackSearcher, should_search_alternative  # type: ignore
            except ImportError:
                from image_fallback_search import ImageFallbackSearcher, should_search_alternative  # type: ignore
        
        # Check if we should search for alternatives
        if not should_search_alternative(original_path):
            status_callback("🎯 Current image quality is acceptable, no fallback needed")
            return None
        
        status_callback(f"🔍 Searching for better quality images for '{entity_name}'...")
        
        # Create fallback searcher
        searcher = ImageFallbackSearcher()
        
        # Generate alternative image path
        base_name, ext = os.path.splitext(original_path)
        alternative_path = f"{base_name}_alternative{ext}"
        
        # Search for and download best alternative
        result = searcher.find_best_alternative(entity_name, alternative_path)
        
        if result:
            status_callback(f"✅ Found better quality alternative image!")
            
            # Replace original with alternative
            try:
                if os.path.exists(original_path):
                    os.remove(original_path)
                os.rename(alternative_path, original_path)
                status_callback(f"🔄 Replaced original image with higher quality version")
                return original_path
            except Exception as e:
                status_callback(f"⚠️ Error replacing original with alternative: {e}")
                return alternative_path
        else:
            status_callback(f"❌ No suitable alternative images found for '{entity_name}'")
            return None
            
    except Exception as e:
        status_callback(f"⚠️ Error during fallback image search: {e}")
        logger.warning(f"Fallback image search error for {entity_name}: {e}")
        return None
