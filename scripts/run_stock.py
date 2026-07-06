#!/usr/bin/env python3
# scripts/run_stock.py — CLI client for triggering the Stock Pipeline.
#
# Use this script to run the stock pipeline synchronously (async=false)
# or asynchronously, using the real PipelineGen API, and optionally
# saving the response JSON.
#
# Usage examples:
#   # Synchronous run (blocking, saves results, returns status):
#   export VELOX_ADMIN_TOKEN="d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d"
#   python3 scripts/run_stock.py --queries "boxing training gym" --folder-name "boxing_clips" --output response.json
#
#   # Direct URLs run (sync):
#   python3 scripts/run_stock.py --urls "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4" --folder-name "bunny"

import argparse
import json
import os
import ssl
import sys
import urllib.request
import urllib.error

DEFAULT_BASE_URL = "http://127.0.0.1:8000"

def main():
    parser = argparse.ArgumentParser(description="Trigger PipelineGen Stock Pipeline via HTTP API")
    parser.add_argument("--base-url", default=os.getenv("VELOX_MASTER_URL", DEFAULT_BASE_URL),
                        help=f"PipelineGen master URL (default: {DEFAULT_BASE_URL})")
    parser.add_argument("--token", default=os.getenv("VELOX_ADMIN_TOKEN"),
                        help="Admin bearer token (defaults to VELOX_ADMIN_TOKEN env var)")
    parser.add_argument("--queries", nargs="+", default=[],
                        help="Search queries to feed to the stock pipeline")
    parser.add_argument("--urls", nargs="+", default=[],
                        help="Direct video/asset URLs to download and stage")
    parser.add_argument("--folder-id", default="",
                        help="Drive folder ID target")
    parser.add_argument("--folder-name", default="stock_stage",
                        help="Drive folder name target")
    parser.add_argument("--subfolder", default="",
                        help="Subfolder inside the target folder")
    parser.add_argument("--total-minutes", type=int, default=5,
                        help="Total minutes requested")
    parser.add_argument("--clip-duration", type=int, default=10,
                        help="Duration of each cut clip in seconds")
    parser.add_argument("--chunk-duration", type=int, default=10,
                        help="Duration of processed chunks")
    parser.add_argument("--max-videos", type=int, default=3,
                        help="Max videos to retrieve per query")
    parser.add_argument("--async-mode", action="store_true", default=False,
                        help="Run asynchronously (default is False/synchronous)")
    parser.add_argument("--persist", action="store_true", default=True,
                        help="Persist results in database in sync mode (default is True)")
    parser.add_argument("--output", "-o", help="File to write JSON response to")

    args = parser.parse_args()

    # Validate inputs
    if not args.queries and not args.urls:
        print("Error: Either --queries or --urls must be provided.", file=sys.stderr)
        parser.print_help()
        sys.exit(1)

    token = args.token
    if not token:
        # Fallback check for common test token
        print("Warning: Admin token not set. Set VELOX_ADMIN_TOKEN env var or pass --token.", file=sys.stderr)

    # Build payload matching StockRunPayload Go struct
    payload = {
        "search_queries": args.queries,
        "direct_urls": args.urls,
        "total_minutes": args.total_minutes,
        "chunk_duration": args.chunk_duration,
        "clip_duration": args.clip_duration,
        "max_videos": args.max_videos,
        "subfolder": args.subfolder,
        "folder_name": args.folder_name,
        "folder_id": args.folder_id,
        "async": args.async_mode,
        "persist": args.persist
    }

    url = f"{args.base_url.rstrip('/')}/api/stock-pipeline/run"
    body = json.dumps(payload).encode("utf-8")

    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")

    print(f"Triggering stock pipeline...")
    print(f"POST {url}")
    print(f"Payload: {json.dumps(payload, indent=2)}")

    try:
        # Avoid SSL certificate validation issues on local environments
        ctx = ssl._create_unverified_context()
        with urllib.request.urlopen(req, context=ctx) as response:
            resp_body = response.read().decode("utf-8")
            status_code = response.status
            print(f"\nResponse Code: {status_code}")
            
            # Try parsing JSON
            try:
                resp_json = json.loads(resp_body)
                formatted_response = json.dumps(resp_json, indent=2)
                print(formatted_response)
            except json.JSONDecodeError:
                formatted_response = resp_body
                print(formatted_response)

            # Save to output file if requested
            if args.output:
                with open(args.output, "w", encoding="utf-8") as f:
                    f.write(formatted_response)
                print(f"\nResponse successfully saved to: {args.output}")

    except urllib.error.HTTPError as e:
        print(f"\nHTTP Error {e.code}: {e.reason}", file=sys.stderr)
        try:
            err_body = e.read().decode("utf-8")
            print(err_body, file=sys.stderr)
        except Exception:
            pass
        sys.exit(2)
    except urllib.error.URLError as e:
        print(f"\nConnection Error: {e.reason}", file=sys.stderr)
        sys.exit(3)
    except Exception as e:
        print(f"\nUnexpected Error: {e}", file=sys.stderr)
        sys.exit(4)

if __name__ == "__main__":
    main()
