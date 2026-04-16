#!/usr/bin/env python3
"""
Helper module per inviare log e status al master server.
"""

import os
import logging
import requests
import threading
import time
from typing import Optional, Dict, Any
from datetime import datetime

logger = logging.getLogger(__name__)

# Variabili globali per configurazione
_MASTER_URL: Optional[str] = None
_WORKER_ID: Optional[str] = None
_JOB_ID: Optional[str] = None
_ENABLED: bool = False
_REQUEST_TIMEOUT: int = 5

# Buffer per log da inviare in batch
_log_buffer: list = []
_log_buffer_lock = threading.Lock()
_log_sending_active = threading.Event()


def configure(master_url: str, worker_id: str, job_id: Optional[str] = None, enabled: bool = True):
    """Configura il logger per inviare log al master."""
    global _MASTER_URL, _WORKER_ID, _JOB_ID, _ENABLED
    _MASTER_URL = master_url.rstrip('/')
    _WORKER_ID = worker_id
    _JOB_ID = job_id
    _ENABLED = enabled
    
    if enabled:
        _log_sending_active.set()
        # Avvia thread per invio log in batch
        thread = threading.Thread(target=_send_logs_worker, daemon=True)
        thread.start()
        logger.info(f"📤 Master logger configurato: master={master_url}, worker={worker_id}, job={job_id}")


def set_job_id(job_id: str):
    """Imposta il job_id corrente."""
    global _JOB_ID
    _JOB_ID = job_id


# Pattern di messaggi da non inviare al master (log superflui durante il job video)
_SKIP_PATTERNS = (
    "polling #",
    "heartbeat ok",
    "💓 heartbeat ok",
    "📡 polling #",
    "get_job: no job",
    "checking for updates",
    "maintenance.check_updates",
)


def _is_job_useful_log(message: str, level: str) -> bool:
    """True se il messaggio è utile per il job video (da mostrare in dashboard)."""
    if not message or not isinstance(message, str):
        return False
    msg_lower = message.lower().strip()
    # Errori e warning sempre utili
    if level.upper() in ("ERROR", "CRITICAL", "WARNING"):
        return True
    # Salta messaggi di polling/heartbeat/maintenance
    for skip in _SKIP_PATTERNS:
        if skip in msg_lower:
            return False
    return True


def send_log(level: str, message: str, job_id: Optional[str] = None):
    """Invia un log al master server. Durante il job video invia solo log utili (no polling/heartbeat)."""
    if not _ENABLED or not _MASTER_URL:
        return
    
    job_id = job_id or _JOB_ID
    if not job_id:
        return  # Non inviare log senza job_id
    
    if not _is_job_useful_log(message, level):
        return
    
    timestamp = datetime.now().isoformat()
    # Limita lunghezza messaggio per non appesantire la coda
    msg_capped = (message[:2000] + "...") if len(message) > 2000 else message
    log_entry = {
        "timestamp": timestamp,
        "message": msg_capped,
        "is_error": level.upper() in ["ERROR", "CRITICAL", "WARNING"]
    }
    
    with _log_buffer_lock:
        _log_buffer.append(log_entry)
        if len(_log_buffer) > 200:
            _log_buffer.pop(0)


def send_status(job_id: Optional[str], progress: int, message: str = ""):
    """Invia uno status update al master server."""
    if not _ENABLED or not _MASTER_URL:
        return
    
    job_id = job_id or _JOB_ID
    if not job_id:
        return
    
    try:
        requests.post(
            f"{_MASTER_URL}/update_job_logs",
            json={
                "job_id": job_id,
                "worker_id": _WORKER_ID,
                "logs": [{
                    "timestamp": datetime.now().isoformat(),
                    "message": f"📊 Progress: {progress}% - {message}",
                    "is_error": False
                }]
            },
            timeout=_REQUEST_TIMEOUT
        )
    except Exception as e:
        logger.debug(f"Errore invio status al master: {e}")


def _send_logs_worker():
    """Worker thread che invia log in batch ogni 3 secondi."""
    consecutive_failures = 0
    max_failures_before_warning = 5
    
    while _log_sending_active.is_set():
        try:
            time.sleep(3)  # Invia ogni 3 secondi
            
            if not _log_buffer or not _JOB_ID:
                continue
            
            # Prendi log dal buffer
            with _log_buffer_lock:
                logs_to_send = _log_buffer.copy()
                _log_buffer.clear()
            
            if not logs_to_send:
                continue
            
            # Invia al master
            try:
                response = requests.post(
                    f"{_MASTER_URL}/update_job_logs",
                    json={
                        "job_id": _JOB_ID,
                        "worker_id": _WORKER_ID,
                        "logs": logs_to_send
                    },
                    timeout=_REQUEST_TIMEOUT
                )
                if response.status_code == 200:
                    consecutive_failures = 0
                    logger.debug(f"📤 Inviati {len(logs_to_send)} log al master per job {_JOB_ID[:8] if _JOB_ID else 'N/A'}...")
                else:
                    consecutive_failures += 1
                    # Rimetti i log nel buffer se l'invio fallisce
                    with _log_buffer_lock:
                        _log_buffer.extend(logs_to_send)
            except requests.exceptions.Timeout:
                consecutive_failures += 1
                if consecutive_failures >= max_failures_before_warning:
                    logger.warning(f"⚠️ Timeout invio log al master ({consecutive_failures} tentativi falliti)")
                # Rimetti i log nel buffer
                with _log_buffer_lock:
                    _log_buffer.extend(logs_to_send)
            except Exception as e:
                consecutive_failures += 1
                if consecutive_failures >= max_failures_before_warning:
                    logger.debug(f"Errore invio log al master: {e}")
                # Rimetti i log nel buffer
                with _log_buffer_lock:
                    _log_buffer.extend(logs_to_send)
        except Exception as e:
            logger.error(f"Errore critico nel thread invio log: {e}", exc_info=True)


def create_status_callback_with_logging(base_callback=None):
    """Crea un callback che invia anche log al master."""
    def combined_callback(message: str, is_error: bool = False):
        # Chiama il callback originale se presente
        if base_callback:
            try:
                base_callback(message, is_error)
            except Exception:
                pass
        
        # Invia log al master
        level = "ERROR" if is_error else "INFO"
        send_log(level, message)
    
    return combined_callback
