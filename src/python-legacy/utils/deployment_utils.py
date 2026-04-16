#!/usr/bin/env python3
"""
Deployment Utilities
Funzioni per gestire releases/, symlink atomici, rollback, healthcheck
"""

import os
import shutil
import subprocess
import hashlib
import json
import time
import logging
from pathlib import Path
from typing import Optional, Tuple, Dict, Any
from datetime import datetime

logger = logging.getLogger(__name__)

def compute_file_sha256(file_path: Path) -> str:
    """Calcola SHA256 di un file."""
    hasher = hashlib.sha256()
    with open(file_path, 'rb') as f:
        for chunk in iter(lambda: f.read(4096), b''):
            hasher.update(chunk)
    return hasher.hexdigest()

def atomic_symlink_update(new_release_dir: Path, base_dir: Path) -> bool:
    """
    Switch atomico di symlink: previous -> vecchio current, current -> nuova release.
    
    Returns:
        True se successo, False altrimenti
    """
    try:
        base_dir = Path(base_dir)
        current = base_dir / "current"
        previous = base_dir / "previous"
        
        # Ottieni il target attuale di current (se esiste)
        old_target = None
        if current.exists() and current.is_symlink():
            try:
                old_target = current.resolve()
            except Exception as e:
                logger.warning(f"⚠️ Impossibile risolvere current symlink: {e}")
        
        # Aggiorna previous -> vecchio current (se esiste)
        if old_target and old_target.exists():
            tmp_prev = base_dir / ".previous.tmp"
            try:
                if tmp_prev.exists() or tmp_prev.is_symlink():
                    tmp_prev.unlink()
                tmp_prev.symlink_to(old_target)
                tmp_prev.replace(previous)
                logger.info(f"✅ previous -> {old_target}")
            except Exception as e:
                logger.warning(f"⚠️ Impossibile aggiornare previous: {e}")
        
        # Aggiorna current -> nuova release
        tmp_cur = base_dir / ".current.tmp"
        try:
            if tmp_cur.exists() or tmp_cur.is_symlink():
                tmp_cur.unlink()
            tmp_cur.symlink_to(new_release_dir)
            tmp_cur.replace(current)
            logger.info(f"✅ current -> {new_release_dir}")
            return True
        except Exception as e:
            logger.error(f"❌ Impossibile aggiornare current: {e}")
            return False
            
    except Exception as e:
        logger.error(f"❌ Errore durante switch atomico: {e}", exc_info=True)
        return False

def rollback_to_previous(base_dir: Path) -> bool:
    """
    Rollback: current -> previous (se previous esiste).
    
    Returns:
        True se rollback riuscito, False altrimenti
    """
    try:
        base_dir = Path(base_dir)
        current = base_dir / "current"
        previous = base_dir / "previous"
        
        if not previous.exists() or not previous.is_symlink():
            logger.warning("⚠️ previous symlink non esiste, impossibile rollback")
            return False
        
        prev_target = previous.resolve()
        if not prev_target.exists():
            logger.warning(f"⚠️ previous target non esiste: {prev_target}")
            return False
        
        # Switch atomico: current -> previous target
        tmp_cur = base_dir / ".current.tmp"
        if tmp_cur.exists() or tmp_cur.is_symlink():
            tmp_cur.unlink()
        tmp_cur.symlink_to(prev_target)
        tmp_cur.replace(current)
        
        logger.info(f"✅ Rollback completato: current -> {prev_target}")
        return True
        
    except Exception as e:
        logger.error(f"❌ Errore durante rollback: {e}", exc_info=True)
        return False

def healthcheck_worker(health_url: str = "http://127.0.0.1:8010/health", timeout: int = 5) -> bool:
    """
    Healthcheck del worker (opzione A: endpoint locale).
    
    Returns:
        True se worker risponde OK, False altrimenti
    """
    try:
        import requests
        response = requests.get(health_url, timeout=timeout)
        if response.status_code == 200:
            data = response.json()
            return data.get("status") == "ok"
        return False
    except Exception as e:
        logger.debug(f"Healthcheck fallito: {e}")
        return False

def healthcheck_via_master(master_url: str, worker_id: str, timeout: int = 10) -> bool:
    """
    Healthcheck del worker (opzione B: via master heartbeat).
    
    Returns:
        True se worker è online secondo master, False altrimenti
    """
    try:
        import requests
        response = requests.get(
            f"{master_url}/workers_status",
            timeout=timeout
        )
        if response.status_code == 200:
            data = response.json()
            workers = data.get("workers", [])
            for w in workers:
                if w.get("worker_id") == worker_id:
                    return w.get("active", False)
        return False
    except Exception as e:
        logger.debug(f"Healthcheck via master fallito: {e}")
        return False

def create_release_timestamp() -> str:
    """Crea timestamp per nome release directory."""
    return datetime.utcnow().strftime("%Y-%m-%d_%H%M")

