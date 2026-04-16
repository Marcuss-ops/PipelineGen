#!/usr/bin/env python3
"""
Artlist Cron Job Manager - Manages scheduled downloads for Artlist clips

Features:
- Schedule downloads at specific times (every N hours)
- Run different terms at different times
- Track progress across runs
- Automatic retry for failed downloads

Usage:
    python3 scripts/artlist_cron_manager.py start    # Start the cron scheduler
    python3 scripts/artlist_cron_manager.py run-now  # Run one download cycle now
    python3 scripts/artlist_cron_manager.py status   # Show scheduled jobs
"""

import os
import sys
import json
import time
import subprocess
import argparse
from datetime import datetime, timedelta
from pathlib import Path
from apscheduler.schedulers.blocking import BlockingScheduler
from apscheduler.triggers.interval import IntervalTrigger
from apscheduler.triggers.cron import CronTrigger

# Paths
SCRIPT_DIR = Path(__file__).parent.parent
CRON_STATE_FILE = SCRIPT_DIR / "data/artlist_cron_state.json"
BULK_DOWNLOADER = SCRIPT_DIR / "scripts/artlist_bulk_downloader.py"

# Default schedule configuration
DEFAULT_SCHEDULE = {
    "jobs": [
        {
            "name": "Morning Batch",
            "time": "06:00",  # 6 AM
            "clips_per_term": 20,
            "terms": ["sunset", "ocean", "mountain", "forest", "rain", "snow"],  # Nature + Weather
            "enabled": True
        },
        {
            "name": "Midday Batch",
            "time": "12:00",  # 12 PM
            "clips_per_term": 20,
            "terms": ["gym", "yoga", "running", "soccer", "swimming", "fitness"],  # Sports
            "enabled": True
        },
        {
            "name": "Afternoon Batch",
            "time": "15:00",  # 3 PM
            "clips_per_term": 20,
            "terms": ["cooking", "food", "kitchen", "chef", "business", "travel"],  # Food + Business + Travel
            "enabled": True
        },
        {
            "name": "Evening Batch",
            "time": "18:00",  # 6 PM
            "clips_per_term": 20,
            "terms": ["dog", "cat", "bird", "horse", "butterfly", "music"],  # Animals + Entertainment
            "enabled": True
        },
        {
            "name": "Night Batch",
            "time": "00:00",  # 12 AM
            "clips_per_term": 15,
            "terms": ["car", "train", "airplane", "wedding", "party", "concert"],  # Transportation + Lifestyle
            "enabled": True
        }
    ]
}

def load_cron_state():
    """Load cron state from file"""
    if CRON_STATE_FILE.exists():
        with open(CRON_STATE_FILE) as f:
            return json.load(f)
    return {
        "schedule": DEFAULT_SCHEDULE,
        "runs": [],
        "last_updated": None
    }

def save_cron_state(state):
    """Save cron state to file"""
    CRON_STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
    state["last_updated"] = datetime.now().isoformat()
    with open(CRON_STATE_FILE, 'w') as f:
        json.dump(state, f, indent=2)

def run_download_cycle(job_config, state):
    """Run a single download cycle for a job"""
    job_name = job_config["name"]
    clips_per_term = job_config.get("clips_per_term", 15)
    terms = job_config.get("terms", [])
    
    if not job_config.get("enabled", True):
        print(f"⏭️  Job '{job_name}' is disabled, skipping")
        return

    print(f"\n{'=' * 70}")
    print(f"🚀 Starting job: {job_name}")
    print(f"📅 {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"🎯 Clips per term: {clips_per_term}")
    print(f"📋 Terms: {', '.join(terms)}")
    print(f"{'=' * 70}\n")

    # Build command
    cmd = [
        sys.executable, str(BULK_DOWNLOADER),
        "--clips-per-term", str(clips_per_term),
        "--terms", ",".join(terms)
    ]

    # Run the downloader
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=3600  # 1 hour timeout for bulk download
        )

        # Log the run
        run_record = {
            "job": job_name,
            "started_at": datetime.now().isoformat(),
            "clips_per_term": clips_per_term,
            "terms": terms,
            "exit_code": result.returncode,
            "success": result.returncode == 0
        }

        if result.returncode == 0:
            print(f"✅ Job '{job_name}' completed successfully")
        else:
            print(f"❌ Job '{job_name}' failed with exit code {result.returncode}")
            print(f"   STDOUT: {result.stdout[-500:]}")
            print(f"   STDERR: {result.stderr[-500:]}")

        # Update state
        state["runs"].append(run_record)
        
        # Keep only last 50 runs in history
        if len(state["runs"]) > 50:
            state["runs"] = state["runs"][-50:]

        save_cron_state(state)

    except subprocess.TimeoutExpired:
        print(f"❌ Job '{job_name}' timed out after 1 hour")
        state["runs"].append({
            "job": job_name,
            "started_at": datetime.now().isoformat(),
            "exit_code": -1,
            "success": False,
            "error": "Timeout"
        })
        save_cron_state(state)

    except Exception as e:
        print(f"❌ Job '{job_name}' failed with exception: {e}")
        state["runs"].append({
            "job": job_name,
            "started_at": datetime.now().isoformat(),
            "exit_code": -1,
            "success": False,
            "error": str(e)
        })
        save_cron_state(state)

