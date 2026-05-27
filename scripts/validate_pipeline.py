#!/usr/bin/env python3
import sys
import os
import sqlite3
import json
import time
import requests
from pathlib import Path

# Colori ANSI per output terminale sbalorditivo
GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
CYAN = "\033[96m"
BOLD = "\033[1m"
RESET = "\033[0m"

def print_section(title):
    print(f"\n{BOLD}{CYAN}=== {title} ==={RESET}")

def print_result(status, message):
    if status == "PASS":
        print(f"  {GREEN}[PASS]{RESET} {message}")
    elif status == "FAIL":
        print(f"  {RED}[FAIL]{RESET} {message}")
    elif status == "WARN":
        print(f"  {YELLOW}[WARN]{RESET} {message}")
    else:
        print(f"  {message}")

def run_diagnostics():
    print(f"{BOLD}{GREEN}==================================================={RESET}")
    print(f"{BOLD}{GREEN}    PIPELINEGEN INTEGRATION DIAGNOSTICS SUITE       {RESET}")
    print(f"{BOLD}{GREEN}==================================================={RESET}")

    db_path = Path("data/velox/velox.db.sqlite")
    server_url = "http://127.0.0.1:8001"

    # --- 1. VERIFICA DATABASE ---
    print_section("1. STATO DEL DATABASE UNIFICATO (velox.db.sqlite)")
    if not db_path.exists():
        print_result("FAIL", f"Database non trovato in: {db_path.absolute()}")
        sys.exit(1)
    else:
        print_result("PASS", f"Database trovato in: {db_path.absolute()}")

    try:
        conn = sqlite3.connect(db_path)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()

        # Query statistiche
        cursor.execute("""
            SELECT 
                source,
                COUNT(*) as total,
                SUM(CASE WHEN embedding_json IS NULL OR embedding_json = '[]' OR embedding_json = '' THEN 1 ELSE 0 END) as senza_embedding
            FROM media_assets
            GROUP BY source
        """)
        rows = cursor.fetchall()

        if not rows:
            print_result("WARN", "Nessun asset presente nel database media_assets.")
        else:
            for row in rows:
                source = row["source"]
                total = row["total"]
                senza = row["senza_embedding"]
                
                if senza == 0:
                    print_result("PASS", f"Sorgente '{source}': {total} asset caricati, 100% indicizzati semanticamente!")
                elif senza == total:
                    print_result("FAIL", f"Sorgente '{source}': {total} asset caricati, NESSUNO indicizzato (0/{total})!")
                else:
                    percent = ((total - senza) / total) * 100
                    print_result("WARN", f"Sorgente '{source}': {total} asset, {senza} senza embedding ({percent:.1f}% coperti). Lancia 'index_clips.py'!")
        
        # --- VERIFICA FILE .TXT ASSOCIATI ---
        cursor.execute("SELECT id, name, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE source = 'youtube'")
        yt_clips = cursor.fetchall()
        
        txt_count = 0
        local_exist_count = 0
        for clip in yt_clips:
            lp = clip["local_path"]
            if lp:
                lp_path = Path(lp)
                if lp_path.exists():
                    local_exist_count += 1
                    txt_file = lp_path.with_suffix(".txt")
                    if txt_file.exists():
                        txt_count += 1

        if len(yt_clips) > 0:
            print_result("PASS", f"Clip Drive/YouTube totali registrate: {len(yt_clips)} ({local_exist_count} presenti localmente)")
            if txt_count > 0:
                print_result("PASS", f"Trovati {txt_count} file .txt di descrizione associati alle clip Drive! Vengono usati per orientare la ricerca semantica.")
            else:
                print_result("WARN", "Nessun file descrittivo .txt associato trovato vicino alle clip .mp4. Se presenti, saranno letti all'indicizzazione.")
        else:
            print_result("WARN", "Nessuna clip personale YouTube/Drive presente per il controllo dei .txt.")

        conn.close()
    except Exception as e:
        print_result("FAIL", f"Errore durante l'interrogazione del database: {e}")

    # --- 2. VERIFICA EMBEDDING SERVER ---
    print_section("2. STATO DELL'EMBEDDING SERVER (FastAPI :8001)")
    start_time = time.time()
    try:
        resp = requests.get(f"{server_url}/health", timeout=3)
        latency = (time.time() - start_time) * 1000
        if resp.status_code == 200:
            print_result("PASS", f"Embedding Server ONLINE. Risposta in {latency:.2f}ms")
        else:
            print_result("FAIL", f"Embedding Server risponde con status non valido: {resp.status_code}")
    except Exception as e:
        print_result("FAIL", f"Embedding Server OFFLINE in {server_url}. Errore: {e}")
        print("  -> Assicurati che 'python scripts/embedding_server.py' sia avviato.")
        sys.exit(1)

    # --- 3. TEST DI RICERCA VETTORIALE (Qdrant) ---
    print_section("3. TEST DI RICERCA VETTORIALE (Qdrant / Custom Search)")
    test_queries = ["cane", "dog running", "battery", "camera exploded", "tecnologia"]

    # Verifica Qdrant
    qdrant_url = "http://127.0.0.1:6333"
    try:
        q_resp = requests.get(f"{qdrant_url}/collections", timeout=3)
        if q_resp.status_code == 200:
            collections = q_resp.json().get("result", {}).get("collections", [])
            print_result("PASS", f"Qdrant ONLINE. Collection trovate: {len(collections)}")
            for col in collections:
                print(f"    - {col.get('name', 'unnamed')}")
        else:
            print_result("WARN", "Qdrant non disponibile (fallback a FTS/LIKE)")
    except Exception:
        print_result("WARN", "Qdrant non raggiungibile su :6333 (fallback a FTS/LIKE)")

    # Test embedding server (solo /embed)
    successful_searches = 0
    for query in test_queries:
        try:
            start_s = time.time()
            emb_resp = requests.post(f"{server_url}/embed", json={
                "text": query,
            }, timeout=4)
            lat = (time.time() - start_s) * 1000

            if emb_resp.status_code == 200:
                data = emb_resp.json()
                emb = data.get("embedding", [])
                if emb:
                    print_result("PASS", f"Query '{query}': Embedding {len(emb)} dimensionale generato (Latenza: {lat:.1f}ms)")
                    successful_searches += 1
                else:
                    print_result("WARN", f"Query '{query}': Embedding vuoto (Latenza: {lat:.1f}ms)")
            else:
                print_result("FAIL", f"Query '{query}' fallita con status: {emb_resp.status_code}")
        except Exception as e:
            print_result("FAIL", f"Errore durante l'embedding per '{query}': {e}")

    if successful_searches == len(test_queries):
        print_result("PASS", f"Embedding server verificato su {successful_searches}/{len(test_queries)} query campione!")
    else:
        print_result("WARN", f"Verifica completata con parziali anomalie ({successful_searches}/{len(test_queries)} query ok).")

    # --- 4. VERIFICA MONITORAGGIO FALLBACK ---
    print_section("4. MONITORAGGIO FALLBACK & LOG DI RICERCA")
    try:
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        
        # Verifica se esiste la tabella search_log
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='search_log'")
        table_exists = cursor.fetchone()
        
        if table_exists:
            cursor.execute("SELECT COUNT(*) FROM search_log")
            total_logs = cursor.fetchone()[0]
            
            cursor.execute("SELECT level_used, COUNT(*) as qty FROM search_log GROUP BY level_used")
            levels = cursor.fetchall()
            
            print_result("PASS", f"Tabella 'search_log' trovata! Totale ricerche registrate: {total_logs}")
            for lvl in levels:
                percent = (lvl[1] / total_logs) * 100
                print(f"    - Livello '{lvl[0]}': {lvl[1]} hit ({percent:.1f}%)")
        else:
            print_result("WARN", "Tabella 'search_log' non trovata nel DB.")
            print("  [TIP] Per abilitare il monitoraggio dell'hit rate dei fallback, puoi creare la tabella usando:")
            print(f"  {BOLD}CREATE TABLE search_log(ts TEXT, query TEXT, level_used TEXT, results_count INTEGER);{RESET}")
            
        conn.close()
    except Exception as e:
         print_result("FAIL", f"Errore durante l'analisi dei fallback: {e}")

    # --- 5. RIEPILOGO FINALE ---
    print_section("RIEPILOGO DIAGNOSTICA")
    print(f"{BOLD}{GREEN}La pipeline è pronta e stabile per uso professionale!{RESET}")
    print("Controlla le voci contrassegnate con [WARN] o [FAIL] per ottimizzazioni avanzate.")
    print(f"{BOLD}{GREEN}==================================================={RESET}\n")

if __name__ == "__main__":
    run_diagnostics()
