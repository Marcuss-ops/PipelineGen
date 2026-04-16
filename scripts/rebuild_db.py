#!/usr/bin/env python3
"""Regenerate stock.db.json with section field (stock vs clips)"""
import json
import os

INPUT = os.path.join(os.path.dirname(__file__), "..", "src", "go-master", "data", "stock_drive_structure.json")
OUTPUT = os.path.join(os.path.dirname(__file__), "..", "src", "go-master", "data", "stock.db.json")

def normalize_slug(s):
    s = s.lower().replace(" ", "-").replace("/", "-").replace("_", "-")
    while "--" in s:
        s = s.replace("--", "-")
    return s.strip("-")

def generate_tags(filename):
    return filename.lower().replace(".mp4", "").replace("_", " ").replace("-", " ").strip()

def process_tree(tree, section, folders_list, clips_list):
    root_name = tree.get("name", "Unknown")
    
    for sub in (tree.get("subfolders") or []):
        sub_name = sub.get("name", "Unknown")
        sub_id = sub.get("id", "")
        
        full_path = f"{section}/{root_name}/{sub_name}"
        
        folder_entry = {
            "topic_slug": normalize_slug(f"{root_name}-{sub_name}"),
            "drive_id": sub_id,
            "parent_id": tree.get("id", ""),
            "full_path": full_path,
            "section": section,
            "last_synced": "2026-04-13T00:00:00"
        }
        folders_list.append(folder_entry)
        
        for clip in (sub.get("clips") or []):
            dur_ms = clip.get("duration_ms", 0) or 0
            clip_entry = {
                "clip_id": clip.get("id", ""),
                "folder_id": sub_id,
                "filename": clip.get("name", ""),
                "source": section,
                "tags": generate_tags(clip.get("name", "")),
                "duration": int(dur_ms / 1000)
            }
            clips_list.append(clip_entry)
        
        for sub2 in (sub.get("subfolders") or []):
            sub2_name = sub2.get("name", "Unknown")
            sub2_id = sub2.get("id", "")
            full_path2 = f"{section}/{root_name}/{sub_name}/{sub2_name}"
            
            folder_entry2 = {
                "topic_slug": normalize_slug(f"{root_name}-{sub_name}-{sub2_name}"),
                "drive_id": sub2_id,
                "parent_id": sub_id,
                "full_path": full_path2,
                "section": section,
                "last_synced": "2026-04-13T00:00:00"
            }
            folders_list.append(folder_entry2)
            
            for clip in (sub2.get("clips") or []):
                dur_ms = clip.get("duration_ms", 0) or 0
                clip_entry = {
                    "clip_id": clip.get("id", ""),
                    "folder_id": sub2_id,
                    "filename": clip.get("name", ""),
                    "source": section,
                    "tags": generate_tags(clip.get("name", "")),
                    "duration": int(dur_ms / 1000)
                }
                clips_list.append(clip_entry)

def main():
    with open(INPUT) as f:
        data = json.load(f)

    folders = []
    clips = []

    for tree in data.get("stock_folders", []):
        process_tree(tree, "stock", folders, clips)

    for tree in data.get("clips_folders", []):
        process_tree(tree, "clips", folders, clips)

    db = {
        "last_synced": "2026-04-13T00:00:00",
        "folders": folders,
        "clips": clips
    }

    os.makedirs(os.path.dirname(OUTPUT), exist_ok=True)
    with open(OUTPUT, "w") as f:
        json.dump(db, f, indent=2)

    stock_f = len([x for x in folders if x["section"] == "stock"])
    clips_f = len([x for x in folders if x["section"] == "clips"])
    stock_c = len([x for x in clips if x["source"] == "stock"])
    clips_c = len([x for x in clips if x["source"] == "clips"])
    artlist_c = len([x for x in clips if "artlist" in x.get("filename", "").lower()])

    print("=" * 60)
    print("DB Updated with Section Separation")
    print("=" * 60)
    print(f"Stock folders: {stock_f}")
    print(f"Clips folders: {clips_f}")
    print(f"Stock clips: {stock_c}")
    print(f"Clips section clips: {clips_c}")
    print(f"Artlist clips: {artlist_c}")
    print(f"Total: {len(folders)} folders, {len(clips)} clips")

    cats = {}
    for f in folders:
        parts = f["full_path"].split("/")
        if len(parts) >= 3:
            cat = f'{f["section"]}/{parts[1]}'
            cats[cat] = cats.get(cat, 0) + 1

    print()
    print("Categories by section:")
    for cat in sorted(cats.keys()):
        print(f"  {cat}: {cats[cat]} folders")

if __name__ == "__main__":
    main()
