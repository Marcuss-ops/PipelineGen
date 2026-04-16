#!/usr/bin/env python3
"""
Master Admin CLI - Comandi rapidi per gestire i worker remoti.

Permette di:
  - elencare lo stato dei worker
  - vedere lo stato degli aggiornamenti codice
  - inviare comandi ai worker (restart, update_code, reboot_host)
  - aggiornare tutti i worker remoti in un colpo solo
"""

import argparse
import sys
from typing import Any, Dict

import requests


def _normalize_url(url: str) -> str:
    url = url.strip()
    if url.endswith("/"):
        url = url[:-1]
    return url


def cmd_list_workers(master_url: str):
    master_url = _normalize_url(master_url)
    # Usa un timeout più alto perché il master può essere occupato
    resp = requests.get(f"{master_url}/workers_status", timeout=15)
    resp.raise_for_status()
    data = resp.json()

    workers = data.get("workers", [])
    print(f"\n{'=' * 80}")
    print(f"📋 LISTA WORKER")
    print(f"{'=' * 80}")
    print(f"💻 Worker totali: {len(workers)}\n")
    
    if not workers:
        print("⚠️ Nessun worker registrato")
        return
    
    # Raggruppa per stato
    active = [w for w in workers if w.get("status") == "active"]
    inactive = [w for w in workers if w.get("status") != "active"]
    
    if active:
        print("✅ WORKER ATTIVI:")
        print("-" * 80)
        for w in active:
            name = w.get("name") or w.get("display_name") or w.get("worker_id")
            wid = w.get("worker_id")
            active_jobs = w.get("active_jobs", 0)
            processing = w.get("processing", 0)
            time_since = w.get("time_since_heartbeat", 0)
            
            print(f"  ✅ {name} ({wid[:8]}...)")
            print(f"     Job attivi: {active_jobs} (processing: {processing})")
            if time_since < 60:
                print(f"     ⏰ Heartbeat: {int(time_since)}s fa")
            else:
                print(f"     ⏰ Heartbeat: {int(time_since/60)}m fa")
            
            if w.get("current_job"):
                cj = w["current_job"]
                print(f"     📋 Job: {cj.get('job_id', 'N/A')[:8]}... ({cj.get('status', 'N/A')})")
            print()
    
    if inactive:
        print("❌ WORKER INATTIVI:")
        print("-" * 80)
        for w in inactive:
            name = w.get("name") or w.get("display_name") or w.get("worker_id")
            wid = w.get("worker_id")
            time_since = w.get("time_since_heartbeat", 0)
            
            print(f"  ❌ {name} ({wid[:8]}...)")
            if time_since > 0:
                print(f"     ⏰ Ultimo heartbeat: {int(time_since/60)}m fa")
            else:
                print(f"     ⏰ Ultimo heartbeat: mai")
            print()
    
    print(f"{'=' * 80}\n")