def get_release_dir(base_dir: Path, timestamp: Optional[str] = None) -> Path:
    """Ottiene path alla directory release."""
    base_dir = Path(base_dir)
    releases_dir = base_dir / "releases"
    if timestamp:
        return releases_dir / timestamp
    return releases_dir / create_release_timestamp()

def read_marker(marker_path: Path) -> Optional[Dict[str, Any]]:
    """Legge .velox_installed.json marker."""
    try:
        if marker_path.exists():
            with open(marker_path, 'r', encoding='utf-8') as f:
                return json.load(f)
    except Exception as e:
        logger.warning(f"⚠️ Errore lettura marker {marker_path}: {e}")
    return None

def write_marker(marker_path: Path, data: Dict[str, Any]) -> bool:
    """Scrive .velox_installed.json marker."""
    try:
        marker_path.parent.mkdir(parents=True, exist_ok=True)
        with open(marker_path, 'w', encoding='utf-8') as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
        return True
    except Exception as e:
        logger.error(f"❌ Errore scrittura marker {marker_path}: {e}")
        return False

def install_locked_deps(requirements_lock_path: Path, python_cmd: str = "python3") -> bool:
    """
    Installa dipendenze da requirements.lock con --require-hashes.
    
    Returns:
        True se installazione riuscita, False altrimenti
    """
    try:
        if not requirements_lock_path.exists():
            logger.warning(f"⚠️ requirements.lock non trovato: {requirements_lock_path}")
            return False

        # If the lockfile doesn't contain hashes, fall back to a normal pip install.
        lock_has_hashes = False
        try:
            content = requirements_lock_path.read_text(encoding="utf-8", errors="ignore")
            lock_has_hashes = ("--hash=" in content) or ("sha256:" in content)
        except Exception:
            lock_has_hashes = False
        
        if lock_has_hashes:
            logger.info("📦 Installazione dipendenze da requirements.lock (con hash verification)...")
        else:
            logger.info("📦 Installazione dipendenze da requirements.lock (senza hash)...")
        result = subprocess.run(
            [python_cmd, "-m", "pip", "install", "--upgrade", "pip"],
            capture_output=True,
            text=True,
            timeout=60
        )
        
        result = subprocess.run(
            (
                [python_cmd, "-m", "pip", "install", "--require-hashes", "-r", str(requirements_lock_path)]
                if lock_has_hashes
                else [python_cmd, "-m", "pip", "install", "-r", str(requirements_lock_path)]
            ),
            capture_output=True,
            text=True,
            timeout=600  # 10 minuti
        )

        if result.returncode != 0 and lock_has_hashes:
            logger.warning("⚠️ Installazione con hash fallita, ritento senza --require-hashes...")
            result = subprocess.run(
                [python_cmd, "-m", "pip", "install", "-r", str(requirements_lock_path)],
                capture_output=True,
                text=True,
                timeout=600,
            )
        
        if result.returncode == 0:
            logger.info("✅ Dipendenze installate con hash verification")
            return True
        else:
            logger.error(f"❌ Errore installazione dipendenze: {result.stderr[:500]}")
            return False
            
    except Exception as e:
        logger.error(f"❌ Errore durante installazione dipendenze: {e}")
        return False

def restart_service(service_name: str) -> bool:
    """
    Riavvia servizio systemd.
    
    Returns:
        True se riavvio riuscito, False altrimenti
    """
    try:
        logger.info(f"🔄 Riavvio servizio: {service_name}")
        result = subprocess.run(
            ["sudo", "systemctl", "restart", service_name],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        if result.returncode == 0:
            logger.info(f"✅ Servizio {service_name} riavviato")
            # Attendi un momento per permettere al servizio di avviarsi
            time.sleep(2)
            return True
        else:
            logger.error(f"❌ Errore riavvio servizio: {result.stderr}")
            return False
            
    except Exception as e:
        logger.error(f"❌ Errore durante riavvio servizio: {e}")
        return False

def cleanup_old_releases(base_dir: Path, keep_last_n: int = 5) -> None:
    """
    Rimuove release vecchie, mantenendo solo le ultime N.
    """
    try:
        releases_dir = base_dir / "releases"
        if not releases_dir.exists():
            return
        
        # Lista tutte le release ordinate per timestamp (più recenti prima)
        releases = sorted(
            [d for d in releases_dir.iterdir() if d.is_dir()],
            key=lambda x: x.name,
            reverse=True
        )
        
        # Rimuovi quelle oltre keep_last_n
        to_remove = releases[keep_last_n:]
        for release_dir in to_remove:
            try:
                logger.info(f"🗑️ Rimozione release vecchia: {release_dir.name}")
                shutil.rmtree(release_dir)
            except Exception as e:
                logger.warning(f"⚠️ Impossibile rimuovere {release_dir}: {e}")
                
    except Exception as e:
        logger.warning(f"⚠️ Errore durante cleanup release: {e}")
