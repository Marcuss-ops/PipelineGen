#!/usr/bin/env python3
"""
Genera script documentario usando API Go Script Pipeline
Uso: python3 scripts/generate_script_from_text.py --topic "Gervonta Davis" --text-file testo.txt
"""

import json
import requests
import sys
import os
import time
import subprocess

API_PORT = 8080
API_BASE = f"http://localhost:{API_PORT}/api/script-pipeline"

def check_server():
    """Verifica se server Go è attivo sulla porta 8080"""
    try:
        r = requests.get(f"http://localhost:{API_PORT}/health", timeout=2)
        return r.status_code == 200
    except:
        return False

def start_server():
    """Avvia server Go in background se non è attivo"""
    if check_server():
        print("✓ Server Go già attivo")
        return True
    
    print("▶ Avvio server Go...")
    # Cerca il server in vari percorsi
    server_paths = [
        os.path.join(os.path.dirname(__file__), "..", "go-master", "server"),
        "/home/pierone/Pyt/VeloxEditing/refactored/go-master/server",
        os.path.join(os.getcwd(), "go-master", "server")
    ]
    
    server_path = None
    for path in server_paths:
        if os.path.exists(path):
            server_path = path
            break
    
    if not server_path:
        print(f"✗ Server non trovato")
        print("  Compila con: cd go-master && make build")
        return False
    
    server_dir = os.path.dirname(server_path)
    print(f"  Trovato: {server_path}")
    
    env = os.environ.copy()
    env["VELOX_PORT"] = "8080"
    env["VELOX_LOG_LEVEL"] = "info"
    env["VELOX_HOST"] = "0.0.0.0"
    
    subprocess.Popen(
        [server_path],
        cwd=server_dir,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True
    )
    
    for i in range(30):
        time.sleep(1)
        if check_server():
            print("✓ Server Go avviato")
            return True
    
    print("✗ Timeout avvio server")
    return False

def divide_text(text, max_segments=5):
    """Divide testo in segmenti"""
    url = f"{API_BASE}/divide"
    r = requests.post(url, json={"script": text, "max_segments": max_segments}, timeout=30)
    return r.json()

def extract_entities(segments, max_entities=10):
    """Estrae entità dai segmenti"""
    url = f"{API_BASE}/extract-entities"
    r = requests.post(url, json={"segments": segments, "max_entities": max_entities}, timeout=30)
    return r.json()

def associate_stock(segments, entities, topic):
    """Associa clip Stock"""
    url = f"{API_BASE}/associate-stock"
    r = requests.post(url, json={
        "segments": segments,
        "entities": entities,
        "topic": topic
    }, timeout=30)
    return r.json()

def associate_artlist(segments, entities):
    """Associa clip Artlist"""
    url = f"{API_BASE}/associate-artlist"
    r = requests.post(url, json={
        "segments": segments,
        "entities": entities
    }, timeout=30)
    return r.json()

def create_document(title, topic, duration, script, entities_data, stock_clips, artlist_clips, segments):
    """Crea documento con 3 sezioni: Artlist | Drive Stock | Drive Associations"""
    url = f"{API_BASE}/create-doc"
    
    # === ARTLIST SECTION - Clip Artlist con nome file ===
    artlist_items = []
    if artlist_clips.get("ok"):
        all_clips = artlist_clips.get("all_clips", [])
        for clip in all_clips[:15]:  # Max 15 clip Artlist
            if clip and isinstance(clip, dict):
                artlist_items.append({
                    "name": clip.get("name", "Artlist Clip"),
                    "folder": clip.get("folder", "Artlist/Generic"),
                    "term": clip.get("term", ""),
                    "url": clip.get("url", "")
                })
    
    # === DRIVE ASSOCIATIONS - Stock clips con timestamp da segmenti ===
    drive_items = []
    if stock_clips.get("ok"):
        seg_data = stock_clips.get("segment_data", [])
        for i, seg in enumerate(seg_data[:5]):  # Max 5 segmenti
            if seg and isinstance(seg, dict):
                seg_clips = seg.get("clips") or []  # Gestisce None
                for clip in seg_clips[:3]:  # Max 3 clip per segmento
                    if clip and isinstance(clip, dict):
                        drive_items.append({
                            "filename": clip.get("filename", "Unknown"),
                            "folder": clip.get("folder_path", "Stock"),
                            "timestamp": f"{i*20}-{i*20+20}s",
                            "matched_term": clip.get("matched_term", ""),
                            "drive_link": clip.get("drive_link", "")
                        })
    
    payload = {
        "title": title,
        "topic": topic,
        "duration": duration,
        "template": "biography",
        "script": script,
        "language": "en",
        "frasi_importanti": entities_data.get("frasi_importanti", [])[:5],
        "nomi_speciali": entities_data.get("nomi_speciali", [])[:10],
        "parole_importanti": entities_data.get("parole_importanti", [])[:10],
        "entita_con_immergine": entities_data.get("entita_con_immagine", [])[:5],
        "artlist_items": artlist_items,  # Nuovo: elenco Artlist
        "drive_items": drive_items,      # Nuovo: Drive con timestamp
        "stock_assocs": [],              # Vuoto
        "artlist_assocs": []             # Vuoto
    }
    
    r = requests.post(url, json=payload, timeout=30)
    return r.json()

