#!/usr/bin/env python3
"""
Phase 1: Clean DB - remove 60 fake expanded terms, keep only 5 originals with 300 unique clips
Phase 2: Scrape NEW real clips from Artlist GraphQL API for many new search terms
Phase 3: Organize into sensible categories (Nature, Tech, City, People, Animals, Sports, etc.)

Usage:
    python3 scripts/clean_and_expand_artlist.py --clean-only     # Just clean DB
    python3 scripts/clean_and_expand_artlist.py --scrape         # Clean + scrape new clips
    python3 scripts/clean_and_expand_artlist.py --scrape --terms "gym,cooking,travel"  # Custom terms
"""

import sqlite3
import subprocess
import json
import sys
import os
import argparse
from pathlib import Path
from datetime import datetime

DB_PATH = Path("src/node-scraper/artlist_videos.db")
NODE_SCRAPER = Path("src/node-scraper")
MAP_SCRIPT = NODE_SCRAPER / "scripts/map_artlist.js"

# New search terms to scrape - organized by category
# Each term can yield up to 500 clips from Artlist API
NEW_SEARCH_TERMS = [
    # Nature & Landscape
    "sunset", "ocean", "mountain", "forest", "river", "waterfall", "beach", "sky", "cloud",
    "flower", "tree", "garden", "wildlife", "autumn", "landscape",
    
    # Technology & Digital
    "computer", "smartphone", "coding", "digital", "software", "artificial intelligence",
    "laptop", "typing keyboard", "screen", "data", "cyber", "internet", "network",
    
    # People & Society
    "person", "human", "crowd", "audience", "face", "portrait", "walking", "talking",
    "business man", "woman", "group", "team", "silhouette", "hands",
    
    # City & Architecture
    "urban", "skyline", "street", "building", "architecture", "traffic", "night city",
    "downtown", "metropolis", "bridge", "cityscape", "rooftop", "highway",
    
    # Animals & Nature
    "wildlife", "bird", "fish", "animal", "insect", "butterfly", "dog", "cat", "horse",
    
    # Sports & Fitness
    "gym", "workout", "running", "yoga", "boxing", "soccer", "basketball", "tennis",
    "swimming", "cycling", "fitness", "training",
    
    # Food & Cooking
    "cooking", "food", "kitchen", "restaurant", "chef", "baking", "grilling",
    
    # Travel & Adventure
    "travel", "adventure", "hiking", "camping", "mountain climbing", "road trip",
    
    # Business & Finance
    "business", "meeting", "office", "presentation", "finance", "money", "startup",
    
    # Education & Science
    "education", "science", "laboratory", "research", "experiment", "student",
    
    # Music & Entertainment
    "music", "concert", "guitar", "piano", "drums", "singing", "dancing",
    
    # Family & Lifestyle
    "family", "children", "baby", "couple", "love", "wedding", "party",
    
    # Weather & Seasons
    "rain", "snow", "storm", "wind", "summer", "winter", "spring",
    
    # Transportation
    "car", "train", "airplane", "boat", "helicopter", "motorcycle",
]


