#!/usr/bin/env python3
"""
Full Entity Extraction + Script Generation + Smart Clip Association

Genera script COMPLETO con Ollama, estrae TUTTE le entità, e associa:
1. FRASI IMPORTANTI → Clip Drive (match per entità)
2. ~5 FRASI → Artlist clips
3. RESTO → Clip Stock YouTube

Entità estratte:
- frasi_importanti: Frasi complete significative
- nomi_speciali: Nomi propri, brand, prodotti
- parole_importanti: Keywords principali
- entity_senza_testo: Entità con immagini/icone associate

Uso:
  python3 scripts/full_entity_script.py --topic "Andrew Tate" --duration 90
  python3 scripts/full_entity_script.py --topic "Elvis Presley" --duration 120 --json
"""

import json
import re
import subprocess
import requests
import os
import sqlite3
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
        conn = sqlite3.connect(ARTLIST_DB)
        cursor = conn.cursor()
        cursor.execute("""
            SELECT vl.file_name, vl.url, vl.duration, c.name as category, vl.download_path
            FROM video_links vl
            LEFT JOIN categories c ON vl.category_id = c.id
            WHERE vl.source = 'artlist'
            LIMIT 300
        """)
        rows = cursor.fetchall()
        conn.close()

        clips = []
        for row in rows:
            # Usa download_path se file_name è None
            name = row[0] or (row[4].split('/')[-1] if row[4] else 'Artlist Clip')
            clips.append({
                'name': name,
                'url': row[1] or '',
                'duration': row[2] or 0,
                'category': row[3] or 'Artlist',
                'download_path': row[4] or ''
            })
        return clips
    except Exception as e:
        return []