def cmd_update_status(master_url: str):
    master_url = _normalize_url(master_url)
    # Usa un timeout più alto perché il calcolo versioni sul master può richiedere qualche secondo
    resp = requests.get(f"{master_url}/workers/update_status", timeout=20)
    resp.raise_for_status()
    data = resp.json()

    master_version = data.get("master_version")
    print(f"\n{'=' * 80}")
    print(f"🔄 STATO AGGIORNAMENTI CODICE")
    print(f"{'=' * 80}")
    print(f"📦 Versione master: {master_version}\n")

    workers = data.get("workers", [])
    if not workers:
        print("⚠️ Nessun worker trovato.")
        return

    # Raggruppa per stato
    updated_ok = []
    pending_ack = []
    version_mismatch = []
    no_update = []

    for w in workers:
        ack = w.get("ack", False)
        ack_ver = w.get("ack_version")
        target = w.get("target_version")
        ok = w.get("version_ok", False)
        auto = w.get("auto_update", False)
        ack_age = w.get("ack_age_seconds")
        pending_cmds = w.get("pending_commands", [])

        # Logica più semplice e robusta:
        # - se ci sono comandi pendenti -> update in attesa di essere ricevuto dal worker
        # - se c'è un target_version e nessun ACK -> update pendente
        # - se c'è ACK -> allineato o mismatch in base a version_ok
        # - se non c'è target_version e non c'è ACK -> nessun update noto
        if pending_cmds and "update_code" in pending_cmds:
            # Comando inviato ma worker non l'ha ancora ricevuto (polling ogni 15s)
            pending_ack.append(w)
        elif target and not ack:
            # Richiesto un update dal master, ma il worker non ha ancora inviato ACK
            pending_ack.append(w)
        elif ack and ack_ver:
            # C'è un ACK (auto-update o update completato)
            if ok or (ack_ver == master_version):
                updated_ok.append(w)
            else:
                version_mismatch.append(w)
        else:
            no_update.append(w)

    # Worker aggiornati e allineati
    if updated_ok:
        print("✅ WORKER AGGIORNATI E ALLINEATI:")
        print("-" * 80)
        for w in updated_ok:
            name = w.get("name") or w.get("worker_id")
            wid = w.get("worker_id")
            ack_ver = w.get("ack_version")
            patch = w.get("ack_patch")
            auto = w.get("auto_update", False)
            ack_age = w.get("ack_age_seconds")
            
            print(f"  ✅ {name} ({wid[:8]}...)")
            print(f"     Versione: {ack_ver}" + (f" (patch: {patch})" if patch else ""))
            if auto:
                print(f"     📝 Auto-update")
            if ack_age is not None:
                if ack_age < 60:
                    print(f"     ⏰ ACK ricevuto {int(ack_age)}s fa")
                else:
                    print(f"     ⏰ ACK ricevuto {int(ack_age/60)}m fa")
            print()
    
    # Worker in attesa di ACK
    if pending_ack:
        print("⏳ WORKER IN ATTESA DI ACK:")
        print("-" * 80)
        for w in pending_ack:
            name = w.get("name") or w.get("worker_id")
            wid = w.get("worker_id")
            target = w.get("target_version")
            requested_age = w.get("requested_age_seconds")
            pending_cmds = w.get("pending_commands", [])
            
            print(f"  ⏳ {name} ({wid[:8]}...)")
            if pending_cmds:
                print(f"     ⚠️ Comandi pendenti: {', '.join(pending_cmds)} (worker non li ha ancora ricevuti - polling ogni 15s)")
            effective_target = w.get("effective_target_version") or target or master_version
            if target:
                print(f"     Target: {target}")
            print(f"     Master: {master_version}")
            print(f"     Effective target: {effective_target}")
            print(f"     Stato: In attesa di conferma aggiornamento...")
            if requested_age is not None:
                if requested_age < 60:
                    print(f"     ⏰ Richiesto {int(requested_age)}s fa")
                else:
                    print(f"     ⏰ Richiesto {int(requested_age/60)}m fa")
            print()
    
    # Worker con versione non allineata
    if version_mismatch:
        print("⚠️ WORKER CON VERSIONE NON ALLINEATA:")
        print("-" * 80)
        for w in version_mismatch:
            name = w.get("name") or w.get("worker_id")
            wid = w.get("worker_id")
            target = w.get("target_version")
            ack_ver = w.get("ack_version")
            patch = w.get("ack_patch")
            ack_age = w.get("ack_age_seconds")
            
            print(f"  ⚠️ {name} ({wid[:8]}...)")
            print(f"     Target: {target}")
            print(f"     Master: {master_version}")
            print(f"     ACK versione: {ack_ver}" + (f" (patch: {patch})" if patch else ""))
            print(f"     ⚠️ Versione NON allineata! (ACK: {ack_ver} != Target: {target})")
            if ack_age is not None:
                if ack_age < 60:
                    print(f"     ⏰ ACK ricevuto {int(ack_age)}s fa")
                else:
                    print(f"     ⏰ ACK ricevuto {int(ack_age/60)}m fa")
            print()
    
    # Worker senza aggiornamenti pendenti
    if no_update:
        print("ℹ️ WORKER SENZA AGGIORNAMENTI PENDENTI:")
        print("-" * 80)
        for w in no_update:
            name = w.get("name") or w.get("worker_id")
            wid = w.get("worker_id")
            ack_ver = w.get("ack_version")
            
            print(f"  ℹ️ {name} ({wid[:8]}...)")
            if ack_ver:
                if ack_ver == master_version:
                    print(f"     Versione: {ack_ver} ✅ (allineato con master)")
                else:
                    print(f"     Versione: {ack_ver} ⚠️ (diversa da master: {master_version})")
            else:
                print(f"     ⚠️ Nessun ACK ricevuto - versione sconosciuta")
            print()
    
    # Riepilogo
    print(f"{'=' * 80}")
    print(f"📊 RIEPILOGO:")
    print(f"   ✅ Allineati: {len(updated_ok)}")
    print(f"   ⏳ In attesa: {len(pending_ack)}")
    print(f"   ⚠️ Non allineati: {len(version_mismatch)}")
    print(f"   ℹ️ Nessun update: {len(no_update)}")
    print(f"{'=' * 80}\n")