def clean_db():
    """Remove all fake expanded terms, keep only 5 originals"""
    print("=" * 70)
    print("🧹 PHASE 1: Clean DB")
    print("=" * 70)
    
    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    
    # Original 5 terms
    original_terms = ["spider", "technology", "people", "nature", "city"]
    
    # Count current state
    cur.execute("SELECT COUNT(DISTINCT term) FROM search_terms")
    total_terms_before = cur.fetchone()[0]
    
    cur.execute("SELECT COUNT(*) FROM video_links WHERE source='artlist'")
    total_links_before = cur.fetchone()[0]
    
    print(f"\n  Before cleaning:")
    print(f"    Search terms: {total_terms_before}")
    print(f"    Video links: {total_links_before}")
    
    # Find terms to delete (all non-original terms)
    placeholders = ",".join("?" for _ in original_terms)
    cur.execute(f"""
        DELETE FROM search_terms 
        WHERE term NOT IN ({placeholders})
    """, original_terms)
    deleted_terms = cur.rowcount
    
    # Delete video links that point to deleted terms
    cur.execute(f"""
        DELETE FROM video_links 
        WHERE source='artlist' 
          AND search_term_id NOT IN (
              SELECT id FROM search_terms WHERE term IN ({placeholders})
          )
    """, original_terms)
    deleted_links = cur.rowcount
    
    # Verify
    cur.execute("SELECT COUNT(DISTINCT term) FROM search_terms")
    total_terms_after = cur.fetchone()[0]
    
    cur.execute("SELECT COUNT(*) FROM video_links WHERE source='artlist'")
    total_links_after = cur.fetchone()[0]
    
    conn.commit()
    
    print(f"\n  After cleaning:")
    print(f"    Search terms: {total_terms_after} (deleted {deleted_terms})")
    print(f"    Video links: {total_links_after} (deleted {deleted_links})")
    
    # Show remaining terms
    cur.execute("""
        SELECT st.term, COUNT(vl.id) as cnt, COUNT(DISTINCT vl.url) as unique_urls
        FROM search_terms st
        JOIN video_links vl ON st.id = vl.search_term_id
        WHERE vl.source='artlist'
        GROUP BY st.term
    """)
    print(f"\n  ✅ Remaining terms:")
    for term, total, unique in cur.fetchall():
        print(f"    {term:20s} → {total} links, {unique} unique URLs")
    
    conn.close()
    return True


def scrape_new_terms(terms_to_scrape, max_pages=5):
    """Scrape new clips from Artlist GraphQL API"""
    print("=" * 70)
    print("🔍 PHASE 2: Scrape NEW clips from Artlist")
    print("=" * 70)
    
    if not MAP_SCRIPT.exists():
        print(f"❌ Map script not found: {MAP_SCRIPT}")
        return False
    
    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    
    # Get Artlist category ID
    cur.execute("SELECT id FROM categories WHERE name='Artlist'")
    row = cur.fetchone()
    if not row:
        cur.execute("INSERT INTO categories (name, description) VALUES ('Artlist', 'Artlist stock footage')")
        category_id = cur.lastrowid
    else:
        category_id = row[0]
    
    total_new_clips = 0
    total_new_terms = 0
    
    for term in terms_to_scrape:
        print(f"\n{'─' * 70}")
        print(f"📥 Searching: '{term}' (max {max_pages} pages)")
        print(f"{'─' * 70}")
        
        # Check if term already exists
        cur.execute("SELECT id FROM search_terms WHERE term=?", (term,))
        existing = cur.fetchone()
        
        if existing:
            term_id = existing[0]
            cur.execute("SELECT COUNT(*) FROM video_links WHERE search_term_id=? AND source='artlist'", (term_id,))
            current_count = cur.fetchone()[0]
            if current_count > 0:
                print(f"  ⏭️  Term already exists with {current_count} clips, skipping")
                continue
        else:
            # Create new search term
            cur.execute("""
                INSERT INTO search_terms (category_id, term, scraped, video_count)
                VALUES (?, ?, 0, 0)
            """, (category_id, term))
            term_id = cur.lastrowid
            total_new_terms += 1
        
        # Run Node.js scraper
        cmd = ["node", str(MAP_SCRIPT), term, str(max_pages)]
        print(f"  ⏳ Running: {' '.join(cmd)}")
        
        try:
            result = subprocess.run(
                cmd,
                cwd=str(NODE_SCRAPER),
                capture_output=True,
                text=True,
                timeout=300  # 5 min timeout
            )
            
            if result.returncode == 0:
                # Parse output to see how many clips were found
                output = result.stdout
                # Look for patterns like "Found X clips" or count lines
                lines = output.strip().split('\n')
                
                # Count how many URLs were added
                cur.execute("SELECT COUNT(*) FROM video_links WHERE search_term_id=? AND source='artlist'", (term_id,))
                new_count = cur.fetchone()[0]
                
                if new_count > 0:
                    print(f"  ✅ Found {new_count} clips for '{term}'")
                    total_new_clips += new_count
                else:
                    print(f"  ⚠️  No clips found for '{term}'")
            else:
                print(f"  ❌ Scraper failed: {result.stderr[:200]}")
                
        except subprocess.TimeoutExpired:
            print(f"  ❌ Timeout (5 min) for '{term}'")
        except Exception as e:
            print(f"  ❌ Error: {e}")
        
        # Small pause between terms
        import time
        time.sleep(2)
    
    # Update scraped flag
    cur.execute("UPDATE search_terms SET scraped=1 WHERE term IN (SELECT DISTINCT term FROM search_terms)")
    
    conn.commit()
    conn.close()
    
    print(f"\n{'=' * 70}")
    print(f"✅ SCRAPING COMPLETE")
    print(f"{'=' * 70}")
    print(f"  New terms created: {total_new_terms}")
    print(f"  New clips found: {total_new_clips}")
    
    return True


