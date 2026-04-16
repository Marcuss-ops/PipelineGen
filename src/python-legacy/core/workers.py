
import json
import socket
import logging
import hashlib
from datetime import datetime, timezone
from pathlib import Path
from typing import Dict, Any, Optional, Set, List

from . import state
from .config import (
    WORKERS_FILE,
    QUARANTINED_WORKERS_FILE,
    REVOKED_WORKERS_FILE,
    PENDING_UPDATES_FILE,
    SCRIPT_DIR
)

try:
    from utils.worker_session_logs import append_worker_session_log
except ImportError:
    def append_worker_session_log(*args, **kwargs): pass

logger = logging.getLogger(__name__)

def load_workers():
    """Carica i worker registrati da disco all'avvio del server."""
    if not WORKERS_FILE.exists():
        logger.info(f"ℹ️ File workers.json non trovato, nessun worker da caricare")
        return
    
    try:
        with open(WORKERS_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
        
        # Conta duplicati prima della pulizia
        total_entries = len(data)
        unique_worker_ids = set(data.keys())
        unique_count = len(unique_worker_ids)
        
        if total_entries != unique_count:
            logger.warning(f"⚠️ Trovati {total_entries} entry in workers.json ma solo {unique_count} worker_id unici!")
            logger.warning(f"   🔍 Rimozione duplicati...")
        
        # Raggruppa per nome per trovare duplicati
        workers_by_name = {}
        for worker_id, info in data.items():
            if isinstance(info, dict):
                worker_name = info.get("worker_name", worker_id)
                if worker_name not in workers_by_name:
                    workers_by_name[worker_name] = []
                workers_by_name[worker_name].append((worker_id, info))
        
        # Trova nomi duplicati
        duplicate_names = {name: entries for name, entries in workers_by_name.items() if len(entries) > 1}
        if duplicate_names:
            logger.warning(f"⚠️ Trovati {len(duplicate_names)} nomi worker duplicati:")
            for name, entries in duplicate_names.items():
                logger.warning(f"   - '{name}': {len(entries)} worker con questo nome")
                for wid, winfo in entries:
                    logger.warning(f"     • ID: {wid[:16]}..., heartbeat: {winfo.get('last_heartbeat', 'N/A')}")
        
        with state.worker_lock:
            loaded_count = 0
            skipped_duplicates = 0
            seen_names = {}  # Traccia i nomi già visti per rimuovere duplicati
            
            for worker_id, info in data.items():
                # Carica solo i worker con informazioni valide
                if not isinstance(info, dict) or not worker_id:
                    continue
                
                worker_name = info.get("worker_name", worker_id)
                
                # Rimuovi duplicati: se abbiamo già visto questo nome, mantieni solo quello con heartbeat più recente
                if worker_name in seen_names:
                    existing_id, existing_info = seen_names[worker_name]
                    existing_hb = existing_info.get("last_heartbeat")
                    current_hb = info.get("last_heartbeat")
                    
                    # Mantieni quello con heartbeat più recente
                    if current_hb and existing_hb:
                        try:
                            existing_dt = datetime.fromisoformat(existing_hb)
                            current_dt = datetime.fromisoformat(current_hb)
                            if current_dt > existing_dt:
                                # Il nuovo è più recente, rimuovi il vecchio
                                state.active_workers.pop(existing_id, None)
                                seen_names[worker_name] = (worker_id, info)
                                skipped_duplicates += 1
                                logger.debug(f"   🔄 Sostituito worker '{worker_name}': vecchio ID {existing_id[:16]}... con nuovo {worker_id[:16]}... (heartbeat più recente)")
                            else:
                                # Il vecchio è più recente, salta il nuovo
                                skipped_duplicates += 1
                                logger.debug(f"   ⏭️ Saltato worker '{worker_name}' con ID {worker_id[:16]}... (esiste già versione più recente)")
                                continue
                        except:
                            # Se non riesco a parsare, mantieni quello esistente
                            skipped_duplicates += 1
                            continue
                    elif current_hb and not existing_hb:
                        # Il nuovo ha heartbeat, il vecchio no: sostituisci
                        state.active_workers.pop(existing_id, None)
                        seen_names[worker_name] = (worker_id, info)
                        skipped_duplicates += 1
                    else:
                        # Nessuno ha heartbeat o il vecchio ha heartbeat: mantieni il vecchio
                        skipped_duplicates += 1
                        continue
                else:
                    seen_names[worker_name] = (worker_id, info)
                
                last_hb = info.get("last_heartbeat")
                # Se il worker non ha heartbeat recente, imposta uno vecchio per mostrarlo come inattivo
                if not last_hb:
                    from datetime import timedelta
                    old_time = (datetime.now() - timedelta(hours=1)).isoformat()
                    last_hb = old_time
                    logger.debug(f"   ⚠️ Worker {worker_id[:16]}... senza heartbeat, impostato vecchio: {old_time}")
                
                state.active_workers[worker_id] = {
                    "worker_id": worker_id,
                    "worker_name": worker_name,
                    "last_heartbeat": last_hb,
                    "schedulable": info.get("schedulable", True),
                    "drain": info.get("drain", False),
                    "worker_group": info.get("worker_group"),
                }
                loaded_count += 1
            
            logger.info(f"✅ Caricati {loaded_count} worker unici da workers.json")
            if skipped_duplicates > 0:
                logger.info(f"   🗑️ Rimossi {skipped_duplicates} worker duplicati")
            if loaded_count > 0:
                worker_names = [info.get("worker_name", wid[:16]) for wid, info in state.active_workers.items()]
                logger.info(f"   👷 Worker caricati: {', '.join(worker_names[:10])}" + (f" ... e altri {len(worker_names)-10}" if len(worker_names) > 10 else ""))
    except Exception as e:
        logger.warning(f"⚠️ Impossibile caricare workers.json: {e}")
        import traceback
        logger.debug(f"   Traceback: {traceback.format_exc()}")


def save_workers():
    """Salva i worker registrati su disco (best effort, non bloccante)."""
    with state.registration_lock:
        if state.REGISTRATION_DISABLED:
            if WORKERS_FILE.exists():
                try:
                    WORKERS_FILE.unlink()
                    logger.debug("🗑️ workers.json eliminato (registrazione bloccata)")
                except Exception:
                    pass
            return
    
    try:
        with state.worker_lock:
            data = {}
            for worker_id, info in state.active_workers.items():
                data[worker_id] = {
                    "worker_id": info.get("worker_id", worker_id),
                    "worker_name": info.get("worker_name", worker_id),
                    "last_heartbeat": info.get("last_heartbeat"),
                    "schedulable": info.get("schedulable", True),
                    "drain": info.get("drain", False),
                    "worker_group": info.get("worker_group"),
                }
        
        if not data:
            if WORKERS_FILE.exists():
                try:
                    WORKERS_FILE.unlink()
                    logger.debug("🗑️ workers.json eliminato (nessun worker)")
                except Exception:
                    pass
            return
        
        with open(WORKERS_FILE, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
    except Exception as e:
        logger.warning(f"Impossibile salvare workers.json: {e}")


def load_quarantined_workers() -> Set[str]:
    """Carica lista worker in quarantena da disco."""
    if not QUARANTINED_WORKERS_FILE.exists():
        return set()
    try:
        with open(QUARANTINED_WORKERS_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
            return set(data.get("quarantined_ids", []))
    except Exception as e:
        logger.warning(f"⚠️ Errore caricamento quarantined_workers.json: {e}")
        return set()


def save_quarantined_workers() -> None:
    """Salva lista worker in quarantena su disco."""
    try:
        with state.quarantined_workers_lock:
            quarantined_copy = set(state.quarantined_workers)
        data = {
            "quarantined_ids": list(quarantined_copy),
            "updated_at": datetime.now(timezone.utc).isoformat() + "Z",
        }
        with open(QUARANTINED_WORKERS_FILE, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
    except Exception as e:
        logger.warning(f"⚠️ Errore salvataggio quarantined_workers.json: {e}")


def is_worker_quarantined(worker_id: str) -> bool:
    """Ritorna True se il worker è in quarantena (non riceve nuovi job)."""
    with state.quarantined_workers_lock:
        return worker_id in state.quarantined_workers


def load_revoked_workers() -> Set[str]:
    """Carica lista worker revocati da disco."""
    if not REVOKED_WORKERS_FILE.exists():
        return set()
    try:
        with open(REVOKED_WORKERS_FILE, 'r', encoding='utf-8') as f:
            data = json.load(f)
            revoked_set = set(data.get("revoked_ids", []))
            logger.info(f"📋 Caricati {len(revoked_set)} worker revocati da {REVOKED_WORKERS_FILE}")
            return revoked_set
    except Exception as e:
        logger.warning(f"⚠️ Errore caricamento revoked_workers.json: {e}")
        return set()


def save_revoked_workers():
    """Salva lista worker revocati su disco."""
    try:
        with state.revoked_workers_lock:
            revoked_copy = set(state.revoked_workers)
        data = {
            "revoked_ids": list(revoked_copy),
            "updated_at": datetime.now(timezone.utc).isoformat() + "Z"
        }
        with open(REVOKED_WORKERS_FILE, 'w', encoding='utf-8') as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
    except Exception as e:
        logger.warning(f"⚠️ Errore salvataggio revoked_workers.json: {e}")


def is_worker_revoked(worker_id: str) -> bool:
    """Verifica se un worker è revocato."""
    with state.revoked_workers_lock:
        return worker_id in state.revoked_workers


def queue_worker_command(worker_id: str, command: Any) -> None:
    """
    Accoda un comando per il worker specificato.
    Supporta sia stringhe (legacy) che dizionari (nuovi comandi con args).
    """
    if worker_id not in state.worker_commands:
        state.worker_commands[worker_id] = []
    state.worker_commands[worker_id].append(command)


def load_pending_updates() -> Dict[str, Dict[str, Any]]:
    """Carica pending_updates da disco."""
    if not PENDING_UPDATES_FILE.exists():
        return {}
    try:
        with open(PENDING_UPDATES_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception as e:
        logger.warning(f"Impossibile caricare pending_updates.json: {e}")
        return {}


def save_pending_updates():
    """Salva pending_updates su disco (best effort, non bloccante)."""
    try:
        with state.worker_lock:
            updates_copy = dict(state.pending_updates)
        with open(PENDING_UPDATES_FILE, "w", encoding="utf-8") as f:
            json.dump(updates_copy, f, indent=2, ensure_ascii=False)
    except Exception as e:
        logger.warning(f"Impossibile salvare pending_updates.json: {e}")


def get_version_number() -> str:
    """Legge il numero di versione dal file VERSION.txt."""
    try:
        candidates = [
            SCRIPT_DIR / "VERSION.txt",
            SCRIPT_DIR / "config" / "version" / "VERSION.txt",
        ]
        for version_file in candidates:
            if version_file.exists():
                content = version_file.read_text(encoding="utf-8", errors="ignore").strip()
                if content:
                    return content.splitlines()[0].strip()
    except Exception as e:
        logger.debug(f"Errore lettura VERSION.txt: {e}")
    return "1.0.0"


def compute_code_version() -> Optional[str]:
    """
    Calcola versione hash basata sull'artefatto ZIP (artifact-based) con caching.
    """
    try:
        zip_path = SCRIPT_DIR / "worker_downloads" / "worker_code.zip"
        
        if zip_path.exists():
            try:
                # Verifica mtime per caching
                zip_mtime = zip_path.stat().st_mtime
                if state.cached_code_version and state.cached_zip_mtime == zip_mtime:
                    return state.cached_code_version

                # Se mtime è cambiato o cache vuota, ricalcola
                hasher = hashlib.sha256()
                with open(zip_path, 'rb') as f:
                    for chunk in iter(lambda: f.read(4096), b''):
                        hasher.update(chunk)
                artifact_sha256 = hasher.hexdigest()
                
                version_number = get_version_number()
                hash_short = artifact_sha256[:16]
                version_str = f"{version_number}|{hash_short}"
                
                # Aggiorna cache
                state.cached_code_version = version_str
                state.cached_zip_mtime = zip_mtime
                
                logger.debug(f"📦 Versione ricalcolata e cache aggiornata: {version_str}")
                return version_str
            except Exception as e:
                logger.warning(f"⚠️ Impossibile calcolare hash artefatto: {e}, fallback a directory")
        
        # FALLBACK (senza caching per ora perché raro e costoso da monitorare correttamente qui)
        logger.debug("📦 Fallback: calcolo hash da directory (zip non disponibile)")
        code_dir = SCRIPT_DIR
        if not code_dir.exists():
            return None

        version_number = get_version_number()

        exclude_dirs = {
            "__pycache__", ".git", "node_modules", "worker_downloads", "logs", "temp", "tmp",
        }
        exclude_files = {
            "job_master_server.py", "main.py", "gradio_ui.py", "VERSION.txt",
        }

        hasher = hashlib.sha256()
        hasher.update(version_number.encode())
        
        for file_path in sorted(code_dir.rglob("*.py")):
            if not file_path.is_file():
                continue

            parts = file_path.parts
            if any(excluded in parts for excluded in exclude_dirs):
                continue
            if file_path.name in exclude_files:
                continue
            if "test" in file_path.name.lower():
                continue

            try:
                with open(file_path, "rb") as f:
                    content = f.read()
                    hasher.update(content)
                    hasher.update(file_path.name.encode())
            except Exception:
                continue

        digest = hasher.hexdigest()
        hash_short = digest[:16] if digest else None
        if hash_short:
            return f"{version_number}|{hash_short}"
        return None
    except Exception as e:
        logger.error(f"Errore nel calcolo code_version: {e}")
        return None


def cleanup_old_workers_on_startup():
    """Rimuove tutti i worker vecchi all'avvio (ora usiamo API v2.0), preservando il worker locale."""
    try:
        with state.worker_lock:
            old_workers_count = len(state.active_workers)
            if old_workers_count > 0:
                logger.info(f"🧹 Pulizia worker vecchi all'avvio: {old_workers_count} worker trovati")
                
                local_worker_id = None
                local_worker_data = None
                hostname = socket.gethostname()
                
                for worker_id, worker_info in state.active_workers.items():
                    worker_name = worker_info.get("worker_name", "")
                    if "Local-Worker" in worker_name or hostname in worker_name:
                        local_worker_id = worker_id
                        local_worker_data = worker_info
                        break
                
                state.active_workers.clear()
                
                if local_worker_id and local_worker_data:
                    state.active_workers[local_worker_id] = local_worker_data
                
                save_workers()
                
                if local_worker_id:
                    local_commands = state.worker_commands.pop(local_worker_id, None)
                    local_update = state.pending_updates.pop(local_worker_id, None)
                    state.worker_commands.clear()
                    state.pending_updates.clear()
                    if local_commands:
                        state.worker_commands[local_worker_id] = local_commands
                    if local_update:
                        state.pending_updates[local_worker_id] = local_update
                else:
                    state.worker_commands.clear()
                    state.pending_updates.clear()
                
                logger.info(f"✅ Comandi e aggiornamenti pendenti puliti")
    except Exception as e:
        logger.warning(f"⚠️ Errore durante pulizia worker vecchi: {e}")

# Importante: _get_allowed_worker_ips era nel file principale, dobbiamo capire se spostarlo qui o in config
# Per ora lo lascio qui, o in config. Meglio qui se usato da is_worker_schedulable

def get_allowed_worker_ips() -> Set[str]:
    # ... implementation mirrors _get_allowed_worker_ips ...
    # Poiché accede a config del job_master_server, potremmo doverlo adattare
    # O semplicemente leggere ENV
    import os
    _allowed_env = os.environ.get("VELOX_ALLOWED_WORKERS", "")
    _allowed_items = [s.strip() for s in _allowed_env.split(",") if s.strip()]
    if _allowed_items:
        return set(_allowed_items)
    return set()

def is_worker_schedulable(worker_id: str) -> bool:
    """Ritorna True se il worker è schedulabile."""
    with state.worker_lock:
        info = state.active_workers.get(worker_id)
        allowlist = get_allowed_worker_ips()

        if not info:
            return False if allowlist else True
        if allowlist:
            candidate_ids = {
                str(info.get("worker_name") or ""),
                str(info.get("name") or ""),
                str(info.get("ip") or ""),
                str(info.get("worker_ip") or ""),
                str(info.get("public_ip") or ""),
                str(info.get("host") or ""),
            }
            if not any(c in allowlist for c in candidate_ids):
                return False
        return bool(info.get("schedulable", True))

def auto_drain_low_disk_workers(threshold_gb: float = 5.0, check_interval: int = 300) -> None:
    """Thread di background che monitora lo spazio disco dei worker."""
    pass

def auto_repair_workers_monitor() -> None:
    """Health monitor attivo che accoda comandi repair_worker."""
    pass

    pass


def worker_log_append(worker_id: str, entries: List[Dict[str, Any]]) -> None:
    try:
        append_worker_session_log(worker_id, entries, SCRIPT_DIR)
    except Exception:
        pass