def cmd_update_all(master_url: str, include_local: bool):
    master_url = _normalize_url(master_url)
    params = {"exclude_local": "false" if include_local else "true"}
    resp = requests.post(f"{master_url}/workers/update_all", params=params, timeout=60)  # Timeout più lungo per generazione zip
    resp.raise_for_status()
    data = resp.json()

    print("\nAggiornamento codice richiesto per tutti i worker remoti.")
    print(f"Target code_version master: {data.get('target_version')}")
    print(f"Updated workers: {len(data.get('updated_workers', []))}")
    print(f"Skipped workers (local/master): {len(data.get('skipped_workers', []))}\n")


def _send_command(master_url: str, worker_id: str, command: str):
    master_url = _normalize_url(master_url)
    resp = requests.post(
        f"{master_url}/worker/send_command",
        params={"worker_id": worker_id, "command": command},
        timeout=5,
    )
    if resp.status_code != 200:
        print(f"Errore: status {resp.status_code} - {resp.text}")
        sys.exit(1)
    data: Dict[str, Any] = resp.json()
    print(f"\nComando '{command}' accodato per worker {worker_id}")
    print(data)


def cmd_restart_worker(master_url: str, worker_id: str):
    _send_command(master_url, worker_id, "restart_worker")


def cmd_update_worker(master_url: str, worker_id: str):
    _send_command(master_url, worker_id, "update_code")


def cmd_reboot_worker(master_url: str, worker_id: str):
    _send_command(master_url, worker_id, "reboot_host")


def main():
    parser = argparse.ArgumentParser(description="Master Admin CLI - controllo remoto worker")
    parser.add_argument(
        "--master-url",
        type=str,
        required=True,
        help="URL del master server (es: http://51.91.11.36:8000)",
    )

    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("list-workers", help="Mostra lo stato dei worker (heartbeat, job attivi)")

    sub.add_parser("update-status", help="Mostra lo stato degli aggiornamenti codice (ACK/version_ok)")

    p_up_all = sub.add_parser("update-all", help="Aggiorna codice per tutti i worker remoti")
    p_up_all.add_argument(
        "--include-local",
        action="store_true",
        help="Includi anche il worker locale/master nell'update",
    )

    p_up_restart_all = sub.add_parser(
        "update-and-restart-all",
        help="Aggiorna codice e riavvia tutti i worker remoti",
    )
    p_up_restart_all.add_argument(
        "--include-local",
        action="store_true",
        help="Includi anche il worker locale/master nell'update",
    )

    p_restart = sub.add_parser("restart-worker", help="Riavvia il processo worker remoto")
    p_restart.add_argument("worker_id", help="ID del worker da riavviare")

    p_up_one = sub.add_parser("update-worker", help="Aggiorna codice per un singolo worker")
    p_up_one.add_argument("worker_id", help="ID del worker da aggiornare")

    p_reboot = sub.add_parser("reboot-worker", help="Richiede reboot dell'host remoto (se sudo configurato)")
    p_reboot.add_argument("worker_id", help="ID del worker (host) da riavviare")

    args = parser.parse_args()

    try:
        if args.command == "list-workers":
            cmd_list_workers(args.master_url)
        elif args.command == "update-status":
            cmd_update_status(args.master_url)
        elif args.command == "update-all":
            cmd_update_all(args.master_url, include_local=args.include_local)
        elif args.command == "update-and-restart-all":
            cmd_update_all(args.master_url, include_local=args.include_local)
        elif args.command == "restart-worker":
            cmd_restart_worker(args.master_url, args.worker_id)
        elif args.command == "update-worker":
            cmd_update_worker(args.master_url, args.worker_id)
        elif args.command == "reboot-worker":
            cmd_reboot_worker(args.master_url, args.worker_id)
        else:
            parser.print_help()
            sys.exit(1)
    except requests.RequestException as e:
        print(f"Errore connessione al master: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
