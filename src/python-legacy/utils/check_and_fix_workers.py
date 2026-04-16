#!/usr/bin/env python3
"""
Script per verificare lo stato del master e aggiornare worker problematici.
"""

import json
import requests
import sys
from pathlib import Path

def check_master_flags(master_url: str = "http://localhost:8000"):
    """Verifica i flag NEW_JOBS_PAUSED e SCHEDULING_PAUSED."""
    try:
        # Carica da master_config.json locale
        config_file = Path(__file__).parent / "master_config.json"
        if config_file.exists():
            with open(config_file, 'r') as f:
                config = json.load(f)
                new_jobs_paused = config.get("new_jobs_paused", False)
                scheduling_paused = config.get("scheduling_paused", False)
                new_jobs_reason = config.get("new_jobs_paused_reason")
                scheduling_reason = config.get("scheduling_paused_reason")
                
                print("="*60)
                print("🔍 STATO MASTER CONFIG")
                print("="*60)
                print(f"NEW_JOBS_PAUSED: {'✅ ATTIVO' if new_jobs_paused else '❌ DISATTIVATO'}")
                if new_jobs_paused and new_jobs_reason:
                    print(f"   Motivo: {new_jobs_reason}")
                print(f"SCHEDULING_PAUSED: {'✅ ATTIVO' if scheduling_paused else '❌ DISATTIVATO'}")
                if scheduling_paused and scheduling_reason:
                    print(f"   Motivo: {scheduling_reason}")
                
                if new_jobs_paused or scheduling_paused:
                    print("\n⚠️ ATTENZIONE: I job potrebbero non essere assegnati ai worker!")
                    return True
                else:
                    print("\n✅ I flag sono disattivati - i job possono essere assegnati normalmente")
                    return False
        else:
            print("⚠️ master_config.json non trovato - usando valori di default")
            return False
    except Exception as e:
        print(f"❌ Errore nel verificare i flag: {e}")
        return False

def check_master_status(master_url: str = "http://localhost:8000"):
    """Verifica che il master sia raggiungibile."""
    try:
        response = requests.get(f"{master_url}/stats", timeout=5)
        if response.status_code == 200:
            data = response.json()
            print("\n" + "="*60)
            print("📊 STATO MASTER SERVER")
            print("="*60)
            print(f"✅ Master raggiungibile: {master_url}")
            print(f"📊 Worker attivi: {data.get('active_workers', 'N/A')}")
            print(f"📋 Job in coda: {data.get('pending_jobs', 'N/A')}")
            print(f"⚙️ Job in elaborazione: {data.get('processing_jobs', 'N/A')}")
            return True
        else:
            print(f"❌ Master risponde ma con status code {response.status_code}")
            return False
    except requests.exceptions.ConnectionError:
        print(f"❌ Impossibile connettersi al master: {master_url}")
        print("   Verifica che il master sia in esecuzione")
        return False
    except Exception as e:
        print(f"❌ Errore nel verificare il master: {e}")
        return False

def update_workers(master_url: str, worker_hosts: list = None):
    """Aggiorna i worker problematici usando la route /ansible/computers/run_action."""
    if worker_hosts is None:
        worker_hosts = []
    
    print("\n" + "="*60)
    print("🔄 AGGIORNAMENTO WORKER")
    print("="*60)
    
    if not worker_hosts:
        print("⚠️ Nessun worker specificato. Usa --hosts per specificare gli host da aggiornare")
        print("   Esempio: python3 check_and_fix_workers.py --update --hosts 51.68.225.209,57.128.245.200")
        return
    
    body = {
        "action": "update",
        "computer_ids": worker_hosts
    }
    
    try:
        response = requests.post(
            f"{master_url}/ansible/computers/run_action",
            json=body,
            timeout=10
        )
        
        if response.status_code == 200:
            result = response.json()
            print(f"✅ Comando update inviato ai worker:")
            for host in worker_hosts:
                print(f"   - {host}")
            print(f"\n📋 Risultato: {result.get('status', 'N/A')}")
            if 'run_id' in result:
                print(f"🆔 Run ID: {result['run_id']}")
                print(f"💡 Monitora lo stato con: curl {master_url}/ansible/runs/{result['run_id']}")
        else:
            print(f"❌ Errore nell'inviare comando update: {response.status_code}")
            print(f"   Risposta: {response.text}")
    except Exception as e:
        print(f"❌ Errore nella richiesta: {e}")

def main():
    master_url = "http://localhost:8000"
    if len(sys.argv) > 1 and sys.argv[1].startswith("http"):
        master_url = sys.argv[1]
        sys.argv = [sys.argv[0]] + sys.argv[2:]
    
    # Parse arguments
    update_mode = "--update" in sys.argv
    hosts_arg_idx = None
    if "--hosts" in sys.argv:
        hosts_arg_idx = sys.argv.index("--hosts") + 1
    
    worker_hosts = []
    if hosts_arg_idx and hosts_arg_idx < len(sys.argv):
        hosts_str = sys.argv[hosts_arg_idx]
        worker_hosts = [h.strip() for h in hosts_str.split(",") if h.strip()]
    
    # Verifica flag
    flags_active = check_master_flags()
    
    # Verifica stato master
    master_ok = check_master_status(master_url)
    
    if not master_ok:
        print("\n❌ Il master non è raggiungibile. Risolvi questo problema prima di procedere.")
        return
    
    if flags_active:
        print("\n⚠️ I flag di pausa sono attivi. I job potrebbero non essere assegnati.")
        print("   Per disattivarli, modifica master_config.json:")
        print("   {")
        print('     "new_jobs_paused": false,')
        print('     "scheduling_paused": false')
        print("   }")
    
    # Se richiesto, aggiorna i worker
    if update_mode:
        if not worker_hosts:
            print("\n⚠️ Specifica gli host da aggiornare con --hosts")
            print("   Esempio: python3 check_and_fix_workers.py --update --hosts 51.68.225.209")
        else:
            update_workers(master_url, worker_hosts)
    else:
        print("\n💡 Per aggiornare worker problematici, esegui:")
        print(f"   python3 {sys.argv[0]} --update --hosts <host1>,<host2>,...")
        print("   Esempio: python3 check_and_fix_workers.py --update --hosts 51.68.225.209,57.128.245.200")

if __name__ == "__main__":
    main()