def generate_script_with_ollama(topic: str, duration: int) -> Dict:
    """Genera script con Ollama"""
    num_segments = max(3, duration // 20)

    prompt = f"""Crea uno script video COMPLETO su "{topic}".
Durata: {duration} secondi ({num_segments} segmenti da ~20s, ~50 parole ciascuno).

Per ogni segmento fornisci:
- full_text: Testo completo del segmento
- important_sentences: Frasi con INFORMAZIONI CHIAVE (fatti, dati, citazioni)
- normal_sentences: Frasi di COLLEGAMENTO (transizioni, intro)
- visual_sentences: Frasi che richiedono AZIONI/EMOZIONI/PERSONAGGI visivi

Topic: {topic}

JSON:
{{
  "title": "...",
  "segments": [
    {{
      "index": 1,
      "full_text": "...",
      "important_sentences": ["...", "..."],
      "normal_sentences": ["...", "..."],
      "visual_sentences": ["...", "..."]
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
                "options": {"temperature": 0.7, "num_predict": 2000}
            },
            timeout=90
        )

        if response.status_code == 200:
            result = response.json()
            text = result.get('response', '')

            # Estrai JSON
            json_start = text.find('{')
            json_end = text.rfind('}') + 1

            if json_start >= 0 and json_end > json_start:
                return json.loads(text[json_start:json_end])

    except Exception as e:
        print(f"⚠️  Ollama error: {e}")

    # Fallback
    return generate_fallback_script(topic, num_segments)

def generate_fallback_script(topic: str, num_segments: int) -> Dict:
    """Fallback script"""
    segments = []

    templates = [
        {
            "full_text": f"{topic} ha rivoluzionato il suo campo. Molti non conoscono la vera storia dietro il successo.",
            "important_sentences": [f"{topic} ha rivoluzionato il suo campo"],
            "normal_sentences": ["Molti non conoscono la vera storia"],
            "visual_sentences": [f"{topic} in azione"]
        },
        {
            "full_text": "I momenti più iconici hanno lasciato il segno. Le testimonianze parlano di un impatto enorme.",
            "important_sentences": ["I momenti più iconici hanno lasciato il segno"],
            "normal_sentences": ["Le testimonianze parlano di un impatto enorme"],
            "visual_sentences": ["Momenti iconici"]
        },
        {
            "full_text": "Le conseguenze si sentono ancora oggi. Il futuro sarà influenzato da queste scelte.",
            "important_sentences": ["Le conseguenze si sentono ancora oggi"],
            "normal_sentences": ["Il futuro sarà influenzato"],
            "visual_sentences": ["Impatto sul futuro"]
        },
    ]

    for i in range(num_segments):
        t = templates[i % len(templates)]
        segments.append({"index": i + 1, **t})

    return {"title": f"Tutto su {topic}", "segments": segments}

def extract_all_entities_with_ollama(script: Dict, topic: str) -> Dict:
    """Estrae TUTTE le entità con Ollama"""

    # Combina tutto il testo
    all_text = ' '.join([s.get('full_text', '') for s in script.get('segments', [])])

    prompt = f"""Estrai TUTTE le entità da questo testo su "{topic}".

Testo:
{all_text}

Categorizza in 4 gruppi:

1. frasi_importanti: Frasi complete con informazioni chiave (fatti, dati, citazioni)
2. nomi_speciali: Nomi propri, brand, prodotti, luoghi, persone
3. parole_importanti: Keywords, concetti principali (max 15)
4. entity_senza_testo: Entità visive (icone, immagini, simboli) come dizionario {{nome: descrizione}}

JSON:
{{
  "frasi_importanti": ["...", "..."],
  "nomi_speciali": ["...", "..."],
  "parole_importanti": ["...", "..."],
  "entity_senza_text": {{"Nome Entità": "descrizione visuale"}}
}}"""

    try:
        response = requests.post(
            f"{OLLAMA_URL}/api/generate",
            json={
                "model": "gemma3:4b",
                "prompt": prompt,
                "stream": False,
                "options": {"temperature": 0.5, "num_predict": 1500}
            },
            timeout=60
        )

        if response.status_code == 200:
            result = response.json()
            text = result.get('response', '')

            json_start = text.find('{')
            json_end = text.rfind('}') + 1

            if json_start >= 0 and json_end > json_start:
                return json.loads(text[json_start:json_end])

    except:
        pass

    # Fallback: estrazione manuale
    words = re.findall(r'\b\w+\b', all_text.lower())
    stop_words = {'the', 'a', 'an', 'is', 'are', 'was', 'were', 'of', 'in', 'on', 'at', 'to', 'for', 'with', 'by', 'from', 'e', 'il', 'lo', 'la', 'i', 'gli', 'le', 'un', 'uno', 'una', 'di', 'a', 'da', 'in', 'con', 'su', 'per', 'tra', 'fra', 'che', 'del', 'della', 'delle', 'dei', 'degli', 'non', 'sono', 'come', 'molto', 'essere', 'avere'}

    importanti = []
    for seg in script.get('segments', []):
        importanti.extend(seg.get('important_sentences', []))

    nomi = list(set([w.title() for w in words if w not in stop_words and len(w) > 3 and w[0].isupper()]))
    parole = list(set([w for w in words if w not in stop_words and len(w) > 4]))[:15]

    return {
        "frasi_importanti": importanti,
        "nomi_speciali": nomi[:10],
        "parole_importanti": parole,
        "entity_senza_text": {topic: "Immagine principale del topic"}
    }

def match_drive_clips_by_entities(entities: Dict, drive_clips: List[Dict]) -> List[Dict]:
    """Matcha clip Drive per OGNI entità - PRIORITÀ ASSOLUTA"""
    results = []

    nomi = entities.get('nomi_speciali', [])
    parole = entities.get('parole_importanti', [])
    frasi = entities.get('frasi_importanti', [])
    
    # Combina tutte le entità
    all_entities = nomi + parole
    all_text = ' '.join(frasi).lower()

    for entity in all_entities:
        entity_lower = entity.lower()
        best_clip = None
        best_score = 0

        for clip in drive_clips:
            name = clip.get('name', '').lower()
            folder = clip.get('folder_name', '').lower()
            tags = ' '.join(clip.get('tags', [])).lower()

            score = 0

            # Priority: folder > name > tags > content in sentences
            if entity_lower in folder:
                score = 100
            elif entity_lower in name:
                score = 90
            elif entity_lower in all_text:
                score = 80
            else:
                name_score = fuzz.partial_ratio(entity_lower, name)
                folder_score = fuzz.partial_ratio(entity_lower, folder)
                tag_score = fuzz.partial_ratio(entity_lower, tags)
                score = max(name_score * 0.7, folder_score * 0.9, tag_score * 0.6)

            if score > best_score and score > 40:
                best_score = score
                best_clip = {
                    **clip,
                    'entity': entity,
                    'match_score': score
                }

        if best_clip:
            results.append(best_clip)

    # Deduplica per clip, mantieni score più alto
    seen = {}
    for r in results:
        clip_id = r['id']
        if clip_id not in seen or r['match_score'] > seen[clip_id]['match_score']:
            seen[clip_id] = r

    return sorted(seen.values(), key=lambda x: -x['match_score'])

def match_artlist_clips(entities: Dict, artlist_clips: List[Dict], count: int = 5) -> List[Dict]:
    """Seleziona ~5 clip Artlist"""
    if not artlist_clips:
        return []

    parole = entities.get('parole_importanti', [])[:count]
    results = []

    for i, parola in enumerate(parole):
        clip_idx = (i * 3) % len(artlist_clips)
        clip = artlist_clips[clip_idx]
        results.append({
            **clip,
            'entity': parola,
            'source': 'artlist'
        })

    return results[:count]

def search_youtube_for_sentences(sentences: List[str]) -> List[Dict]:
    """Cerca clip YouTube per frasi normali"""
    results = []

    for sentence in sentences:
        words = re.findall(r'\b\w+\b', sentence.lower())
        keywords = [w for w in words if len(w) > 4 and w not in ['questo', 'quella', 'della', 'sono', 'come', 'molto']]

        if not keywords:
            continue

        query = ' '.join(keywords[:3])

        try:
            cmd = subprocess.run(
                ["/home/pierone/venv/bin/yt-dlp", f"ytsearch2:{query}", "--dump-json", "--flat-playlist", "--no-warnings"],
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

def display_complete_results(topic: str, script: Dict, entities: Dict, segment_clips: List[Dict], drive_matches: List[Dict], artlist_clips: List[Dict]):
    """Stampa risultati completi"""

    print("=" * 120)
    print(f"  📝 SCRIPT COMPLETO CON ENTITY EXTRACTION + CLIP ASSOCIATION")
    print(f"  Topic: {topic}")
    print(f"  Titolo: {script.get('title', 'N/A')}")
    print("=" * 120)
    print()

    # === ENTITÀ ESTRATTE ===
    print(f"{'═' * 120}")
    print(f"  🔍 ENTITÀ ESTRATTE (TUTTE)")
    print(f"{'═' * 120}")
    print()

    frasi = entities.get('frasi_importanti', [])
    if frasi:
        print(f"  📌 FRASI IMPORTANTI ({len(frasi)}):")
        for i, f in enumerate(frasi, 1):
            print(f"     {i}. {f}")
        print()

    nomi = entities.get('nomi_speciali', [])
    if nomi:
        print(f"  👤 NOMI SPECIALI ({len(nomi)}):")
        for i, n in enumerate(nomi, 1):
            print(f"     {i}. {n}")
        print()

    parole = entities.get('parole_importanti', [])
    if parole:
        print(f"  🔑 PAROLE IMPORTANTI ({len(parole)}):")
        for i, p in enumerate(parole, 1):
            print(f"     {i}. {p}")
        print()

    entity_senza = entities.get('entity_senza_text', {})
    if entity_senza:
        print(f"  🎨 ENTITY SENZA TESTO ({len(entity_senza)}):")
        for name, desc in entity_senza.items():
            print(f"     • {name}: {desc}")
        print()

    # === SCRIPT CON CLIP ===
    print(f"{'═' * 120}")
    print(f"  📜 SCRIPT CON CLIP ASSOCIATE (PRIORITÀ: Drive → Artlist)")
    print(f"{'═' * 120}")
    print()

    total_drive = 0
    total_artlist = 0
    total_no_clip = 0

    for i, segment in enumerate(script.get('segments', [])):
        idx = segment.get('index', 0)
        full_text = segment.get('full_text', '')
        clips = segment_clips[i] if i < len(segment_clips) else {'important': [], 'normal': [], 'visual': []}

        print(f"{'─' * 120}")
        print(f"  📍 SEGMENTO {idx}")
        print(f"{'─' * 120}")
        print(f"\n  📜 {full_text}\n")

        # Frasi importanti → Drive clips
        important = clips.get('important', [])
        if important:
            print(f"  🔴 FRASI IMPORTANTI → CLIP DRIVE ({len([c for c in important if c.get('clip')])}/{len(important)}):")
            for item in important:
                sentence = item.get('sentence', '')
                clip = item if item.get('id') else None
                
                print(f"     💬 \"{sentence}\"")
                if clip:
                    print(f"     🎬 {clip.get('name', 'N/A')}")
                    print(f"     📁 {clip.get('folder_name', 'N/A')}")
                    print(f"     🎯 Entity: '{clip.get('entity', '')}' | Match: {clip.get('match_score', 0):.0f}%")
                    print(f"     🔗 {clip.get('drive_url', '')}")
                    total_drive += 1
                else:
                    print(f"     ⚠️  Nessuna clip trovata")
                    total_no_clip += 1
                print()

        # Frasi normali → Drive clips
        normal = clips.get('normal', [])
        if normal:
            print(f"  🟡 FRASI NORMALI → CLIP DRIVE ({len([c for c in normal if c.get('clip')])}/{len(normal)}):")
            for item in normal:
                sentence = item.get('sentence', '')
                clip = item if item.get('id') else None
                
                print(f"     💬 \"{sentence}\"")
                if clip:
                    print(f"     🎬 {clip.get('name', 'N/A')}")
                    print(f"     📁 {clip.get('folder_name', 'N/A')}")
                    print(f"     🎯 Entity: '{clip.get('entity', '')}' | Match: {clip.get('match_score', 0):.0f}%")
                    print(f"     🔗 {clip.get('drive_url', '')}")
                    total_drive += 1
                else:
                    print(f"     ⚠️  Nessuna clip trovata")
                    total_no_clip += 1
                print()

        # Frasi visuali → Artlist
        visual = clips.get('visual', [])
        if visual:
            print(f"  🟢 FRASI VISUALI → ARTLIST ({len([c for c in visual if c.get('name')])}/{len(visual)}):")
            for item in visual:
                sentence = item.get('sentence', '')
                clip = item if item.get('name') else None
                
                print(f"     💬 \"{sentence}\"")
                if clip and clip.get('name'):
                    print(f"     🎵 {clip.get('name', 'N/A')}")
                    print(f"     📁 {clip.get('category', 'Artlist')} | ⏱️ {clip.get('duration', 0)/1000:.1f}s")
                    print(f"     🔗 {clip.get('url', '')}")
                    total_artlist += 1
                else:
                    print(f"     🎬 Da cercare su Artlist")
                    total_no_clip += 1
                print()

        print()

    # === RIASSUNTO ===
    print(f"{'═' * 120}")
    print(f"  ✅ RIASSUNTO FINALE")
    print(f"{'═' * 120}")
    print(f"  🔍 Entità estratte:")
    print(f"     • Frasi importanti: {len(frasi)}")
    print(f"     • Nomi speciali: {len(nomi)}")
    print(f"     • Parole importanti: {len(parole)}")
    print(f"     • Entity senza testo: {len(entity_senza)}")
    print(f"  🎬 Clip associate:")
    print(f"     • Drive (tutte le frasi): {total_drive}")
    print(f"     • Artlist (~5 frasi visuali): {total_artlist}")
    print(f"     • Senza clip: {total_no_clip}")
    print(f"     • TOTALE: {total_drive + total_artlist}")
    print(f"{'═' * 120}")

def save_json(topic, script, entities, segment_clips, drive_matches, artlist_clips):
    """Salva JSON"""
    output_dir = "/home/pierone/Pyt/VeloxEditing/refactored/data"
    os.makedirs(output_dir, exist_ok=True)

    output = {
        'topic': topic,
        'title': script.get('title'),
        'entities': entities,
        'segments': [{
            'index': segment_clips[i].get('index', script.get('segments', [{}])[i].get('index')),
            'full_text': script.get('segments', [{}])[i].get('full_text'),
            'important_clips': segment_clips[i].get('important', []),
            'normal_clips': segment_clips[i].get('normal', []),
            'visual_clips': segment_clips[i].get('visual', [])
        } for i in range(len(segment_clips))],
        'totals': {
            'drive_clips': sum(1 for seg in segment_clips for c in seg.get('important', []) + seg.get('normal', []) if c.get('id')),
            'artlist_clips': sum(1 for seg in segment_clips for c in seg.get('visual', []) if c.get('name')),
            'total': sum(1 for seg in segment_clips for c in seg.get('important', []) + seg.get('normal', []) + seg.get('visual', []) if c.get('id') or c.get('name'))
        }
    }

    filename = f"{output_dir}/full_entity_script_{topic.lower().replace(' ', '_')}.json"
    with open(filename, 'w', encoding='utf-8') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)

    print(f"\n💾 Output JSON: {filename}")

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--topic', required=True)
    parser.add_argument('--duration', type=int, default=90)
    parser.add_argument('--artlist-count', type=int, default=5)
    parser.add_argument('--json', action='store_true')
    parser.add_argument('--no-ollama', action='store_true')

    args = parser.parse_args()

    # Load
    if not args.json:
        print("⏳ Caricamento clip...")
    drive_clips = load_drive_clips()
    artlist_clips = load_artlist_clips()
    if not args.json:
        print(f"✅ Drive: {len(drive_clips)} | Artlist: {len(artlist_clips)}")

    # Generate script
    if not args.json:
        print(f"\n🤖 Generazione script ({args.topic})...")
    script = generate_fallback_script(args.topic, args.duration // 20) if args.no_ollama else generate_script_with_ollama(args.topic, args.duration)
    if not args.json:
        print(f"✅ {script.get('title')}")

    # Extract entities
    if not args.json:
        print(f"\n🔍 Estrazione entità...")
    entities = extract_all_entities_with_ollama(script, args.topic)
    if not args.json:
        print(f"✅ Frasi: {len(entities.get('frasi_importanti', []))} | Nomi: {len(entities.get('nomi_speciali', []))} | Parole: {len(entities.get('parole_importanti', []))}")

    # Match clips - PRIORITÀ: Drive > Artlist > YouTube
    if not args.json:
        print(f"\n🎬 Associazione clip...")
    
    # 1. Matcha TUTTE le clip da Drive per entità
    drive_matches = match_drive_clips_by_entities(entities, drive_clips)
    if not args.json:
        print(f"✅ Drive clips trovate: {len(drive_matches)}")

    # 2. Prendi le clip Drive per ciascun segmento
    # Per ogni segmento, assegna le clip Drive disponibili
    all_clips_for_segments = []
    drive_idx = 0
    
    for segment in script.get('segments', []):
        segment_clips = {
            'important': [],
            'normal': [],
            'visual': []
        }
        
        # Assegna clip Drive a FRASI IMPORTANTI
        for sentence in segment.get('important_sentences', []):
            if drive_idx < len(drive_matches):
                clip = drive_matches[drive_idx % len(drive_matches)]
                segment_clips['important'].append({**clip, 'sentence': sentence})
                drive_idx += 1
            else:
                segment_clips['important'].append({'sentence': sentence, 'clip': None})
        
        # Assegna clip Drive a FRASI NORMALI (stesse entità, clip diverse)
        for sentence in segment.get('normal_sentences', []):
            if drive_idx < len(drive_matches):
                clip = drive_matches[drive_idx % len(drive_matches)]
                segment_clips['normal'].append({**clip, 'sentence': sentence})
                drive_idx += 1
            else:
                segment_clips['normal'].append({'sentence': sentence, 'clip': None})
        
        # Assegna clip Artlist a FRASI VISUALI
        visual_sentences = segment.get('visual_sentences', [])
        for i, sentence in enumerate(visual_sentences):
            artlist_idx = (i + (segment.get('index', 0) - 1) * 2) % len(artlist_clips) if artlist_clips else 0
            if artlist_clips and artlist_idx < len(artlist_clips):
                clip = artlist_clips[artlist_idx]
                segment_clips['visual'].append({**clip, 'sentence': sentence})
            else:
                segment_clips['visual'].append({'sentence': sentence, 'clip': None})
        
        all_clips_for_segments.append(segment_clips)
    
    if not args.json:
        print(f"✅ Artlist clips disponibili: {len(artlist_clips)}")
        print(f"✅ Clip associate a {len(all_clips_for_segments)} segmenti")
        print()

    # Output
    if args.json:
        output = {
            'title': script.get('title'),
            'entities': entities,
            'segments': []
        }
        
        for i, segment in enumerate(script.get('segments', [])):
            seg_data = {
                'index': segment.get('index'),
                'full_text': segment.get('full_text'),
                'important_clips': all_clips_for_segments[i]['important'],
                'normal_clips': all_clips_for_segments[i]['normal'],
                'visual_clips': all_clips_for_segments[i]['visual']
            }
            output['segments'].append(seg_data)
        
        print(json.dumps(output, indent=2, ensure_ascii=False))
    else:
        display_complete_results(args.topic, script, entities, all_clips_for_segments, drive_matches, artlist_clips)
        save_json(args.topic, script, entities, all_clips_for_segments, drive_matches, artlist_clips)

if __name__ == '__main__':
    main()
