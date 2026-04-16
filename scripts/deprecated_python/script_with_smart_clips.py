#!/usr/bin/env python3
"""
Script Generator con Smart Clip Association

Genera uno script completo e associa AUTOMATICAMENTE:
1. FRASI IMPORTANTI → Clip Drive (2020 clip disponibili)
2. RESTO DEL TESTO → Clip Stock (YouTube)
3. ~5 FRASI → Artlist clips (300 disponibili)

Uso:
  python3 scripts/script_with_smart_clips.py --topic "Andrew Tate" --duration 90
  python3 scripts/script_with_smart_clips.py --topic "Elvis Presley" --duration 120
"""

import json
import re
import subprocess
import requests
from collections import defaultdict
from typing import List, Dict, Tuple
from rapidfuzz import fuzz

# Costanti
CLIP_INDEX = "/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/data/clip_index.json"
ARTLIST_DB = "/home/pierone/Pyt/VeloxEditing/refactored/src/node-scraper/artlist_videos.db"
OLLAMA_URL = "http://localhost:11434"

def load_drive_clips():
    """Carica clip da Drive index"""
    with open(CLIP_INDEX, 'r') as f:
        data = json.load(f)

    clips = data.get('clips', [])
    folders = data.get('folders', [])

    folder_names = {fld.get('id', ''): fld.get('name', 'Unknown') for fld in folders}

    for clip in clips:
        fid = clip.get('folder_id', '')
        clip['folder_name'] = folder_names.get(fid, 'Unknown')
        clip['drive_url'] = f"https://drive.google.com/file/d/{clip.get('id', '')}/view?usp=drive_link"

    return clips

def load_artlist_clips():
    """Carica clip da Artlist DB"""
    try:
        import sqlite3
        conn = sqlite3.connect(ARTLIST_DB)
        cursor = conn.cursor()
        cursor.execute("""
            SELECT vl.name, vl.url, vl.tags, vl.duration, c.name as category
            FROM video_links vl
            LEFT JOIN categories c ON vl.category_id = c.id
            WHERE vl.source = 'artlist'
            LIMIT 300
        """)
        rows = cursor.fetchall()
        conn.close()

        clips = []
        for row in rows:
            clips.append({
                'name': row[0] or '',
                'url': row[1] or '',
                'tags': json.loads(row[2]) if row[2] else [],
                'duration': row[3] or 0,
                'category': row[4] or 'Artlist'
            })
        return clips
    except Exception as e:
        print(f"⚠️  Artlist DB error: {e}")
        return []

