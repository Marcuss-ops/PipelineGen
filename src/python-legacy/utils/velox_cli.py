#!/usr/bin/env python3
"""
Velox CLI - Strumento unico per interagire con il Job Master.

Esempi:
  velox_cli.py workers list --master-url http://localhost:8000
  velox_cli.py workers drain worker-id-123 --master-url http://localhost:8000
  velox_cli.py workers revoke worker-id-123 --master-url http://localhost:8000
  velox_cli.py jobs stats --master-url http://localhost:8000
"""

import argparse
import sys
from typing import Any, Dict

import requests


def request_json(method: str, url: str, **kwargs) -> Dict[str, Any]:
    resp = requests.request(method, url, timeout=20, **kwargs)
    resp.raise_for_status()
    if resp.content:
        return resp.json()
    return {}


def cmd_workers_list(master_url: str) -> None:
    url = f"{master_url.rstrip('/')}/workers_status"
    data = request_json("GET", url)
    workers = data.get("workers", [])
    print(f"Workers attivi: {len(workers)}")
    for w in workers:
        wid = w.get("worker_id", "")[:16]
        name = w.get("name") or w.get("display_name") or wid
        state = w.get("state") or w.get("status")
        jobs = w.get("active_jobs", 0)
        print(f"- {wid}...  {name}  state={state}  active_jobs={jobs}")


def cmd_workers_drain(master_url: str, worker_id: str) -> None:
    url = f"{master_url.rstrip('/')}/drain_worker"
    data = request_json(
        "POST",
        url,
        json={"worker_id": worker_id, "command": "update_code_and_restart"},
    )
    print(f"Drain avviato per {worker_id}: {data}")


def cmd_workers_revoke(master_url: str, worker_id: str) -> None:
    url = f"{master_url.rstrip('/')}/workers/revoke"
    data = request_json("POST", url, json={"worker_id": worker_id})
    print(f"Worker revocato: {data}")


def cmd_workers_quarantine(master_url: str, worker_id: str) -> None:
    url = f"{master_url.rstrip('/')}/workers/quarantine"
    data = request_json("POST", url, json={"worker_id": worker_id})
    print(f"Worker in quarantena: {data}")


def cmd_jobs_stats(master_url: str) -> None:
    url = f"{master_url.rstrip('/')}/cluster_view"
    resp = requests.get(url, timeout=20)
    if not resp.ok:
        print(f"Errore cluster_view: HTTP {resp.status_code}", file=sys.stderr)
        sys.exit(1)
    # Per la CLI basta stampare l'URL da aprire
    print(f"Cluster view disponibile su: {url}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Velox CLI")
    parser.add_argument(
        "--master-url",
        type=str,
        default="http://localhost:8000",
        help="URL del Job Master",
    )
    subparsers = parser.add_subparsers(dest="command")

    # workers list
    p_workers = subparsers.add_parser("workers", help="Operazioni sui worker")
    workers_sub = p_workers.add_subparsers(dest="workers_cmd")

    workers_sub.add_parser("list", help="Lista worker")

    p_drain = workers_sub.add_parser("drain", help="Metti un worker in drain")
    p_drain.add_argument("worker_id")

    p_revoke = workers_sub.add_parser("revoke", help="Revoca un worker")
    p_revoke.add_argument("worker_id")

    p_quarantine = workers_sub.add_parser(
        "quarantine", help="Mette un worker in quarantena"
    )
    p_quarantine.add_argument("worker_id")

    # jobs stats
    p_jobs = subparsers.add_parser("jobs", help="Operazioni sui job")
    jobs_sub = p_jobs.add_subparsers(dest="jobs_cmd")
    jobs_sub.add_parser("stats", help="Mostra link Cluster View")

    args = parser.parse_args()
    master_url = args.master_url

    if args.command == "workers":
        if args.workers_cmd == "list":
            cmd_workers_list(master_url)
        elif args.workers_cmd == "drain":
            cmd_workers_drain(master_url, args.worker_id)
        elif args.workers_cmd == "revoke":
            cmd_workers_revoke(master_url, args.worker_id)
        elif args.workers_cmd == "quarantine":
            cmd_workers_quarantine(master_url, args.worker_id)
        else:
            parser.print_help()
            sys.exit(1)
    elif args.command == "jobs":
        if args.jobs_cmd == "stats":
            cmd_jobs_stats(master_url)
        else:
            parser.print_help()
            sys.exit(1)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()

