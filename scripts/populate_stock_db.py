#!/usr/bin/env python3
"""
Popola il DB locale (JSON) con i dati scansionati da Drive.
Legge data/stock_drive_structure.json e scrive data/stock.db.json
"""
import json
import os
import time
from datetime import datetime

INPUT_FILE = "data/stock_drive_structure.json"
OUTPUT_FILE = "data/stock.db.json"

def normalize_slug(s):
    s = s.lower().replace(" ", "-").replace("/", "-").replace("_", "-")
    while "--" in s:
        s = s.replace("--", "-")
    return s.strip("-")

def generate_tags(filename):
    name = filename.lower().replace(".mp4", "").replace("_", " ").replace("-", " ")
    return name.strip()

def detect_source(folder_name):
    name = folder_name.lower()
    if "artlist" in name:
        return "artlist"
    if "stock" in name or "final" in name:
        return "stock"
    return "youtube"

def main():
    print("=" * 60)
    print("📦 Drive Scan → Local DB Populator")
    print("=" * 60)
    
    with open(INPUT_FILE) as f:
        data = json.load(f)
    
    folders = []
    clips = []
    
    # Process stock_folders
    stock_trees = data.get("stock_folders", [])
    clips_trees = data.get("clips_folders", [])
    all_trees = stock_trees + clips_trees
    
    section_type = "stock"
    for i, tree in enumerate(all_trees):
        if i == len(stock_trees):
            section_type = "clips"
            print(f"\n🎬 Processing CLIPS section...")
        elif i == 0:
            print(f"\n📦 Processing STOCK section...")
        
        root_name = tree.get("name", "Unknown")
        print(f"\n  📁 {root_name}")
        
        # Process subfolders
        for sub in (tree.get("subfolders") or []):
            sub_name = sub.get("name", "Unknown")
            sub_id = sub.get("id", "")
            sub_path = sub.get("path", f"Stock/{root_name}/{sub_name}")
            sub_url = sub.get("url", "")
            
            folder_entry = {
                "topic_slug": normalize_slug(f"{root_name}-{sub_name}"),
                "drive_id": sub_id,
                "parent_id": tree.get("id", ""),
                "full_path": sub_path,
                "last_synced": datetime.now().isoformat()
            }
            folders.append(folder_entry)
            
            # Process clips in this subfolder
            for clip in (sub.get("clips") or []):
                clip_entry = {
                    "clip_id": clip.get("id", ""),
                    "folder_id": sub_id,
                    "filename": clip.get("name", ""),
                    "source": detect_source(root_name),
                    "tags": generate_tags(clip.get("name", "")),
                    "duration": int(clip.get("duration_ms", 0) / 1000)
                }
                clips.append(clip_entry)
            
            # Process nested subfolders (3 levels deep)
            for sub2 in (sub.get("subfolders") or []):
                sub2_name = sub2.get("name", "Unknown")
                sub2_id = sub2.get("id", "")
                sub2_path = f"{sub_path}/{sub2_name}"
                sub2_url = sub2.get("url", "")
                
                folder_entry2 = {
                    "topic_slug": normalize_slug(f"{root_name}-{sub_name}-{sub2_name}"),
                    "drive_id": sub2_id,
                    "parent_id": sub_id,
                    "full_path": sub2_path,
                    "last_synced": datetime.now().isoformat()
                }
                folders.append(folder_entry2)
                
                for clip in (sub2.get("clips") or []):
                    clip_entry = {
                        "clip_id": clip.get("id", ""),
                        "folder_id": sub2_id,
                        "filename": clip.get("name", ""),
                        "source": detect_source(root_name),
                        "tags": generate_tags(clip.get("name", "")),
                        "duration": int(clip.get("duration_ms", 0) / 1000)
                    }
                    clips.append(clip_entry)
            
            print(f"    {sub_name}: {len([c for c in clips if c['folder_id'] == sub_id])} clips")
    
    # Build DB structure
    db = {
        "last_synced": datetime.now().isoformat(),
        "folders": folders,
        "clips": clips
    }
    
    # Save
    os.makedirs(os.path.dirname(OUTPUT_FILE), exist_ok=True)
    with open(OUTPUT_FILE, "w") as f:
        json.dump(db, f, indent=2)
    
    print(f"\n{'=' * 60}")
    print(f"💾 Saved to: {OUTPUT_FILE}")
    print(f"📊 Summary:")
    print(f"   Folders: {len(folders)}")
    print(f"   Clips:   {len(clips)}")
    print(f"   Stock clips: {len([c for c in clips if c['source'] == 'stock'])}")
    print(f"   Artlist clips: {len([c for c in clips if c['source'] == 'artlist'])}")
    print(f"{'=' * 60}")

if __name__ == "__main__":
    main()
