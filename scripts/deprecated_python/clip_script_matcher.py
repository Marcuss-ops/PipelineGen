#!/usr/bin/env python3
"""
Clip-to-Script Matcher - Trova e collega clip da Drive a segmenti di script

Uso:
  python3 scripts/clip_script_matcher.py --topic "Andrew Tate boxing"
  python3 scripts/clip_script_matcher.py --topic "Elon Musk Tesla"
  python3 scripts/clip_script_matcher.py --topic "Elvis Presley" --segments 4
"""

import json
import argparse
import re
from collections import defaultdict
from typing import List, Dict, Tuple
from rapidfuzz import fuzz

# Percorsi
CLIP_INDEX = "/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/data/clip_index.json"
ARTLIST_DB = "/home/pierone/Pyt/VeloxEditing/refactored/src/node-scraper/artlist_videos.db"

def load_drive_clips():
    """Carica tutte le clip da Drive index"""
    with open(CLIP_INDEX, 'r') as f:
        data = json.load(f)

    clips = data.get('clips', [])
    folders = data.get('folders', [])

    # Mappa folder_id → nome
    folder_names = {}
    for fld in folders:
        folder_names[fld.get('id', '')] = fld.get('name', 'Unknown')

    # Arricchisci clip con nome folder completo
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

def extract_entities_from_topic(topic: str) -> List[str]:
    """Estrae entità/keywords dal topic"""
    # Rimuovi stop words
    stop_words = {'the', 'a', 'an', 'is', 'are', 'was', 'were', 'of', 'in', 'on', 'at', 'to', 'for', 'with', 'by', 'from', 'e', 'il', 'lo', 'la', 'i', 'gli', 'le', 'un', 'uno', 'una', 'di', 'a', 'da', 'in', 'con', 'su', 'per', 'tra', 'fra'}

    words = re.findall(r'\b\w+\b', topic.lower())
    entities = [w for w in words if w not in stop_words and len(w) > 2]
    return list(set(entities))

def find_matching_clips(clips: List[Dict], entities: List[str], top_k: int = 15) -> List[Dict]:
    """Trova clip che matchano con le entità usando fuzzy matching"""
    scored_clips = []

    for clip in clips:
        name = clip.get('name', '').lower()
        folder = clip.get('folder_name', '').lower()
        tags = ' '.join(clip.get('tags', [])).lower()

        best_score = 0
        best_match = ""
        match_priority = 3  # 1=exact folder, 2=exact name, 3=fuzzy

        for entity in entities:
            entity_lower = entity.lower()

            # Priority 1: Exact match on folder name (BEST)
            if entity_lower in folder or folder in entity_lower:
                score = 100
                if score > best_score:
                    best_score = score
                    best_match = entity
                    match_priority = 1

            # Priority 2: Exact match on clip name
            elif entity_lower in name or name in entity_lower:
                score = 95
                if score > best_score:
                    best_score = score
                    best_match = entity
                    match_priority = 2

            # Priority 3: Fuzzy matching
            else:
                name_score = fuzz.partial_ratio(entity_lower, name)
                folder_score = fuzz.partial_ratio(entity_lower, folder)
                tag_score = fuzz.partial_ratio(entity_lower, tags)

                # Weighted combination
                clip_score = max(name_score * 0.7, folder_score * 0.8, tag_score * 0.6)

                if clip_score > best_score and clip_score > 40:
                    best_score = clip_score
                    best_match = entity
                    match_priority = 3

        if best_score > 40:
            scored_clips.append({
                **clip,
                'match_score': best_score,
                'best_match': best_match,
                'match_priority': match_priority
            })

    # Sort: priority first (lower is better), then score (higher is better)
    scored_clips.sort(key=lambda x: (x['match_priority'], -x['match_score']))
    return scored_clips[:top_k]

def generate_script_segments(topic: str, num_segments: int = 4) -> List[Dict]:
    """Genera segmenti di script basati sul topic"""
    entities = extract_entities_from_topic(topic)

    # Template base per segmenti
    segment_templates = [
        f"Introduzione a {topic}. {', '.join(entities[:2]).title()} sta cambiando il mondo in cui viviamo.",
        f"La storia di {entities[0].title() if entities else topic} è affascinante. Molti non sanno che...",
        f"I momenti più iconici di {topic}. {entities[1].title() if len(entities) > 1 else 'Questo'} ha lasciato il segno.",
        f"Impatto e futuro di {topic}. {entities[2].title() if len(entities) > 2 else 'Le conseguenze'} saranno enormi.",
        f"Conclusioni su {topic}. {entities[0].title() if entities else 'Questo'} continuerà a influenzare il futuro.",
    ]

    segments = []
    for i in range(num_segments):
        template = segment_templates[i % len(segment_templates)]
        segments.append({
            'segment_index': i + 1,
            'text': template,
            'target_duration': 20,  # secondi
            'entities': entities
        })

    return segments

def match_clips_to_segments(segments: List[Dict], all_clips: List[Dict], top_per_segment: int = 3) -> List[Dict]:
    """Collega clip ai segmenti in base alle entità"""
    for segment in segments:
        entities = segment.get('entities', [])

        # Trova clip per questo segmento
        matching = find_matching_clips(all_clips, entities, top_k=top_per_segment * 2)

        # Prendi le migliori top_per_segment
        segment['clips'] = matching[:top_per_segment]
        segment['total_matching_clips'] = len(matching)

        # Calcola score medio
        if matching:
            segment['avg_match_score'] = sum(c['match_score'] for c in matching[:top_per_segment]) / min(len(matching), top_per_segment)
        else:
            segment['avg_match_score'] = 0

    return segments