def main():
    import argparse
    parser = argparse.ArgumentParser(description="Genera script documentario")
    parser.add_argument("--topic", required=True, help="Topic del documentario")
    parser.add_argument("--text-file", help="File con testo di input")
    parser.add_argument("--duration", type=int, default=120, help="Durata target in secondi")
    parser.add_argument("--title", help="Titolo documento (default: topic)")
    parser.add_argument("--output", default="/tmp/script_output.json", help="Output JSON file")
    
    args = parser.parse_args()
    
    title = args.title or f"{args.topic}: The Complete Story"
    
    if not args.text_file:
        print(f"▶ Nessun file testo fornito, uso stub per {args.topic}")
        text = f"{args.topic} is a famous person with an incredible story."
    else:
        with open(args.text_file, 'r') as f:
            text = f.read()
    
    print(f"\n{'='*60}")
    print(f"  GENERAZIONE DOCUMENTARIO")
    print(f"  Topic: {args.topic}")
    print(f"{'='*60}\n")
    
    if not start_server():
        print("✗ Impossibile avviare server")
        sys.exit(1)
    
    # Step 1: Divide
    print("\nStep 1: Dividendo testo in segmenti...")
    divide_result = divide_text(text)
    if not divide_result.get("ok"):
        print(f"✗ Errore: {divide_result.get('error')}")
        sys.exit(1)
    segments = divide_result.get("segments", [])
    print(f"✓ {len(segments)} segmenti creati")
    
    # Step 2: Extract Entities
    print("\nStep 2: Estraendo entità...")
    extract_result = extract_entities(segments)
    if not extract_result.get("ok"):
        print(f"✗ Errore: {extract_result.get('error')}")
        sys.exit(1)
    
    print(f"  Frasi importanti: {len(extract_result.get('frasi_importanti', []))}")
    print(f"  Nomi speciali: {len(extract_result.get('nomi_speciali', []))}")
    print(f"  Parole importanti: {len(extract_result.get('parole_importanti', []))}")
    print(f"  Entity con immagini: {len(extract_result.get('entita_con_immagine', []))}")
    
    for ent in extract_result.get("entita_con_immagine", [])[:5]:
        print(f"    • {ent['entity']}: {ent['image_url'][:50]}...")
    
    # Step 3: Associa Stock
    print("\nStep 3: Associando clip Stock...")
    entities_list = extract_result.get("nomi_speciali", []) + extract_result.get("parole_importanti", [])[:10]
    stock_result = associate_stock(segments, entities_list, args.topic)
    if stock_result.get("ok"):
        print(f"✓ {len(stock_result.get('all_clips', []))} clip Stock trovate")
    else:
        print(f"⚠ {stock_result.get('error', 'StockDB non disponibile')}")
    
    # Step 4: Associa Artlist
    print("\nStep 4: Associando clip Artlist...")
    artlist_result = associate_artlist(segments, entities_list[:5])
    if artlist_result.get("ok"):
        print(f"✓ {len(artlist_result.get('all_clips', []))} clip Artlist trovate")
    else:
        print(f"⚠ {artlist_result.get('error', 'ArtlistDB non disponibile')}")
    
    # Step 5: Create Document
    print("\nStep 5: Creando documento...")
    doc_result = create_document(
        title, args.topic, args.duration, text,
        extract_result, stock_result, artlist_result, segments
    )
    
    if doc_result.get("ok"):
        doc_url = doc_result.get("doc_url", "N/A")
        print(f"\n{'='*60}")
        print(f"  ✓ DOCUMENTO CREATO!")
        print(f"{'='*60}")
        print(f"\n  Titolo: {title}")
        print(f"  URL: {doc_url}")
        print(f"\n{'='*60}")
    else:
        print(f"✗ Errore creazione documento: {doc_result.get('error')}")
    
    output = {
        "topic": args.topic,
        "title": title,
        "duration": args.duration,
        "segments": segments,
        "entities": extract_result,
        "stock_clips": stock_result if stock_result.get("ok") else {},
        "artlist_clips": artlist_result if artlist_result.get("ok") else {},
        "document": doc_result if doc_result.get("ok") else {}
    }
    
    with open(args.output, 'w') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    
    print(f"\n  JSON salvato: {args.output}")

if __name__ == "__main__":
    main()
