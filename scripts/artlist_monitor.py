#!/usr/bin/env python3
"""
Artlist Progress Monitor - Shows download progress and statistics

Usage:
    python3 scripts/artlist_monitor.py            # Show current progress
    python3 scripts/artlist_monitor.py --watch    # Watch mode (refreshes every 10s)
    python3 scripts/artlist_monitor.py --json     # Output as JSON
"""

import os
import json
import sqlite3
import time
import argparse
from datetime import datetime
from pathlib import Path

# Paths
SCRIPT_DIR = Path(__file__).parent.parent
ARTLIST_DB = SCRIPT_DIR / "src/node-scraper/artlist_videos.db"
TRACKING_FILE = SCRIPT_DIR / "data/artlist_uploaded.json"
INDEX_FILE = SCRIPT_DIR / "data/artlist_stock_index.json"
CRON_STATE_FILE = SCRIPT_DIR / "data/artlist_cron_state.json"

def load_json_file(path):
    """Load JSON file if it exists"""
    if path.exists():
        with open(path) as f:
            return json.load(f)
    return None

def get_db_stats():
    """Get statistics from SQLite DB"""
    if not ARTLIST_DB.exists():
        return None

    conn = sqlite3.connect(ARTLIST_DB)
    cur = conn.cursor()

    # Total terms and clips
    cur.execute("""
        SELECT 
            COUNT(DISTINCT st.term) as term_count,
            COUNT(vl.id) as clip_count
        FROM search_terms st
        JOIN video_links vl ON st.id = vl.search_term_id
        WHERE vl.source='artlist' AND vl.url IS NOT NULL
    """)
    total_terms, total_clips = cur.fetchone()

    # Per-term breakdown
    cur.execute("""
        SELECT st.term, COUNT(vl.id) as cnt
        FROM search_terms st
        JOIN video_links vl ON st.id = vl.search_term_id
        WHERE vl.source='artlist' AND vl.url IS NOT NULL
        GROUP BY st.term
        ORDER BY cnt DESC
    """)
    term_breakdown = [(row[0], row[1]) for row in cur.fetchall()]

    conn.close()

    return {
        "total_terms": total_terms,
        "total_clips": total_clips,
        "term_breakdown": term_breakdown
    }

def get_upload_stats():
    """Get upload statistics from tracking file"""
    tracking = load_json_file(TRACKING_FILE)
    if not tracking:
        return None

    uploaded = tracking.get("uploaded", {})
    stats = tracking.get("stats", {})

    total_uploaded = sum(len(clips) for clips in uploaded.values())
    
    per_term = {}
    for term in uploaded:
        term_stats = stats.get(term, {})
        per_term[term] = {
            "uploaded": len(uploaded[term]),
            "success": term_stats.get("success", 0),
            "failed": term_stats.get("failed", 0),
            "last_run": term_stats.get("last_run", "Never")
        }

    return {
        "total_uploaded": total_uploaded,
        "per_term": per_term,
        "last_updated": tracking.get("last_updated")
    }

def get_index_stats():
    """Get index file statistics"""
    index = load_json_file(INDEX_FILE)
    if not index:
        return None

    return {
        "total_clips": index.get("total_clips", len(index.get("clips", []))),
        "folder_id": index.get("folder_id"),
        "created_at": index.get("created_at")
    }

def get_cron_stats():
    """Get cron job statistics"""
    cron_state = load_json_file(CRON_STATE_FILE)
    if not cron_state:
        return None

    runs = cron_state.get("runs", [])
    schedule = cron_state.get("schedule", {})

    total_runs = len(runs)
    successful_runs = sum(1 for r in runs if r.get("success"))
    failed_runs = total_runs - successful_runs

    return {
        "scheduled_jobs": len(schedule.get("jobs", [])),
        "total_runs": total_runs,
        "successful_runs": successful_runs,
        "failed_runs": failed_runs,
        "last_run": runs[-1]["started_at"] if runs else None
    }