def schedule_jobs():
    """Schedule all jobs using APScheduler"""
    print("=" * 70)
    print("🎬 Artlist Cron Manager")
    print("=" * 70)
    print(f"📅 {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print()

    state = load_cron_state()
    schedule = state.get("schedule", DEFAULT_SCHEDULE)

    scheduler = BlockingScheduler()

    for job in schedule.get("jobs", []):
        if not job.get("enabled", True):
            print(f"⏭️  Skipping disabled job: {job['name']}")
            continue

        # Parse time (HH:MM format)
        time_str = job.get("time", "12:00")
        hour, minute = map(int, time_str.split(":"))

        # Schedule to run daily at specified time
        trigger = CronTrigger(hour=hour, minute=minute, timezone="Europe/Rome")

        scheduler.add_job(
            run_download_cycle,
            trigger=trigger,
            args=[job, state],
            id=job["name"],
            name=job["name"],
            replace_existing=True
        )

        print(f"📅 Scheduled: {job['name']} at {job['time']}")
        print(f"   Terms: {', '.join(job.get('terms', []))}")
        print(f"   Clips/term: {job.get('clips_per_term', 15)}")
        print()

    job_count = len(scheduler.get_jobs())
    print(f"✅ Scheduler started with {job_count} jobs")
    print(f"📄 State file: {CRON_STATE_FILE}")
    print(f"\nPress Ctrl+C to stop\n")

    try:
        scheduler.start()
    except (KeyboardInterrupt, SystemExit):
        print("\n\n🛑 Scheduler stopped")
        scheduler.shutdown()

def run_now():
    """Run all enabled jobs right now"""
    print("=" * 70)
    print("🚀 Running all enabled jobs NOW")
    print("=" * 70)

    state = load_cron_state()
    schedule = state.get("schedule", DEFAULT_SCHEDULE)

    for job in schedule.get("jobs", []):
        if job.get("enabled", True):
            run_download_cycle(job, state)

    print("\n✅ All jobs completed")

def show_status():
    """Show current schedule and run history"""
    state = load_cron_state()
    schedule = state.get("schedule", DEFAULT_SCHEDULE)
    runs = state.get("runs", [])

    print("=" * 70)
    print("📅 Artlist Cron Schedule")
    print("=" * 70)
    print()

    print("📋 SCHEDULED JOBS:")
    for job in schedule.get("jobs", []):
        status = "✅ Enabled" if job.get("enabled", True) else "⏭️  Disabled"
        print(f"   {job['name']:20s} at {job.get('time', 'N/A'):5s} - {status}")
        print(f"      Terms: {', '.join(job.get('terms', []))}")
        print(f"      Clips/term: {job.get('clips_per_term', 15)}")
        print()

    print("\n📈 RECENT RUNS (last 10):")
    for run in runs[-10:]:
        status = "✅" if run.get("success") else "❌"
        print(f"   {status} {run['job']:20s} - {run['started_at'][:19]}")

    print()
    print(f"📄 State file: {CRON_STATE_FILE}")
    print(f"📊 Total runs: {len(runs)}")

def add_job(name, time, terms, clips_per_term=15):
    """Add a new scheduled job"""
    state = load_cron_state()
    
    new_job = {
        "name": name,
        "time": time,
        "terms": terms.split(","),
        "clips_per_term": clips_per_term,
        "enabled": True
    }

    if "jobs" not in state["schedule"]:
        state["schedule"]["jobs"] = []

    state["schedule"]["jobs"].append(new_job)
    save_cron_state(state)

    print(f"✅ Added job: {name} at {time}")
    print(f"   Terms: {', '.join(terms.split(','))}")
    print(f"   Clips/term: {clips_per_term}")

def remove_job(name):
    """Remove a scheduled job"""
    state = load_cron_state()
    
    jobs = state["schedule"].get("jobs", [])
    state["schedule"]["jobs"] = [j for j in jobs if j["name"] != name]
    
    save_cron_state(state)
    print(f"✅ Removed job: {name}")

def main():
    parser = argparse.ArgumentParser(description='Artlist Cron Manager')
    parser.add_argument(
        "command",
        choices=["start", "run-now", "status", "add-job", "remove-job"],
        help="Command to execute"
    )
    parser.add_argument("--name", type=str, help="Job name (for add-job/remove-job)")
    parser.add_argument("--time", type=str, help="Time HH:MM (for add-job)")
    parser.add_argument("--terms", type=str, help="Comma-separated terms (for add-job)")
    parser.add_argument("--clips", type=int, default=15, help="Clips per term (for add-job)")
    
    args = parser.parse_args()

    if args.command == "start":
        schedule_jobs()
    elif args.command == "run-now":
        run_now()
    elif args.command == "status":
        show_status()
    elif args.command == "add-job":
        if not all([args.name, args.time, args.terms]):
            print("❌ Error: --name, --time, and --terms are required for add-job")
            sys.exit(1)
        add_job(args.name, args.time, args.terms, args.clips)
    elif args.command == "remove-job":
        if not args.name:
            print("❌ Error: --name is required for remove-job")
            sys.exit(1)
        remove_job(args.name)

if __name__ == '__main__':
    main()
