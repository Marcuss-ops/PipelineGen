
import json
import logging
from typing import Dict, Any, List
from . import config
from . import state

logger = logging.getLogger(__name__)

def load_ansible_computers() -> Dict[str, Dict[str, Any]]:
    """Carica l'inventario delle macchine gestite in stile Ansible da disco."""
    if not config.ANSIBLE_COMPUTERS_FILE.exists():
        return {}
    try:
        with open(config.ANSIBLE_COMPUTERS_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
        # Supporta sia formato dict sia lista
        if isinstance(data, dict):
            # Sanifica eventuali entry corrotte
            cleaned: Dict[str, Dict[str, Any]] = {}
            for cid, meta in data.items():
                if not isinstance(meta, dict):
                    continue
                cid_str = str(cid)
                if "ID\tHost\tUser\tEnabled" in cid_str or "\tAzioni" in cid_str:
                    logger.warning(f"⚠️ Scarto entry ansible_computers corrotta (sembra una tabella copiata) id='{cid_str[:80]}'")
                    continue
                cleaned[cid] = meta
            return cleaned
        if isinstance(data, list):
            result: Dict[str, Dict[str, Any]] = {}
            for item in data:
                if not isinstance(item, dict):
                    continue
                cid = item.get("id") or item.get("computer_id") or item.get("name") or item.get("host")
                if not cid:
                    continue
                item["id"] = cid
                result[cid] = item
            return result
    except Exception as e:
        logger.warning(f"⚠️ Impossibile caricare ansible_computers.json: {e}")
    return {}


def save_ansible_computers():
    """Salva l'inventario delle macchine 'Ansible Computer' su disco (best effort)."""
    try:
        with state.ansible_computers_lock:
            raw_data = dict(state.ansible_computers)

        # Rimuovi entry chiaramente corrotte prima di salvare
        data: Dict[str, Dict[str, Any]] = {}
        for cid, meta in raw_data.items():
            cid_str = str(cid)
            if "ID\tHost\tUser\tEnabled" in cid_str or "\tAzioni" in cid_str:
                logger.warning(f"⚠️ Non salvo entry ansible_computers corrotta id='{cid_str[:80]}'")
                continue
            if isinstance(meta, dict):
                data[cid] = meta
        with open(config.ANSIBLE_COMPUTERS_FILE, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
    except Exception as e:
        logger.warning(f"⚠️ Impossibile salvare ansible_computers.json: {e}")


def load_ansible_command_history() -> List[Dict[str, Any]]:
    """Carica lo storico comandi Ansible da disco."""
    if not config.ANSIBLE_COMMAND_HISTORY_FILE.exists():
        return []
    try:
        with open(config.ANSIBLE_COMMAND_HISTORY_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
        if isinstance(data, list):
            # Mantieni solo ultimi 50 comandi
            return data[-50:]
        return []
    except Exception as e:
        logger.warning(f"⚠️ Impossibile caricare ansible_command_history.json: {e}")
    return []


def save_ansible_command_history(history: List[Dict[str, Any]]) -> None:
    """Salva lo storico comandi Ansible su disco (mantiene max 50)."""
    try:
        # Mantieni solo ultimi 50
        history = history[-50:]
        with open(config.ANSIBLE_COMMAND_HISTORY_FILE, "w", encoding="utf-8") as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
    except Exception as e:
        logger.warning(f"⚠️ Impossibile salvare ansible_command_history.json: {e}")


def load_ansible_runs() -> Dict[str, Dict[str, Any]]:
    """Carica le esecuzioni Ansible (batch) da disco."""
    if not config.ANSIBLE_RUNS_FILE.exists():
        return {}
    try:
        with open(config.ANSIBLE_RUNS_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
        if isinstance(data, dict):
            return data
    except Exception as e:
        logger.warning(f"⚠️ Impossibile caricare ansible_runs.json: {e}")
    return {}


def save_ansible_runs():
    """Salva le esecuzioni Ansible su disco (best effort)."""
    try:
        with state.ansible_runs_lock:
            data = dict(state.ansible_runs)
        with open(config.ANSIBLE_RUNS_FILE, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
    except Exception as e:
        logger.warning(f"⚠️ Impossibile salvare ansible_runs.json: {e}")
