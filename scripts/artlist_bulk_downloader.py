#!/usr/bin/env python3
import os, re, json, sqlite3, subprocess, tempfile, hashlib, time, argparse
from datetime import datetime
from pathlib import Path
from typing import List, Dict, Optional, Tuple, Set
from itertools import islice
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload
from google.auth.transport.requests import Request

SCRIPT_DIR = Path(__file__).parent.parent
ARTLIST_DB = SCRIPT_DIR / "src/node-scraper/artlist_videos.db"
TOKEN_FILE = SCRIPT_DIR / "src/go-master/token.json"
TRACKING_FILE = SCRIPT_DIR / "data/artlist_uploaded.json"
INDEX_FILE = SCRIPT_DIR / "data/artlist_stock_index.json"
TEMP_DIR = Path("/tmp/artlist_downloads")
DEFAULT_CLIPS_PER_TERM = 15
STOCK_FOLDER_ID = "1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S"

GENERIC_WORDS = {'the','a','an','and','or','but','in','on','at','to','for','of','with','by','from','is','are','was','were','be','been','being','have','has','had','do','does','did','will','would','could','should','may','might','must','shall','can','i','me','my','myself','we','our','ours','ourselves','you','your','yours','yourself','yourselves','he','him','his','himself','she','her','hers','herself','it','its','itself','they','them','their','theirs','themselves','what','which','who','whom','this','that','these','those','am','is','are','was','were','being','have','has','had','having','do','does','did','doing','a','an','the','and','but','if','or','because','as','until','while','of','at','by','for','with','about','against','between','through','during','before','after','above','below','to','from','up','down','in','out','on','off','over','under','again','further','then','once','here','there','when','where','why','how','all','both','each','few','more','most','other','some','such','no','nor','not','only','own','same','so','than','too','very','s','t','just','now','also','back','further'}

ENTITY_PRIORITY = {'proper_noun':1,'keyword':2,'phrase':3}

CATEGORY_MAPPING = {"sunset":"Nature","ocean":"Nature","mountain":"Nature","forest":"Nature","river":"Nature","waterfall":"Nature","beach":"Nature","sky":"Nature","cloud":"Nature","flower":"Nature","tree":"Nature","garden":"Nature","wildlife":"Nature","autumn":"Nature","landscape":"Nature","nature":"Nature","rain":"Weather","snow":"Weather","storm":"Weather","wind":"Weather","technology":"Technology","computer":"Technology","smartphone":"Technology","coding":"Technology","digital":"Technology","software":"Technology","artificial intelligence":"Technology","laptop":"Technology","typing keyboard":"Technology","screen":"Technology","data":"Technology","cyber":"Technology","internet":"Technology","network":"Technology","people":"People","person":"People","human":"People","crowd":"People","audience":"People","face":"People","portrait":"People","walking":"People","talking":"People","business man":"People","woman":"People","group":"People","team":"People","silhouette":"People","hands":"People","city":"City","urban":"City","skyline":"City","street":"City","building":"City","architecture":"City","traffic":"City","night city":"City","downtown":"City","metropolis":"City","bridge":"City","cityscape":"City","rooftop":"City","highway":"City","spider":"Animals","spider web":"Animals","arachnid":"Animals","web spinning":"Animals","insect macro":"Animals","nature closeup":"Animals","dog":"Animals","cat":"Animals","bird":"Animals","horse":"Animals","butterfly":"Animals","animal":"Animals","fish":"Animals","insect":"Animals","gym":"Sports","workout":"Sports","running":"Sports","yoga":"Sports","boxing":"Sports","soccer":"Sports","basketball":"Sports","tennis":"Sports","swimming":"Sports","cycling":"Sports","fitness":"Sports","training":"Sports","cooking":"Food","food":"Food","kitchen":"Food","restaurant":"Food","chef":"Food","baking":"Food","grilling":"Food","travel":"Travel","adventure":"Travel","hiking":"Travel","camping":"Travel","mountain climbing":"Travel","road trip":"Travel","business":"Business","meeting":"Business","office":"Business","presentation":"Business","finance":"Business","money":"Business","startup":"Business","education":"Education","science":"Education","laboratory":"Education","research":"Education","experiment":"Education","student":"Education","music":"Entertainment","concert":"Entertainment","guitar":"Entertainment","piano":"Entertainment","drums":"Entertainment","singing":"Entertainment","dance":"Entertainment","gymnastics":"Entertainment","magic":"Entertainment","circus":"Entertainment","opera":"Entertainment","theater":"Entertainment","video games":"Entertainment","poker":"Entertainment","painting":"Art","drawing":"Art","sculpture":"Art","car":"Transportation","train":"Transportation","airplane":"Transportation","wedding":"Lifestyle","party":"Lifestyle","interview":"Business"}