def display_results(topic: str, segments: List[Dict], artlist_clips: List[Dict]):
    """Stampa risultati formattati"""
    print("=" * 80)
    print(f"  📝 CLIP-TO-SCRIPT MATCHER")
    print(f"  Topic: {topic}")
    print("=" * 80)
    print()

    total_clips_used = 0

    for segment in segments:
        idx = segment['segment_index']
        text = segment['text']
        clips = segment.get('clips', [])

        print(f"{'─' * 80}")
        print(f"  📍 SEGMENTO {idx}")
        print(f"{'─' * 80}")
        print(f"  📜 Testo: {text}")
        print(f"  ⏱️  Durata target: {segment['target_duration']}s")
        print(f"  🎯 Match score medio: {segment.get('avg_match_score', 0):.1f}%")
        print(f"  📊 Clip trovate: {segment.get('total_matching_clips', 0)}")
        print()

        if clips:
            print(f"  🎬 CLIP DRIVE CONSIGLIATE ({len(clips)}):")
            for j, clip in enumerate(clips, 1):
                name = clip.get('name', 'Unknown')
                folder = clip.get('folder_name', 'Unknown')
                score = clip.get('match_score', 0)
                match_entity = clip.get('best_match', '')
                drive_url = clip.get('drive_url', '')

                print(f"    {j}. {name}")
                print(f"       📁 {folder}")
                print(f"       🎯 Match: {score:.0f}% (entità: '{match_entity}')")
                print(f"       🔗 {drive_url}")
                print()

            total_clips_used += len(clips)

        # Artlist clips (opzionale)
        if artlist_clips:
            # Prendi 2-3 clip artlist random come fallback
            artlist_for_segment = artlist_clips[(idx-1)*3:idx*3]
            if artlist_for_segment:
                print(f"  🎵 CLIP ARTLIST DI SUPPORTO ({len(artlist_for_segment)}):")
                for j, al_clip in enumerate(artlist_for_segment[:3], 1):
                    print(f"    {j}. {al_clip.get('name', 'Unknown')}")
                    print(f"       📁 {al_clip.get('category', 'Artlist')}")
                    print(f"       ⏱️  {al_clip.get('duration', 0)}s")
                    print(f"       🔗 {al_clip.get('url', '')}")
                    print()

        print()

    print("=" * 80)
    print(f"  ✅ RIASSUNTO")
    print(f"{'=' * 80}")
    print(f"  Segmenti generati: {len(segments)}")
    print(f"  Clip Drive usate: {total_clips_used}")
    print(f"  Clip Artlist disponibili: {len(artlist_clips)}")
    print(f"{'=' * 80}")

def save_output(topic: str, segments: List[Dict], artlist_clips: List[Dict]):
    """Salva output JSON"""
    import os

    output = {
        'topic': topic,
        'segments': segments,
        'total_drive_clips_used': sum(len(s.get('clips', [])) for s in segments),
        'total_artlist_clips': len(artlist_clips)
    }

    output_dir = "/home/pierone/Pyt/VeloxEditing/refactored/data"
    os.makedirs(output_dir, exist_ok=True)

    filename = f"{output_dir}/clip_script_match_{topic.lower().replace(' ', '_')}.json"

    with open(filename, 'w', encoding='utf-8') as f:
        json.dump(output, f, indent=2, ensure_ascii=False)

    print(f"\n💾 Output salvato: {filename}")
    return filename

def main():
    parser = argparse.ArgumentParser(description='Clip-to-Script Matcher')
    parser.add_argument('--topic', type=str, help='Topic per generare script', required=True)
    parser.add_argument('--segments', type=int, default=4, help='Numero segmenti (default: 4)')
    parser.add_argument('--clips-per-segment', type=int, default=3, help='Clip per segmento (default: 3)')
    parser.add_argument('--top-k', type=int, default=15, help='Top K clip da cercare (default: 15)')
    parser.add_argument('--no-artlist', action='store_true', help='Non usare Artlist')
    parser.add_argument('--json', action='store_true', help='Output solo JSON')

    args = parser.parse_args()

    # Load clips
    if not args.json:
        print("⏳ Caricamento clip da Drive...")
    drive_clips = load_drive_clips()
    if not args.json:
        print(f"✅ {len(drive_clips)} clip caricate da Drive")

    artlist_clips = []
    if not args.no_artlist:
        if not args.json:
            print("⏳ Caricamento clip da Artlist...")
        artlist_clips = load_artlist_clips()
        if not args.json:
            print(f"✅ {len(artlist_clips)} clip caricate da Artlist")

    # Generate segments
    segments = generate_script_segments(args.topic, args.segments)

    # Match clips to segments
    segments = match_clips_to_segments(segments, drive_clips, args.clips_per_segment)

    # Output
    if args.json:
        output = {
            'topic': args.topic,
            'segments': [{
                'index': s['segment_index'],
                'text': s['text'],
                'clips': [{
                    'name': c.get('name'),
                    'folder': c.get('folder_name'),
                    'match_score': c.get('match_score'),
                    'drive_url': c.get('drive_url')
                } for c in s.get('clips', [])]
            } for s in segments]
        }
        print(json.dumps(output, indent=2, ensure_ascii=False))
    else:
        display_results(args.topic, segments, artlist_clips)
        save_output(args.topic, segments, artlist_clips)

if __name__ == '__main__':
    main()
