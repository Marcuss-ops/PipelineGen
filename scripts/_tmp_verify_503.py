#!/usr/bin/env python3
"""Start the PipelineGen server, send curl requests to artlist endpoints,
capture actual HTTP responses, report findings, then tear down.
"""
import json
import os
import signal
import subprocess
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path

PORT = 18080
BASE = f"http://127.0.0.1:{PORT}"
SERVER_BIN = r"C:\WINDOWS\TEMP\pipelinegen-server"
DATA_DIR = r"C:\pg-test\data"
LOG_FILE = r"C:\pg-test\server.log"
PID_FILE = r"C:\pg-test\server.pid"

# Prepare data dir
Path(DATA_DIR).mkdir(parents=True, exist_ok=True)
Path(LOG_FILE).unlink(missing_ok=True)
Path(PID_FILE).unlink(missing_ok=True)

# Build env
env = os.environ.copy()
env.update({
    "VELOX_PORT": str(PORT),
    "VELOX_HOST": "127.0.0.1",
    "VELOX_MASTER_URL": BASE,
    "VELOX_ENABLE_AUTH": "false",
    "VELOX_ALLOW_INSECURE_DEV": "true",
    "VELOX_ALLOW_PLACEHOLDERS": "true",
    "VELOX_DATA_DIR": DATA_DIR,
    "VELOX_PRIMARY_DB_PATH": f"{DATA_DIR}\\media.db.sqlite",
})

print("=" * 60)
print("STARTING SERVER")
print("=" * 60)
# Use CREATE_NEW_PROCESS_GROUP on Windows for clean teardown
proc = subprocess.Popen(
    [SERVER_BIN],
    stdout=open(LOG_FILE, "w"),
    stderr=subprocess.STDOUT,
    env=env,
    cwd="/c/Users/pater/Pyt/PipelineGen",
    creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == "win32" else 0,
)
print(f"PID: {proc.pid}")
Path(PID_FILE).write_text(str(proc.pid))

# Wait for server to be ready (up to 15 seconds)
print("\nWaiting for server to be ready...")
ready = False
for i in range(30):
    time.sleep(0.5)
    if proc.poll() is not None:
        print(f"!!! Server exited with code {proc.returncode} after {i*0.5:.1f}s")
        break
    try:
        req = urllib.request.urlopen(f"{BASE}/api/health", timeout=1)
        if req.status == 200:
            print(f"Server ready after {i*0.5:.1f}s (HTTP {req.status})")
            ready = True
            break
    except (urllib.error.URLError, ConnectionError, OSError):
        continue

if not ready and proc.poll() is None:
    print("Server still running but /api/health not responding; trying endpoints anyway...")

# Print startup log
print("\n" + "=" * 60)
print("STARTUP LOG (first 60 lines)")
print("=" * 60)
if Path(LOG_FILE).exists():
    log_content = Path(LOG_FILE).read_text(errors="replace")
    for line in log_content.split("\n")[:60]:
        print(line)

if proc.poll() is not None:
    print(f"\n!!! Server crashed at startup with code {proc.returncode}")
    print("Full log:")
    print(log_content)
    sys.exit(1)

# Send curl requests to the 5 artlist endpoints
print("\n" + "=" * 60)
print("CURL REQUESTS")
print("=" * 60)

endpoints = [
    ("POST", "/api/artlist/recommend",
     {"topic": "test", "segment_id": "abc", "queries": [], "min_score": 0}),
    ("POST", "/api/artlist/sync-catalogs", {}),
    ("GET", "/api/artlist/stats", None),
    ("GET", "/api/artlist/diagnostics?term=test", None),
    ("POST", "/api/artlist/run", {"term": "test", "limit": 10}),
]

for method, path, body in endpoints:
    print(f"\n--- {method} {BASE}{path} ---")
    if body is not None:
        print(f"Request body: {json.dumps(body)}")
    try:
        data = json.dumps(body).encode("utf-8") if body is not None else None
        req = urllib.request.Request(
            f"{BASE}{path}",
            data=data,
            method=method,
            headers={"Content-Type": "application/json"} if body is not None else {},
        )
        with urllib.request.urlopen(req, timeout=5) as resp:
            status = resp.status
            response_body = resp.read().decode("utf-8", errors="replace")
            print(f"HTTP {status}")
            print(f"Response body: {response_body[:500]}")
    except urllib.error.HTTPError as e:
        status = e.code
        response_body = e.read().decode("utf-8", errors="replace")
        print(f"HTTP {status} (HTTPError)")
        print(f"Response body: {response_body[:500]}")
    except (urllib.error.URLError, ConnectionError, OSError) as e:
        print(f"ERROR: {e}")

# Print remaining log
print("\n" + "=" * 60)
print("FULL SERVER LOG (after requests)")
print("=" * 60)
if Path(LOG_FILE).exists():
    log_content = Path(LOG_FILE).read_text(errors="replace")
    lines = log_content.split("\n")
    # Print lines 60+ (the ones we haven't shown yet)
    for line in lines[60:]:
        print(line)

# Tear down
print("\n" + "=" * 60)
print("TEAR DOWN")
print("=" * 60)
if sys.platform == "win32":
    # Use taskkill on Windows for clean process tree teardown
    subprocess.run(["taskkill", "/F", "/T", "/PID", str(proc.pid)],
                   capture_output=True)
else:
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
print(f"Server PID {proc.pid} terminated")