def generate_script_with_ollama(topic: str, duration_seconds: int = 90) -> Dict:
    """Genera script completo con Ollama, strutturato con frasi importanti"""

    # Calcola numero segmenti (circa 20 secondi ciascuno)
    num_segments = max(3, duration_seconds // 20)

    prompt = f"""Crea uno script video di {duration_seconds} secondi su "{topic}".

Struttura lo script in {num_segments} segmenti di circa 20 secondi ciascuno (50 parole).

Per OGNI segmento, fornisci:
1. Testo del segmento
2. Una lista delle FRASI IMPORTANTI (frasi che contengono informazioni chiave, citazioni, fatti specifici)
3. Una lista delle FRASI NORMALI (descrizioni, transizioni, contesto)
4. Una lista delle FRASI VISUALI (frasi che richiederebbero clip specifiche con azione, emozione, personaggi)

IMPORTANTE:
- Le FRASI IMPORTANTI sono fatti specifici, citazioni, nomi, date, numeri
- Le FRASI NORMALI sono collegamenti, introduzioni, transizioni
- Le FRASI VISUALI descrivono azioni, emozioni, scene con persone

Topic: {topic}
Durata: {duration_seconds} secondi
Numero segmenti: {num_segments}

Formato JSON:
{{
  "title": "Titolo del video",
  "segments": [
    {{
      "index": 1,
      "full_text": "Testo completo del segmento",
      "important_sentences": ["Frase importante 1", "Frase importante 2"],
      "normal_sentences": ["Frase normale 1", "Frase normale 2"],
      "visual_sentences": ["Frase visuale 1", "Frase visuale 2"]
    }}
  ]
}}"""

    try:
        response = requests.post(
            f"{OLLAMA_URL}/api/generate",
            json={
                "model": "gemma3:4b",
                "prompt": prompt,
                "stream": False,
                "options": {
                    "temperature": 0.7,
                    "num_predict": 2000
                }
            },
            timeout=60
        )

        if response.status_code == 200:
            result = response.json()
            text = result.get('response', '')

            # Estrai JSON dalla risposta
            json_start = text.find('{')
            json_end = text.rfind('}') + 1

            if json_start >= 0 and json_end > json_start:
                script_json = json.loads(text[json_start:json_end])
                return script_json
            else:
                print("⚠️  JSON non trovato, uso fallback")
                return generate_fallback_script(topic, num_segments)
        else:
            print(f"⚠️  Ollama error: {response.status_code}")
            return generate_fallback_script(topic, num_segments)

    except Exception as e:
        print(f"⚠️  Ollama generation failed: {e}")
        return generate_fallback_script(topic, num_segments)

def generate_fallback_script(topic: str, num_segments: int) -> Dict:
    """Script fallback se Ollama non è disponibile"""

    segments = []

    templates = [
        {
            "full_text": f"{topic} ha rivoluzionato il suo campo. Molti non conoscono la vera storia dietro il successo.",
            "important_sentences": [f"{topic} ha rivoluzionato il suo campo"],
            "normal_sentences": ["Molti non conoscono la vera storia dietro il successo"],
            "visual_sentences": [f"{topic} nel suo elemento"]
        },
        {
            "full_text": "I momenti più iconici hanno lasciato il segno nella storia. Le testimonianze parlano di un impatto enorme.",
            "important_sentences": ["I momenti più iconici hanno lasciato il segno nella storia"],
            "normal_sentences": ["Le testimonianze parlano di un impatto enorme"],
            "visual_sentences": ["Momenti iconici in azione"]
        },
        {
            "full_text": "Le conseguenze di queste azioni si sentono ancora oggi. Il futuro sarà influenzato da queste scelte.",
            "important_sentences": ["Le conseguenze di queste azioni si sentono ancora oggi"],
            "normal_sentences": ["Il futuro sarà influenzato da queste scelte"],
            "visual_sentences": ["Impatto sul futuro"]
        },
    ]

    for i in range(num_segments):
        template = templates[i % len(templates)]
        segments.append({
            "index": i + 1,
            **template
        })

    return {
        "title": f"Tutto su {topic}",
        "segments": segments
    }

def find_drive_clips_for_sentences(sentences: List[str], drive_clips: List[Dict], topic: str) -> List[Dict]:
    """Trova clip Drive per le frasi importanti"""
    results = []

    for sentence in sentences:
        # Estrai entities dalla frase
        words = re.findall(r'\b\w+\b', sentence.lower())
        entities = [w for w in words if len(w) > 3 and w not in ['questo', 'quella', 'della', 'sono', 'come', 'molto', 'essere', 'avere']]

        best_clip = None
        best_score = 0

        for clip in drive_clips:
            name = clip.get('name', '').lower()
            folder = clip.get('folder_name', '').lower()
            tags = ' '.join(clip.get('tags', [])).lower()

            score = 0

            for entity in entities:
                # Exact folder match = best
                if entity in folder:
                    score = max(score, 100)
                # Name match
                elif entity in name:
                    score = max(score, 90)
                # Fuzzy
                else:
                    name_score = fuzz.partial_ratio(entity, name)
                    folder_score = fuzz.partial_ratio(entity, folder)
                    score = max(score, max(name_score, folder_score) * 0.8)

            if score > best_score and score > 50:
                best_score = score
                best_clip = {
                    **clip,
                    'sentence': sentence,
                    'match_score': score,
                    'matched_entity': next((e for e in entities if e in clip.get('folder_name', '').lower()), entities[0] if entities else '')
                }

        if best_clip:
            results.append(best_clip)

    return results[:len(sentences)]  # Max 1 clip per frase

def find_artlist_clips_for_sentences(sentences: List[str], artlist_clips: List[Dict], count: int = 5) -> List[Dict]:
    """Trova clip Artlist per ~5 frasi"""
    if not artlist_clips or not sentences:
        return []

    # Prendi le prime 'count' frasi visive
    target_sentences = sentences[:count]
    results = []

    for i, sentence in enumerate(target_sentences):
        # Scegli clip Artlist distribuite
        clip_idx = (i * 3) % len(artlist_clips)
        clip = artlist_clips[clip_idx]

        results.append({
            **clip,
            'sentence': sentence,
            'source': 'artlist'
        })

    return results

def search_youtube_clips(sentences: List[str], count_per_sentence: int = 2) -> List[Dict]:
    """Cerca clip YouTube per le frasi normali/stock"""
    results = []

    for sentence in sentences:
        # Estrai keywords per la ricerca
        words = re.findall(r'\b\w+\b', sentence.lower())
        keywords = [w for w in words if len(w) > 3 and w not in ['questo', 'quella', 'della', 'sono', 'come', 'molto', 'essere', 'avere']]

        if not keywords:
            continue

        query = ' '.join(keywords[:3])

        try:
            yt_dlp_path = "/home/pierone/venv/bin/yt-dlp"
            cmd = subprocess.run(
                [yt_dlp_path, f"ytsearch{count_per_sentence}:{query}", "--dump-json", "--flat-playlist", "--no-warnings"],
                capture_output=True,
                text=True,
                timeout=20
            )

            if cmd.returncode == 0:
                for line in cmd.stdout.strip().split('\n'):
                    if line.strip().startswith('{'):
                        try:
                            video = json.loads(line)
                            results.append({
                                'title': video.get('title', ''),
                                'url': video.get('url', f"https://youtube.com/watch?v={video.get('id', '')}"),
                                'duration': video.get('duration', 0),
                                'sentence': sentence,
                                'search_query': query,
                                'source': 'youtube'
                            })
                        except:
                            continue
        except:
            continue

    return results

def display_full_script_with_clips(topic: str, script: Dict, drive_results: List[Dict], artlist_results: List[Dict], youtube_results: List[Dict]):
    """Stampa script completo con clip associate"""

    print("=" * 100)
    print(f"  📝 SCRIPT COMPLETO CON CLIP ASSOCIATE")
    print(f"  Topic: {topic}")
    print(f"  Titolo: {script.get('title', 'N/A')}")
    print("=" * 100)
    print()

    total_drive = 0
    total_artlist = 0
    total_youtube = 0

    for segment in script.get('segments', []):
        idx = segment.get('index', 0)
        full_text = segment.get('full_text', '')

        print(f"{'═' * 100}")
        print(f"  📍 SEGMENTO {idx}")
        print(f"{'═' * 100}")
        print(f"\n  📜 TESTO COMPLETO:")
        print(f"  {full_text}")
        print()

        # Frasi importanti → Drive clips
        important = segment.get('important_sentences', [])
        if important:
            print(f"  🔴 FRASI IMPORTANTI ({len(important)}) → CLIP DRIVE:")
            for sentence in important:
                print(f"     💬 \"{sentence}\"")

                # Trova clip associata
                drive_clip = next((d for d in drive_results if d.get('sentence') == sentence), None)
                if drive_clip:
                    print(f"     🎬 Clip: {drive_clip.get('name', 'N/A')}")
                    print(f"     📁 Folder: {drive_clip.get('folder_name', 'N/A')}")
                    print(f"     🎯 Match: {drive_clip.get('match_score', 0):.0f}%")
                    print(f"     🔗 {drive_clip.get('drive_url', '')}")
                    total_drive += 1
                else:
                    print(f"     ⚠️  Nessuna clip Drive trovata")
                print()

        # Frasi normali → YouTube/Stock
        normal = segment.get('normal_sentences', [])
        if normal:
            print(f"  🟡 FRASI NORMALI ({len(normal)}) → CLIP STOCK (YouTube):")
            for sentence in normal:
                print(f"     💬 \"{sentence}\"")

                yt_clip = next((y for y in youtube_results if y.get('sentence') == sentence), None)
                if yt_clip:
                    print(f"     📺 Stock: {yt_clip.get('title', 'N/A')}")
                    print(f"     ⏱️  Durata: {yt_clip.get('duration', 0)}s")
                    print(f"     🔗 {yt_clip.get('url', '')}")
                    total_youtube += 1
                else:
                    print(f"     🎬 Da cercare su YouTube")
                print()

        # Frasi visuali → Artlist (max 5 total)
        visual = segment.get('visual_sentences', [])
        if visual:
            print(f"  🟢 FRASI VISUALI ({len(visual)}) → ARTLIST:")
            for sentence in visual:
                print(f"     💬 \"{sentence}\"")

                artlist_clip = next((a for a in artlist_results if a.get('sentence') == sentence), None)
                if artlist_clip:
                    print(f"     🎵 Artlist: {artlist_clip.get('name', 'N/A')}")
                    print(f"     📁 Category: {artlist_clip.get('category', 'N/A')}")
                    print(f"     ⏱️  Durata: {artlist_clip.get('duration', 0)}s")
                    print(f"     🔗 {artlist_clip.get('url', '')}")
                    total_artlist += 1
                else:
                    print(f"     🎬 Da cercare su Artlist")
                print()

        print()

    print(f"{'═' * 100}")
    print(f"  ✅ RIASSUNTO ASSOCIAZIONE CLIP")
    print(f"{'═' * 100}")
    print(f"  🎬 Clip Drive (frasi importanti): {total_drive}")
    print(f"  📺 Clip Stock YouTube (resto): {total_youtube}")
    print(f"  🎵 Clip Artlist (frasi visuali): {total_artlist}")
    print(f"  📊 TOTALE CLIP ASSOCIATE: {total_drive + total_youtube + total_artlist}")
    print(f"{'═' * 100}")

def save_output(topic: str, script: Dict, drive_results: List[Dict], artlist_results: List[Dict], youtube_results: List[Dict]):
    """Salva output JSON"""
    import os

    output = {
        'topic': topic,
        'title': script.get('title', ''),
        'segments': script.get('segments', []),
        'clip_associations': {
            'drive_clips': drive_results,
            'artlist_clips': artlist_results,
            'youtube_clips': youtube_results
        },
        'totals': {
            'drive_clips': len(drive_results),
            'artlist_clips': len(artlist_results),
            'youtube_clips': len(youtube_results),
            'total_clips': len(drive_results) + len(artlist_results) + len(youtube_results)
        }
    }

    output_dir = "/home/pierone/Pyt/VeloxEditing/refactored/data"
    os.makedirs(output_dir, exist_ok=True)

    filename = f"{output_dir}/script_with_clips_{topic.lower().replace(' ', '_')}.json"

    with open(filename, 'w', encoding='utf-8') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)

    print(f"\n💾 Output salvato: {filename}")
    return filename