def display_progress():
    """Display current progress in human-readable format"""
    print("=" * 70)
    print("🎬 Artlist Download Progress")
    print("=" * 70)
    print(f"📅 {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print()

    # DB stats
    db_stats = get_db_stats()
    if db_stats:
        print("📊 DATABASE (SQLite):")
        print(f"   Total search terms: {db_stats['total_terms']}")
        print(f"   Total clips indexed: {db_stats['total_clips']}")
        print()

    # Upload stats
    upload_stats = get_upload_stats()
    if upload_stats:
        print("✅ UPLOADS (to Google Drive):")
        print(f"   Total uploaded: {upload_stats['total_uploaded']}")
        print()

        print("   Per-term breakdown:")
        for term, stats in sorted(upload_stats['per_term'].items()):
            uploaded = stats['uploaded']
            success = stats['success']
            failed = stats['failed']
            last_run_str = stats['last_run'][:19] if stats['last_run'] != "Never" else "Never"
            
            # Progress bar
            if db_stats:
                term_total = next((cnt for t, cnt in db_stats['term_breakdown'] if t == term), 0)
                progress = min(uploaded / 20 * 100, 100) if uploaded > 0 else 0  # Assuming target is 20 per term
            else:
                progress = 0
            
            bar = '█' * int(progress / 5) + '░' * (20 - int(progress / 5))
            
            print(f"   {term:25s} [{bar:20s}] {uploaded:3d} clips (✅ {success} ❌ {failed}) 🕐 {last_run_str}")
        print()

    # Index stats
    index_stats = get_index_stats()
    if index_stats:
        print("📁 INDEX FILE:")
        print(f"   Total clips in index: {index_stats['total_clips']}")
        print(f"   Main folder ID: {index_stats['folder_id']}")
        print(f"   Last updated: {index_stats['created_at'][:19]}")
        print()

    # Cron stats
    cron_stats = get_cron_stats()
    if cron_stats:
        print("⏰ CRON JOBS:")
        print(f"   Scheduled jobs: {cron_stats['scheduled_jobs']}")
        print(f"   Total runs: {cron_stats['total_runs']}")
        print(f"   Successful: {cron_stats['successful_runs']}")
        print(f"   Failed: {cron_stats['failed_runs']}")
        if cron_stats['last_run']:
            print(f"   Last run: {cron_stats['last_run'][:19]}")
        print()

    # Overall summary
    print("=" * 70)
    if upload_stats and db_stats:
        total_possible = db_stats['total_clips']
        total_done = upload_stats['total_uploaded']
        percentage = (total_done / total_possible * 100) if total_possible > 0 else 0
        
        print(f"📈 OVERALL PROGRESS:")
        print(f"   Downloaded: {total_done} / {total_possible} clips")
        print(f"   Progress: {percentage:.1f}%")
        print(f"   Remaining: {total_possible - total_done} clips")
    print("=" * 70)

def display_json():
    """Output statistics as JSON"""
    data = {
        "timestamp": datetime.now().isoformat(),
        "database": get_db_stats(),
        "uploads": get_upload_stats(),
        "index": get_index_stats(),
        "cron": get_cron_stats()
    }
    print(json.dumps(data, indent=2))

def watch_mode(interval=10):
    """Watch mode - refreshes display every interval seconds"""
    print(f"👁️  Watch mode (refreshing every {interval}s)")
    print(f"Press Ctrl+C to stop\n")
    
    try:
        while True:
            os.system('clear')
            display_progress()
            time.sleep(interval)
    except KeyboardInterrupt:
        print("\n\n🛑 Watch mode stopped")

def main():
    parser = argparse.ArgumentParser(description='Artlist Progress Monitor')
    parser.add_argument("--watch", action="store_true", help="Watch mode (refreshes every 10s)")
    parser.add_argument("--interval", type=int, default=10, help="Refresh interval in seconds (for watch mode)")
    parser.add_argument("--json", action="store_true", help="Output as JSON")
    
    args = parser.parse_args()

    if args.json:
        display_json()
    elif args.watch:
        watch_mode(args.interval)
    else:
        display_progress()

if __name__ == '__main__':
    main()