class ArtlistBulkDownloader:
    def __init__(self):
        self.db_path = ARTLIST_DB
        self.token_file = TOKEN_FILE
        self.tracking_file = TRACKING_FILE
        self.index_file = INDEX_FILE
        self.temp_dir = TEMP_DIR
        self.stock_folder_id = STOCK_FOLDER_ID
        self.tracking_data = self._load_tracking_data()
    
    def _load_tracking_data(self) -> Dict:
        if TRACKING_FILE.exists():
            with open(TRACKING_FILE, 'r') as f:
                return json.load(f)
        return {'uploaded_clips':{},'failed_clips':{},'processed_terms':[],'total_processed':0,'last_updated':datetime.now().isoformat()}
    
    def _filter_youtube_relevant_entities(self, entities: List[str]) -> List[str]:
        filtered = []
        for entity in entities:
            if entity.lower() in GENERIC_WORDS: continue
            if len(entity) < 2 or len(entity) > 50: continue
            if entity.isdigit(): continue
            filtered.append(entity)
        return filtered
    
    def _calculate_entity_priority(self, entity: str, context: str) -> int:
        score = ENTITY_PRIORITY.get('proper_noun',1)
        if context and entity.lower() in context.lower(): score -= 1
        if entity in CATEGORY_MAPPING: score -= 1
        return score
    
    def _get_category_for_entity(self, entity: str) -> str:
        if entity in CATEGORY_MAPPING: return CATEGORY_MAPPING[entity]
        for key, category in CATEGORY_MAPPING.items():
            if key in entity.lower(): return category
        return "Other"
    
    def extract_entities_from_script(self, script_text: str) -> List[Dict]:
        segments = script_text.split('\n\n')
        entities = []
        for i, segment in enumerate(segments):
            proper_nouns = re.findall(r'\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*\b', segment)
            words = re.findall(r'\b\w{4,}\b', segment.lower())
            keyword_freq = {}
            for word in words:
                if word not in GENERIC_WORDS: keyword_freq[word] = keyword_freq.get(word,0)+1
            top_keywords = sorted(keyword_freq.items(), key=lambda x:x[1], reverse=True)[:10]
            keywords = [kw for kw,_ in top_keywords]
            phrases = re.findall(r'\b\w+(?:\s+\w+){1,3}\b', segment)
            phrases = [p for p in phrases if 2<=len(p.split())<=4]
            all_entities = proper_nouns + keywords + phrases
            filtered_entities = self._filter_youtube_relevant_entities(all_entities)
            entity_objects = []
            for entity in filtered_entities:
                priority = self._calculate_entity_priority(entity, segment)
                category = self._get_category_for_entity(entity)
                entity_objects.append({'text':entity,'priority':priority,'category':category,'segment_index':i})
            entities.extend(entity_objects)
        seen = set()
        unique_entities = []
        for entity in sorted(entities, key=lambda x:x['priority']):
            key = (entity['text'].lower(), entity['category'])
            if key not in seen:
                seen.add(key)
                unique_entities.append(entity)
        return unique_entities[:50]
    
    def process_segment_batch(self, segment: str, context: str = "") -> Tuple[List[Dict], List[str]]:
        entities = self.extract_entities_from_script(segment)
        clip_mappings = []
        for entity in entities:
            mapping = {'entity':entity['text'],'category':entity['category'],'priority':entity['priority'],'clips_found':[],'status':'pending'}
            clip_mappings.append(mapping)
        return clip_mappings, entities
    
    def batch_process_segments(self, segments: List[str], contexts: List[str] = None) -> Dict:
        if contexts is None: contexts = [""] * len(segments)
        all_mappings, all_entities = [], []
        for segment, context in zip(segments, contexts):
            mappings, entities = self.process_segment_batch(segment, context)
            all_mappings.extend(mappings)
            all_entities.extend(entities)
        return {'mappings':all_mappings,'entities':all_entities,'segment_count':len(segments)}
    
    def generate_documentation(self, batch_result: Dict) -> str:
        doc = "# Batch Processing Results\n\n"
        doc += f"Total Segments Processed: {batch_result['segment_count']}\n\n"
        doc += f"Total Entities Found: {len(batch_result['entities'])}\n\n"
        doc += "## Entity Summary\n\n"
        by_category = {}
        for entity in batch_result['entities']:
            cat = entity['category']
            if cat not in by_category: by_category[cat] = []
            by_category[cat].append(entity['text'])
        for category, es in sorted(by_category.items()):
            doc += f"### {category} ({len(es)} entities)\n"
            doc += ", ".join(sorted(set(es))) + "\n\n"
        return doc

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Artlist Batch Downloader")
    parser.add_argument("--test-mode", action="store_true")
    args = parser.parse_args()
    d = ArtlistBulkDownloader()
    if args.test_mode:
        s = ["Gervonta Davis born November 7 1994 raised Sandtown-Winchester parents drug addicts",
             "At five years old walked Upton Boxing Center Pennsylvania Avenue met Calvin Ford trainer father figure",
             "Davis turned pro 18 February 22 2013 Desi Williams first-round knockout"]
        r = d.batch_process_segments(s)
        print(d.generate_documentation(r))
        print(f"Processed {len(r['entities'])} entities from {r['segment_count']} segments")
    else:
        print("Use --test-mode")