def main():
    import argparse

    parser = argparse.ArgumentParser(description='Script Generator with Smart Clip Association')
    parser.add_argument('--topic', type=str, help='Topic per generare script', required=True)
    parser.add_argument('--duration', type=int, default=90, help='Durata totale in secondi (default: 90)')
    parser.add_argument('--artlist-count', type=int, default=5, help='Numero frasi per Artlist (default: 5)')
    parser.add_argument('--json', action='store_true', help='Output solo JSON')
    parser.add_argument('--no-ollama', action='store_true', help='Non usare Ollama (usa fallback)')

    args = parser.parse_args()

    # Load clips
    if not args.json:
        print("⏳ Caricamento clip da Drive...")
    drive_clips = load_drive_clips()
    if not args.json:
        print(f"✅ {len(drive_clips)} clip caricate da Drive")

    if not args.json:
        print("⏳ Caricamento clip da Artlist...")
    artlist_clips = load_artlist_clips()
    if not args.json:
        print(f"✅ {len(artlist_clips)} clip caricate da Artlist")

    # Generate script
    if not args.json:
        print(f"\n🤖 Generazione script con Ollama (topic: {args.topic})...")

    if args.no_ollama:
        script = generate_fallback_script(args.topic, args.duration // 20)
    else:
        script = generate_script_with_ollama(args.topic, args.duration)

    if not args.json:
        print(f"✅ Script generato: {script.get('title', 'N/A')}")
        print(f"   Segmenti: {len(script.get('segments', []))}")
        print()

    # Collect all sentences by type
    all_important = []
    all_normal = []
    all_visual = []

    for segment in script.get('segments', []):
        all_important.extend(segment.get('important_sentences', []))
        all_normal.extend(segment.get('normal_sentences', []))
        all_visual.extend(segment.get('visual_sentences', []))

    # Find matching clips
    if not args.json:
        print("🔍 Ricerca clip Drive per frasi importanti...")
    drive_results = find_drive_clips_for_sentences(all_important, drive_clips, args.topic)
    if not args.json:
        print(f"✅ {len(drive_results)} clip Drive trovate")

    if not args.json:
        print("🔍 Ricerca clip Artlist per frasi visuali...")
    artlist_results = find_artlist_clips_for_sentences(all_visual, artlist_clips, args.artlist_count)
    if not args.json:
        print(f"✅ {len(artlist_results)} clip Artlist trovate")

    if not args.json:
        print("🔍 Ricerca clip YouTube per frasi normali...")
    youtube_results = search_youtube_clips(all_normal, count_per_sentence=2)
    if not args.json:
        print(f"✅ {len(youtube_results)} clip YouTube trovate")

    print()

    # Output
    if args.json:
        output = {
            'title': script.get('title'),
            'segments': [{
                'index': s.get('index'),
                'full_text': s.get('full_text'),
                'important_clips': [d for d in drive_results if d.get('sentence') in s.get('important_sentences', [])],
                'stock_clips': [y for y in youtube_results if y.get('sentence') in s.get('normal_sentences', [])],
                'artlist_clips': [a for a in artlist_results if a.get('sentence') in s.get('visual_sentences', [])]
            } for s in script.get('segments', [])]
        }
        print(json.dumps(output, indent=2, ensure_ascii=False))
    else:
        display_full_script_with_clips(args.topic, script, drive_results, artlist_results, youtube_results)
        save_output(args.topic, script, drive_results, artlist_results, youtube_results)

if __name__ == '__main__':
    main()
