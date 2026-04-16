#!/usr/bin/env python3
"""
Populate the Artlist SQLite DB with more search terms.
Maps existing videos to new related search terms so the Go backend
can search for many keywords without needing new actual videos.

Usage: python3 populate_artlist_terms.py
"""
import sqlite3
import random
import json
from datetime import datetime

DB_PATH = "src/node-scraper/artlist_videos.db"

# Map existing terms → new related terms
# Each existing term's videos will be duplicated with these new search terms
TERM_EXPANSIONS = {
    "spider": ["spider web", "arachnid", "web spinning", "insect macro", "nature closeup"],
    "technology": ["computer", "smartphone", "coding", "digital", "software", "artificial intelligence", 
                   "laptop", "typing keyboard", "screen", "data", "cyber", "internet", "network"],
    "people": ["person", "human", "crowd", "audience", "face", "portrait", "walking", "talking",
               "business man", "woman", "group", "team", "silhouette", "hands"],
    "nature": ["landscape", "forest", "mountain", "ocean", "river", "sunset", "sky", "cloud",
               "flower", "tree", "wildlife", "waterfall", "beach", "garden", "autumn"],
    "city": ["urban", "skyline", "street", "building", "architecture", "traffic", "night city",
             "downtown", "metropolis", "bridge", "cityscape", "rooftop", "highway"],
}

def main():
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    
    # Get category ID for Artlist
    c.execute("SELECT id FROM categories WHERE name='Artlist'")
    artlist_cat = c.fetchone()
    if not artlist_cat:
        # Create Artlist category
        c.execute("INSERT INTO categories (name, description) VALUES ('Artlist', 'Artlist stock footage')")
        artlist_cat = (c.lastrowid,)
    artlist_cat_id = artlist_cat[0]
    
    print(f"Artlist category ID: {artlist_cat_id}")
    
    # Get existing videos for each term
    total_new = 0
    total_new_terms = 0
    
    for existing_term, new_terms in TERM_EXPANSIONS.items():
        # Get existing video IDs for this term
        c.execute("""
            SELECT DISTINCT vl.id, vl.search_term_id, vl.category_id, vl.url, vl.video_id,
                   vl.file_name, vl.file_size, vl.downloaded, vl.download_path, vl.source,
                   vl.width, vl.height, vl.duration
            FROM video_links vl
            JOIN search_terms st ON vl.search_term_id = st.id
            WHERE st.term = ? AND vl.source = 'artlist'
        """, (existing_term,))
        existing_videos = c.fetchall()
        
        if not existing_videos:
            print(f"  ⚠ No videos found for term: {existing_term}")
            continue
        
        print(f"\n  {existing_term}: {len(existing_videos)} videos → expanding to {len(new_terms)} new terms")
        
        for new_term in new_terms:
            # Check if search term already exists
            c.execute("SELECT id FROM search_terms WHERE term = ?", (new_term,))
            existing = c.fetchone()
            
            if existing:
                term_id = existing[0]
                print(f"    Term already exists: {new_term} (id={term_id})")
            else:
                # Insert new search term
                c.execute("""
                    INSERT INTO search_terms (category_id, term, scraped, video_count)
                    VALUES (?, ?, 1, 0)
                """, (artlist_cat_id, new_term))
                term_id = c.lastrowid
                total_new_terms += 1
                print(f"    New term: {new_term} (id={term_id})")
            
            # For each existing video, check if already linked to this term
            c.execute("SELECT COUNT(*) FROM video_links WHERE search_term_id = ? AND source = 'artlist'", (term_id,))
            current_count = c.fetchone()[0]
            
            if current_count >= len(existing_videos):
                print(f"      Already has {current_count} videos, skipping")
                continue
            
            # Link videos to new term (sample 10-20 videos per new term)
            videos_to_link = random.sample(existing_videos, min(15, len(existing_videos)))
            linked = 0
            for vid in videos_to_link:
                # Check if this exact URL is already linked to this term
                c.execute("""
                    SELECT COUNT(*) FROM video_links 
                    WHERE search_term_id = ? AND url = ? AND source = 'artlist'
                """, (term_id, vid[3]))
                if c.fetchone()[0] > 0:
                    continue
                
                # Insert new video link
                c.execute("""
                    INSERT INTO video_links 
                    (search_term_id, category_id, url, video_id, file_name, file_size,
                     downloaded, download_path, source, width, height, duration)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'artlist', ?, ?, ?)
                """, (
                    term_id, artlist_cat_id, vid[3], vid[4], vid[5],
                    vid[6], 0, None, vid[10], vid[11], vid[12]
                ))
                linked += 1
            
            # Update video count for this term
            c.execute("SELECT COUNT(*) FROM video_links WHERE search_term_id = ? AND source = 'artlist'", (term_id,))
            final_count = c.fetchone()[0]
            c.execute("UPDATE search_terms SET video_count = ? WHERE id = ?", (final_count, term_id))
            
            total_new += linked
            print(f"      Linked {linked} videos (total: {final_count})")
    
    # Final stats
    conn.commit()
    
    print(f"\n{'='*60}")
    print(f"  New search terms created: {total_new_terms}")
    print(f"  New video links created:  {total_new}")
    print(f"{'='*60}")
    
    # Print all terms
    print(f"\n  All search terms now in DB:")
    c.execute("""
        SELECT DISTINCT st.term, COUNT(vl.id) as cnt
        FROM search_terms st
        JOIN video_links vl ON st.id = vl.search_term_id
        WHERE vl.source = 'artlist'
        GROUP BY st.term
        ORDER BY cnt DESC
    """)
    for r in c.fetchall():
        bar = '█' * min(r[1] // 2, 40)
        print(f"    {r[0]:30s} {r[1]:4d} {bar}")
    
    conn.close()
    print(f"\n  Done! DB path: {DB_PATH}")

if __name__ == "__main__":
    random.seed(42)  # Reproducible
    main()
