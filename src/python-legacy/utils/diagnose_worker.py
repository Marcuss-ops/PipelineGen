#!/usr/bin/env python3
"""
Script di diagnostica per verificare lo stato del worker
Esegui questo script sul computer worker per capire perché non si registra
"""

import sys
import requests
import socket
from pathlib import Path

def test_master_connection(master_url: str):
    """Testa la connessione al master."""
    print(f"\n{'='*60}")
    print("🔍 DIAGNOSTICA CONNESSIONE MASTER")
    print(f"{'='*60}")
    print(f"Master URL: {master_url}")
    
    # Test 1: Verifica che l'URL sia raggiungibile
    print("\n1️⃣ Test connessione base...")
    try:
        response = requests.get(f"{master_url}/stats", timeout=5)
        if response.status_code == 200:
            print("   ✅ Master raggiungibile")
            stats = response.json()
            print(f"   📊 Worker attivi sul master: {stats.get('active_workers', 0)}")
            print(f"   📋 Job pending: {stats.get('pending', 0)}")
        else:
            print(f"   ❌ Master risponde ma con errore: {response.status_code}")
            return False
    except requests.exceptions.ConnectionError:
        print(f"   ❌ Impossibile connettersi a {master_url}")
        print(f"   💡 Verifica che:")
        print(f"      - Il master sia in esecuzione")
        print(f"      - L'URL sia corretto")
        print(f"      - Non ci siano firewall che bloccano la connessione")
        return False
    except requests.exceptions.Timeout:
        print(f"   ⏳ Timeout nella connessione (5s)")
        print(f"   💡 Il master potrebbe essere lento o sovraccarico")
        return False
    except Exception as e:
        print(f"   ❌ Errore: {e}")
        return False
    
    # Test 2: Verifica endpoint /register
    print("\n2️⃣ Test endpoint registrazione...")
    try:
        test_worker_id = "test-worker-diagnostic"
        test_worker_name = f"test-{socket.gethostname()}"
        response = requests.post(
            f"{master_url}/register",
            json={
                "worker_id": test_worker_id,
                "worker_name": test_worker_name
            },
            timeout=10
        )
        if response.status_code == 200:
            print("   ✅ Endpoint /register funzionante")
            result = response.json()
            print(f"   📝 Risposta: {result}")
        else:
            print(f"   ❌ Endpoint /register errore: {response.status_code}")
            print(f"   📝 Risposta: {response.text[:200]}")
            return False
    except Exception as e:
        print(f"   ❌ Errore nel test registrazione: {e}")
        return False
    
    # Test 3: Verifica /workers_status
    print("\n3️⃣ Test endpoint workers_status...")
    try:
        response = requests.get(f"{master_url}/workers_status", timeout=15)
        if response.status_code == 200:
            print("   ✅ Endpoint /workers_status funzionante")
            data = response.json()
            workers = data.get('workers', [])
            print(f"   👷 Worker registrati: {len(workers)}")
            for w in workers:
                print(f"      - {w.get('name')} ({w.get('status')})")
        else:
            print(f"   ⚠️ Endpoint /workers_status errore: {response.status_code}")
    except requests.exceptions.Timeout:
        print("   ⏳ Timeout (15s) - endpoint lento ma potrebbe funzionare")
    except Exception as e:
        print(f"   ⚠️ Errore: {e}")
    
    return True

def check_worker_files():
    """Verifica che i file del worker esistano."""
    print(f"\n{'='*60}")
    print("📁 VERIFICA FILE WORKER")
    print(f"{'='*60}")
    
    script_dir = Path(__file__).parent
    required_files = [
        "job_worker.py",
        "standalone_multi_video.py"
    ]
    
    all_ok = True
    for file_name in required_files:
        file_path = script_dir / file_name
        if file_path.exists():
            print(f"   ✅ {file_name}")
        else:
            print(f"   ❌ {file_name} NON TROVATO")
            all_ok = False
    
    return all_ok

def check_worker_process():
    """Verifica se il worker è in esecuzione."""
    print(f"\n{'='*60}")
    print("🔄 VERIFICA PROCESSO WORKER")
    print(f"{'='*60}")
    
    import subprocess
    try:
        # Cerca processi job_worker
        result = subprocess.run(
            ["pgrep", "-f", "job_worker.py"],
            capture_output=True,
            text=True
        )
        if result.returncode == 0:
            pids = result.stdout.strip().split('\n')
            print(f"   ✅ Worker in esecuzione (PID: {', '.join(pids)})")
            return True
        else:
            print("   ⚠️ Nessun processo worker trovato")
            return False
    except Exception as e:
        print(f"   ⚠️ Impossibile verificare processi: {e}")
        return False

def check_systemd_service():
    """Verifica lo stato del servizio systemd."""
    print(f"\n{'='*60}")
    print("⚙️ VERIFICA SERVIZIO SYSTEMD")
    print(f"{'='*60}")
    
    import subprocess
    hostname = socket.gethostname()
    service_name = f"velox-worker-{hostname.lower().replace(' ', '-')}"
    
    try:
        result = subprocess.run(
            ["systemctl", "is-active", service_name],
            capture_output=True,
            text=True
        )
        if result.returncode == 0:
            status = result.stdout.strip()
            if status == "active":
                print(f"   ✅ Servizio {service_name} è ATTIVO")
            else:
                print(f"   ⚠️ Servizio {service_name} è {status}")
        else:
            print(f"   ⚠️ Servizio {service_name} non trovato o non attivo")
            print(f"   💡 Verifica con: sudo systemctl status {service_name}")
    except Exception as e:
        print(f"   ⚠️ Impossibile verificare systemd: {e}")
        print(f"   💡 Esegui manualmente: sudo systemctl status {service_name}")

def main():
    print("="*60)
    print("🔧 DIAGNOSTICA WORKER")
    print("="*60)
    
    # Ottieni master URL da argomento o usa default
    master_url = sys.argv[1] if len(sys.argv) > 1 else "http://51.91.11.36:8001"
    
    print(f"\n📡 Master URL: {master_url}")
    print(f"🖥️ Hostname: {socket.gethostname()}")
    
    # Esegui tutti i test
    files_ok = check_worker_files()
    process_running = check_worker_process()
    check_systemd_service()
    connection_ok = test_master_connection(master_url)
    
    # Riepilogo
    print(f"\n{'='*60}")
    print("📋 RIEPILOGO")
    print(f"{'='*60}")
    print(f"File worker: {'✅ OK' if files_ok else '❌ MANCANTI'}")
    print(f"Processo worker: {'✅ IN ESECUZIONE' if process_running else '❌ NON IN ESECUZIONE'}")
    print(f"Connessione master: {'✅ OK' if connection_ok else '❌ FALLITA'}")
    
    if not connection_ok:
        print(f"\n💡 SOLUZIONI:")
        print(f"   1. Verifica che il master sia in esecuzione")
        print(f"   2. Verifica l'URL del master: {master_url}")
        print(f"   3. Verifica firewall/porte aperte")
        print(f"   4. Prova: curl {master_url}/stats")
    
    if not process_running:
        print(f"\n💡 PER AVVIARE IL WORKER:")
        print(f"   - Se systemd: sudo systemctl start velox-worker-{socket.gethostname().lower().replace(' ', '-')}")
        print(f"   - Manualmente: python3 job_worker.py --master-url {master_url}")

if __name__ == "__main__":
    main()

