#!/usr/bin/env python3
"""
Script per verificare e aggiornare la connessione al master server.
"""

import os
import sys
import subprocess
import requests
import socket
from pathlib import Path

def get_systemd_services():
    """Trova tutti i servizi velox-worker."""
    try:
        result = subprocess.run(
            ["systemctl", "list-units", "velox-worker*.service", "--no-legend", "--no-pager"],
            capture_output=True,
            text=True,
            timeout=5
        )
        services = []
        for line in result.stdout.strip().split('\n'):
            if line.strip():
                service_name = line.split()[0]
                if service_name.endswith('.service'):
                    services.append(service_name)
        return services
    except Exception as e:
        print(f"⚠️ Errore nel trovare servizi: {e}")
        return []

def get_master_url_from_service(service_name):
    """Estrae il master_url dal servizio systemd."""
    try:
        result = subprocess.run(
            ["systemctl", "show", service_name, "--property=ExecStart"],
            capture_output=True,
            text=True,
            timeout=5
        )
        exec_start = result.stdout.strip()
        # Cerca --master-url http://...
        if "--master-url" in exec_start:
            parts = exec_start.split("--master-url")
            if len(parts) > 1:
                url_part = parts[1].strip().split()[0]
                return url_part
        return None
    except Exception as e:
        print(f"⚠️ Errore nell'estrarre master_url: {e}")
        return None

def test_master_connection(master_url):
    """Testa la connessione al master."""
    try:
        response = requests.get(f"{master_url}/stats", timeout=5)
        if response.status_code == 200:
            return True, "✅ Connessione OK"
        else:
            return False, f"❌ Master risponde ma con status code {response.status_code}"
    except requests.exceptions.ConnectionRefused:
        return False, "❌ Connessione rifiutata - il master potrebbe non essere in esecuzione"
    except requests.exceptions.Timeout:
        return False, "❌ Timeout - il master potrebbe non essere raggiungibile"
    except requests.exceptions.RequestException as e:
        return False, f"❌ Errore: {e}"
    except Exception as e:
        return False, f"❌ Errore sconosciuto: {e}"

def update_service_master_url(service_name, new_master_url):
    """Aggiorna il master_url nel servizio systemd."""
    try:
        # Leggi il file del servizio
        service_file = Path(f"/etc/systemd/system/{service_name}")
        if not service_file.exists():
            print(f"❌ File servizio non trovato: {service_file}")
            return False
        
        content = service_file.read_text()
        
        # Sostituisci il master_url
        import re
        # Pattern per trovare --master-url http://...
        pattern = r'--master-url\s+\S+'
        replacement = f'--master-url {new_master_url}'
        
        new_content = re.sub(pattern, replacement, content)
        
        if new_content == content:
            print(f"⚠️ Nessun master_url trovato da sostituire nel servizio")
            return False
        
        # Scrivi il file aggiornato
        service_file.write_text(new_content)
        
        # Ricarica systemd
        subprocess.run(["systemctl", "daemon-reload"], check=True, timeout=10)
        
        print(f"✅ Servizio {service_name} aggiornato con master_url: {new_master_url}")
        return True
    except Exception as e:
        print(f"❌ Errore nell'aggiornare servizio: {e}")
        return False

def main():
    print("="*60)
    print("🔍 VERIFICA CONNESSIONE MASTER")
    print("="*60)
    
    # Trova servizi
    services = get_systemd_services()
    if not services:
        print("❌ Nessun servizio velox-worker trovato")
        return
    
    print(f"\n📋 Servizi trovati: {len(services)}")
    for i, service in enumerate(services, 1):
        print(f"   {i}) {service}")
    
    # Per ogni servizio, verifica master_url
    print("\n" + "="*60)
    print("📡 STATO CONNESSIONI")
    print("="*60)
    
    for service in services:
        print(f"\n🔍 Servizio: {service}")
        master_url = get_master_url_from_service(service)
        if master_url:
            print(f"   Master URL configurato: {master_url}")
            ok, msg = test_master_connection(master_url)
            print(f"   {msg}")
        else:
            print(f"   ⚠️ Master URL non trovato nella configurazione")
    
    # Opzione per aggiornare
    if len(sys.argv) > 1:
        new_master_url = sys.argv[1]
        print(f"\n" + "="*60)
        print(f"🔄 AGGIORNAMENTO MASTER URL")
        print("="*60)
        print(f"Nuovo master URL: {new_master_url}")
        
        # Testa prima la connessione
        ok, msg = test_master_connection(new_master_url)
        print(f"Test connessione: {msg}")
        
        if not ok:
            response = input("\n⚠️ La connessione al nuovo master URL non funziona. Continuare comunque? (s/N): ")
            if response.lower() != 's':
                print("❌ Operazione annullata")
                return
        
        # Aggiorna tutti i servizi
        for service in services:
            if update_service_master_url(service, new_master_url):
                print(f"   ✅ {service} aggiornato")
                # Riavvia il servizio
                try:
                    subprocess.run(["systemctl", "restart", service], check=True, timeout=10)
                    print(f"   ✅ {service} riavviato")
                except Exception as e:
                    print(f"   ⚠️ Errore nel riavviare {service}: {e}")
            else:
                print(f"   ❌ Errore nell'aggiornare {service}")
    else:
        print(f"\n💡 Per aggiornare il master URL, esegui:")
        print(f"   python3 {sys.argv[0]} <nuovo_master_url>")
        print(f"   Esempio: python3 {sys.argv[0]} http://51.91.11.36:8000")

if __name__ == "__main__":
    main()