def show_stats():
    """Show final statistics"""
    print(f"\n{'=' * 70}")
    print("📊 FINAL STATISTICS")
    print(f"{'=' * 70}")
    
    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    
    cur.execute("SELECT COUNT(DISTINCT st.term) FROM search_terms st JOIN video_links vl ON st.id = vl.search_term_id WHERE vl.source='artlist'")
    term_count = cur.fetchone()[0]
    
    cur.execute("SELECT COUNT(DISTINCT vl.url) FROM video_links vl WHERE vl.source='artlist'")
    unique_urls = cur.fetchone()[0]
    
    cur.execute("SELECT COUNT(*) FROM video_links WHERE source='artlist'")
    total_links = cur.fetchone()[0]
    
    print(f"\n  Search terms: {term_count}")
    print(f"  Unique URLs: {unique_urls}")
    print(f"  Total entries: {total_links}")
    
    cur.execute("""
        SELECT st.term, COUNT(vl.id) as cnt
        FROM search_terms st
        JOIN video_links vl ON st.id = vl.search_term_id
        WHERE vl.source='artlist'
        GROUP BY st.term
        ORDER BY cnt DESC
    """)
    print(f"\n  Terms breakdown:")
    for term, count in cur.fetchall():
        bar = '█' * min(count // 5, 50)
        print(f"    {term:25s} {count:5d} {bar}")
    
    conn.close()


def main():
    parser = argparse.ArgumentParser(description='Clean and expand Artlist DB')
    parser.add_argument("--clean-only", action="store_true", help="Only clean DB, no scraping")
    parser.add_argument("--scrape", action="store_true", help="Clean + scrape new clips")
    parser.add_argument("--terms", type=str, default=None, help="Comma-separated terms to scrape")
    parser.add_argument("--pages", type=int, default=5, help="Max pages per term (default: 5)")
    
    args = parser.parse_args()
    
    if not DB_PATH.exists():
        print(f"❌ DB not found: {DB_PATH}")
        sys.exit(1)
    
    if args.clean_only:
        clean_db()
        show_stats()
        return
    
    if args.scrape:
        # Phase 1: Clean
        clean_db()
        
        # Phase 2: Scrape
        if args.terms:
            terms = [t.strip() for t in args.terms.split(",")]
        else:
            terms = NEW_SEARCH_TERMS
        
        print(f"\n🎯 Will scrape {len(terms)} search terms")
        print(f"   Expected clips: ~{len(terms) * 250} (avg 250 per term)")
        
        confirm = input("\n⚠️  This will make {len(terms)} API calls to Artlist. Continue? (y/n): ")
        if confirm.lower() != 'y':
            print("Cancelled")
            return
        
        scrape_new_terms(terms, max_pages=args.pages)
        show_stats()
        return
    
    # Default: clean only
    print("No action specified. Use --clean-only or --scrape")
    print("Examples:")
    print("  python3 scripts/clean_and_expand_artlist.py --clean-only")
    print("  python3 scripts/clean_and_expand_artlist.py --scrape")
    print("  python3 scripts/clean_and_expand_artlist.py --scrape --terms \"gym,cooking,travel\"")


if __name__ == '__main__':
    main()
