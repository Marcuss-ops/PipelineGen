#!/usr/bin/env python3
"""Live smoke test for the retrieved-image acquisition path.

Runs 100 different queries through the running PipelineGen server:
search -> download -> media persistence -> Drive publication. Each query is
reported individually so a failure names the topic instead of only the count.

Usage:
  python3 scripts/images_live_100.py --token "$VELOX_ADMIN_TOKEN"
  python3 scripts/images_live_100.py --token "$VELOX_ADMIN_TOKEN" --concurrency 4
  python3 scripts/images_live_100.py --token "$VELOX_ADMIN_TOKEN" --out /tmp/report.json

The token can also be supplied through VELOX_ADMIN_TOKEN. The script never
prints the token.
"""

from __future__ import annotations

import argparse
import json
import os
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone

QUERIES = [
    # People
    "Anastasia Trofimova", "Francis Kaufmann", "Chiara Poggi", "Alberto Stasi",
    "Michael Jordan", "Taylor Swift", "Elon Musk", "Tim Cook", "Angela Taccia",
    "Donald Trump", "Joe Biden", "Pope Francis", "Giorgia Meloni",
    "Sergio Mattarella", "Cristiano Ronaldo", "Lionel Messi", "Mike Tyson",
    "Tom Cruise", "Meryl Streep", "David Bowie",
    # Places
    "Villa Pamphili Roma", "Colosseo Roma", "Torre Eiffel Parigi",
    "Statua della Liberta New York", "Ponte di Brooklyn", "Duomo di Milano",
    "Canal Grande Venezia", "Piazza San Marco", "Machu Picchu", "Grand Canyon",
    "Monte Fuji", "Deserto del Sahara", "Canali di Amsterdam", "Tram 28 Lisbona",
    "Porta di Brandeburgo Berlino", "Ponte Carlo Praga", "Santorini Oia",
    "Cascata Skogafoss Islanda", "Tre Cime di Lavaredo", "Costiera Amalfitana",
    # Events
    "processo penale aula tribunale", "conferenza stampa parlamento",
    "manifestazione in piazza", "incidente stradale notturno",
    "soccorso ambulanze", "indagine polizia scientifica",
    "archivio storico documenti", "elezioni urne", "concerto stadio",
    "partita di calcio", "cerimonia premiazione", "mostra d'arte",
    "festival del cinema", "matrimonio", "laurea universitaria",
    "tramonto sul mare", "temporale fulmini", "neve in citta",
    "incendio boschivo", "alluvione",
    # Objects and nature
    "macchina fotografica vintage", "telefono anni 90", "computer portatile",
    "orologio da polso", "bicicletta da corsa", "automobile classica",
    "aereo in decollo", "treno ad alta velocita", "nave cargo",
    "farfalla monarca", "lupo grigio", "orso polare", "tigre del bengala",
    "pinguino imperatore", "barriera corallina", "foresta amazzonica",
    "vulcano in eruzione", "aurora boreale", "deserto al tramonto",
    "cascata tropicale",
    # Concepts and technology
    "intelligenza artificiale robot", "data center server", "pannelli solari",
    "turbine eoliche", "laboratorio scientifico", "microscopio elettronico",
    "stazione spaziale", "lancio razzo", "satellite orbitale",
    "telescopio astronomico", "sala operatoria", "risonanza magnetica",
    "farmaci blister", "DNA elica", "cervello umano modello",
    "occhio umano macro", "mani che scrivono", "libri antichi biblioteca",
    "mappa geografica antica", "bussola navigazione",
]


def search_one(base_url: str, token: str, query: str, lang: str, timeout: float) -> dict:
    params = urllib.parse.urlencode(
        {"q": query, "provider": "duckduckgo", "lang": lang, "limit": 1}
    )
    url = f"{base_url.rstrip('/')}/api/images/retrieved/search?{params}"
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    started = time.time()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = json.load(response)
        results = payload.get("results") or []
        first = results[0] if results else {}
        return {
            "query": query,
            "ok": bool(results),
            "count": len(results),
            "asset_id": first.get("asset_id"),
            "preview_url": first.get("preview_url"),
            "cache_hit": first.get("cache_hit"),
            "elapsed_ms": int((time.time() - started) * 1000),
        }
    except urllib.error.HTTPError as exc:
        detail = exc.read(500).decode("utf-8", "replace")
        return {
            "query": query,
            "ok": False,
            "error": f"HTTP {exc.code}: {detail}",
            "elapsed_ms": int((time.time() - started) * 1000),
        }
    except Exception as exc:  # noqa: BLE001 - report the concrete transport failure
        return {
            "query": query,
            "ok": False,
            "error": f"{type(exc).__name__}: {exc}",
            "elapsed_ms": int((time.time() - started) * 1000),
        }


def percentile(values: list[int], fraction: float) -> int:
    if not values:
        return 0
    ordered = sorted(values)
    index = min(len(ordered) - 1, max(0, int(round((len(ordered) - 1) * fraction))))
    return ordered[index]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8000")
    parser.add_argument("--token", default=os.environ.get("VELOX_ADMIN_TOKEN", ""))
    parser.add_argument("--lang", default="it")
    parser.add_argument("--concurrency", type=int, default=4)
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument("--fail-under", type=float, default=1.0)
    parser.add_argument("--out", default="images_live_100_report.json")
    args = parser.parse_args()

    if not args.token:
        print("error: pass --token or set VELOX_ADMIN_TOKEN", file=sys.stderr)
        return 2
    if len(QUERIES) != 100:
        print(f"error: expected 100 queries, found {len(QUERIES)}", file=sys.stderr)
        return 2

    started = time.time()
    results: list[dict] = []
    with ThreadPoolExecutor(max_workers=max(1, args.concurrency)) as pool:
        futures = {
            pool.submit(search_one, args.base_url, args.token, query, args.lang, args.timeout): query
            for query in QUERIES
        }
        for future in as_completed(futures):
            results.append(future.result())

    results.sort(key=lambda item: QUERIES.index(item["query"]))
    successes = [item for item in results if item["ok"]]
    failures = [item for item in results if not item["ok"]]
    elapsed = [item["elapsed_ms"] for item in results]
    unique_assets = {item["asset_id"] for item in successes if item.get("asset_id")}
    report = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "base_url": args.base_url,
        "total": len(results),
        "success": len(successes),
        "failure": len(failures),
        "success_rate": round(len(successes) / len(results), 4) if results else 0,
        "unique_assets": len(unique_assets),
        "elapsed_ms": int((time.time() - started) * 1000),
        "latency_ms": {
            "p50": percentile(elapsed, 0.50),
            "p95": percentile(elapsed, 0.95),
            "max": max(elapsed) if elapsed else 0,
        },
        "failures": failures,
        "results": results,
    }

    with open(args.out, "w", encoding="utf-8") as handle:
        json.dump(report, handle, ensure_ascii=False, indent=2)
        handle.write("\n")

    print(
        f"images: {len(successes)}/{len(results)} ok "
        f"({report['success_rate']:.1%}), unique_assets={len(unique_assets)}, "
        f"p50={report['latency_ms']['p50']}ms p95={report['latency_ms']['p95']}ms, "
        f"report={args.out}"
    )
    for failure in failures[:10]:
        print(f"  FAIL {failure['query']}: {failure.get('error', 'no result')}")
    if len(failures) > 10:
        print(f"  ... {len(failures) - 10} more failures in {args.out}")

    return 0 if report["success_rate"] >= args.fail_under else 1


if __name__ == "__main__":
    raise SystemExit(main())
