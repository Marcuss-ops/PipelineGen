#!/usr/bin/env python3
"""Create or trash one bounded temporary Drive test folder.

This deliberately uses the project's canonical google-accounting OAuth client.
It never deletes permanently: cleanup moves the folder to Drive Trash.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials
from googleapiclient.discovery import build

ROOT = Path(__file__).resolve().parents[2]
TOKEN = ROOT / "token.json"
CLIENT = ROOT / "credentials.json"
SCOPES = [
    "https://www.googleapis.com/auth/drive",
    "https://www.googleapis.com/auth/documents",
]

FOLDER_MIME = "application/vnd.google-apps.folder"


def build_service():
    if not TOKEN.is_file():
        raise RuntimeError(f"Drive token not found: {TOKEN}")
    token = json.loads(TOKEN.read_text())
    client = json.loads(CLIENT.read_text()).get("installed", {})
    credentials = Credentials(
        token=token.get("access_token"),
        refresh_token=token.get("refresh_token"),
        token_uri="https://oauth2.googleapis.com/token",
        client_id=client.get("client_id"),
        client_secret=client.get("client_secret"),
        scopes=SCOPES,
    )
    if not credentials.valid:
        if credentials.expired and credentials.refresh_token:
            credentials.refresh(Request())
        else:
            raise RuntimeError("Drive token is expired or invalid; refresh token first")
    return build("drive", "v3", credentials=credentials, cache_discovery=False)


def ensure(service, parent: str, name: str) -> dict[str, str]:
    escaped = name.replace("'", "\\'")
    query = (
        f"'{parent}' in parents and name = '{escaped}' "
        f"and mimeType = '{FOLDER_MIME}' and trashed = false"
    )
    found = (
        service.files()
        .list(q=query, spaces="drive", fields="files(id,name,parents,webViewLink)", pageSize=10)
        .execute()
        .get("files", [])
    )
    if len(found) > 1:
        raise RuntimeError(f"ambiguous temporary folder {name!r} under {parent}")
    if found:
        return found[0]
    return (
        service.files()
        .create(
            body={"name": name, "mimeType": FOLDER_MIME, "parents": [parent]},
            fields="id,name,parents,webViewLink",
        )
        .execute()
    )


def trash(service, folder_id: str) -> dict[str, str | bool]:
    return (
        service.files()
        .update(fileId=folder_id, body={"trashed": True}, fields="id,trashed")
        .execute()
    )


def replace_doc_link(document_id: str, old_link: str, new_link: str) -> dict[str, int | str]:
    drive = build_service()
    token = json.loads(TOKEN.read_text())
    client = json.loads(CLIENT.read_text()).get("installed", {})
    credentials = Credentials(
        token=token.get("access_token"),
        refresh_token=token.get("refresh_token"),
        token_uri="https://oauth2.googleapis.com/token",
        client_id=client.get("client_id"),
        client_secret=client.get("client_secret"),
        scopes=SCOPES,
    )
    if not credentials.valid and credentials.expired and credentials.refresh_token:
        credentials.refresh(Request())
    docs = build("docs", "v1", credentials=credentials, cache_discovery=False)
    reply = docs.documents().batchUpdate(
        documentId=document_id,
        body={"requests": [{"replaceAllText": {"containsText": {"text": old_link, "matchCase": True}, "replaceText": new_link}}]},
    ).execute()
    count = sum(r.get("replaceAllText", {}).get("occurrencesChanged", 0) for r in reply.get("replies", []))
    return {"document_id": document_id, "replacements": count}


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    create = sub.add_parser("ensure")
    create.add_argument("--parent", required=True)
    create.add_argument("--name", required=True)
    move_to_trash = sub.add_parser("trash")
    move_to_trash.add_argument("--folder-id", required=True)
    replace = sub.add_parser("replace-doc-link")
    replace.add_argument("--document-id", required=True)
    replace.add_argument("--old-link", required=True)
    replace.add_argument("--new-link", required=True)
    args = parser.parse_args()
    service = build_service()
    if args.command == "ensure":
        result = ensure(service, args.parent, args.name)
    elif args.command == "trash":
        result = trash(service, args.folder_id)
    else:
        result = replace_doc_link(args.document_id, args.old_link, args.new_link)
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
