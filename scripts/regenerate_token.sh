#!/usr/bin/env bash
# scripts/regenerate_token.sh
# A simple wrapper to regenerate the Google Drive token.json

set -euo pipefail

# Find repo root
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CRED_FILE="${REPO_ROOT}/credentials.json"
TOKEN_FILE="${REPO_ROOT}/token.json"

echo "=== PipelineGen Google Drive Token Regenerator ==="

# Check if credentials.json exists
if [ ! -f "$CRED_FILE" ]; then
    echo "ERROR: Google Client credentials not found at ${CRED_FILE}."
    echo "Please download the credentials JSON (Desktop/Installed Application type) from Google Cloud Console"
    echo "and save it as 'credentials.json' in the root of the project."
    exit 1
fi

# Detect if credentials.json is a Manicode file instead of Google OAuth client secrets
if grep -q '"authToken"' "$CRED_FILE" || grep -q '"fingerprintId"' "$CRED_FILE" || ! grep -q -E '"web"|"installed"' "$CRED_FILE"; then
    echo "WARNING: ${CRED_FILE} looks like Manicode credentials, not Google OAuth credentials!"
    echo "A valid Google credentials JSON must contain either 'web' or 'installed' key."
    echo "Please download the correct client secrets JSON from the Google Cloud Console"
    echo "and overwrite ${CRED_FILE} with it."
    exit 1
fi

echo "Using credentials from: ${CRED_FILE}"
echo "Token will be saved to: ${TOKEN_FILE}"
echo ""
echo "Starting Google OAuth manual flow..."
echo "Please follow the instructions on screen."
echo ""

export OAUTHLIB_INSECURE_TRANSPORT=1

python3 "${REPO_ROOT}/scripts/tools/generate_drive_token.py" \
    --credentials "$CRED_FILE" \
    --token "$TOKEN_FILE" \
    --manual
