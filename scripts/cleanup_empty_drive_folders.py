#!/usr/bin/env python3
"""
Cleanup empty Google Drive folders within PipelineGen project roots.

Usage:
    python3 scripts/cleanup_empty_drive_folders.py --dry-run    # Show empty folders
    python3 scripts/cleanup_empty_drive_folders.py --delete     # Actually delete them
"""

import argparse
import sys
from pathlib import Path

# Add google-accounting to path
sys.path.insert(0, str(Path(__file__).parent.parent / "google-accounting"))

from drive_client import _build_service

# PipelineGen project root folders from config.yaml
PROJECT_ROOT_FOLDERS = [
    ("artlist", "1Dj3-BlM9LcJr3dh3I4VxEDuMBbaBwwSE"),
    ("stock", "1zH0ltYRbZAp9qZ7QFozfdvWFD5QibgPq"),
    ("clips", "1t_Szw00QTlWc3Oa69hUQ65VXf_a7qxcD"),
    ("images", "1kr8c1KZmUus10mkIdqJlYqAzXDyoNZeY"),
    ("video_ai", "1OJ1-ITo8J3sh1IpUiLwoTkXmPCANmnzL"),
    ("scripts", "1_RNkjRqcJjxursFvWJoIV9vrZUuMeuAe"),
    ("books", "1yTNwhOT93s0IN9WqG0cpil1WayUw-2Zl"),
    ("voiceover", "19GIMmY0wS61qUJRRbeeo7hEybuhr9h-m"),
    ("sound_effects", "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW"),
]


def list_folders_recursive(service, parent_id, parent_path="", visited=None, max_depth=10, current_depth=0):
    """Recursively list all folders and their file counts."""
    if visited is None:
        visited = set()
    if parent_id in visited or current_depth >= max_depth:
        return []
    visited.add(parent_id)

    results = []
    page_token = None

    while True:
        query = f"'{parent_id}' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'"
        response = (
            service.files()
            .list(
                q=query,
                spaces="drive",
                fields="nextPageToken, files(id, name)",
                pageToken=page_token,
                pageSize=1000,
            )
            .execute()
        )

        items = response.get("files", [])
        for item in items:
            subpath = f"{parent_path}/{item['name']}" if parent_path else item["name"]
            results.append({
                "id": item["id"],
                "name": item["name"],
                "path": subpath,
                "parent_id": parent_id,
            })
            # Recurse into subfolder
            results.extend(list_folders_recursive(service, item["id"], subpath, visited, max_depth=3, current_depth=current_depth + 1))

        page_token = response.get("nextPageToken")
        if not page_token:
            break

    return results


def is_folder_empty(service, folder_id):
    """Check if a folder has any non-folder items (files)."""
    query = f"'{folder_id}' in parents and trashed = false and mimeType != 'application/vnd.google-apps.folder'"
    response = (
        service.files()
        .list(
            q=query,
            spaces="drive",
            fields="nextPageToken, files(id)",
            pageSize=1,
        )
        .execute()
    )

    items = response.get("files", [])
    return len(items) == 0


def delete_folder(service, folder_id, use_trash=True):
    """Delete or trash a folder."""
    if use_trash:
        service.files().update(fileId=folder_id, body={"trashed": True}).execute()
        return "trashed"
    else:
        service.files().delete(fileId=folder_id).execute()
        return "deleted"


def main():
    parser = argparse.ArgumentParser(description="Clean up empty Google Drive folders")
    parser.add_argument("--dry-run", action="store_true", help="Show empty folders without deleting")
    parser.add_argument("--delete", action="store_true", help="Actually delete empty folders")
    parser.add_argument("--root-folder", type=str, help="Specific Drive folder ID to scan (overrides defaults)")
    parser.add_argument("--trash", action="store_true", default=True, help="Move to trash instead of permanent delete")
    args = parser.parse_args()

    if not args.dry_run and not args.delete:
        print("Usage: --dry-run to preview, --delete to actually remove")
        sys.exit(1)

    print("Building Google Drive service...")
    service = _build_service()

    if args.root_folder:
        roots = [("custom", args.root_folder)]
    else:
        roots = PROJECT_ROOT_FOLDERS

    all_folders = []
    for name, root_id in roots:
        print(f"Scanning {name} ({root_id})...")
        folders = list_folders_recursive(service, root_id, name)
        all_folders.extend(folders)
        print(f"  → {len(folders)} folders found")

    print(f"\nTotal folders found: {len(all_folders)}")

    empty_folders = []
    for folder in all_folders:
        if is_folder_empty(service, folder["id"]):
            empty_folders.append(folder)

    print(f"\nFound {len(empty_folders)} empty folders:")
    for folder in empty_folders:
        print(f"  - {folder['path']} (id: {folder['id']})")

    if args.delete and empty_folders:
        print(f"\nDeleting {len(empty_folders)} empty folders...")
        deleted = 0
        failed = 0
        for folder in empty_folders:
            try:
                action = delete_folder(service, folder["id"], use_trash=args.trash)
                print(f"  {action}: {folder['path']}")
                deleted += 1
            except Exception as e:
                print(f"  FAILED: {folder['path']} — {e}")
                failed += 1
        print(f"\nDone: {deleted} deleted, {failed} failed")
    else:
        print(f"\nDry-run: {len(empty_folders)} empty folders would be {'trashed' if args.trash else 'deleted'}")


if __name__ == "__main__":
    main()
