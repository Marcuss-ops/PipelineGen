#!/usr/bin/env bash
# tests/operational/boxers-generate/test_reporting.sh — offline report-library test.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

REPORTS_DIR="$TMP_DIR/reports"
PENDING_REPORTS_DIR="$TMP_DIR/pending"
INCOMPLETE_REPORTS_DIR="$TMP_DIR/incomplete"
RED=''
YELLOW=''
RESET=''
mkdir -p "$REPORTS_DIR" "$PENDING_REPORTS_DIR" "$INCOMPLETE_REPORTS_DIR"

# shellcheck disable=SC1091
source "$DIR/lib/reporting.sh"

printf 'raw report\n' > "$PENDING_REPORTS_DIR/01_Name_job.json"
printf 'verification report\n' > "$PENDING_REPORTS_DIR/01_Name_verification_report.json"
publish_scenario_reports 01 Name

test "$(cat "$REPORTS_DIR/raw/01_Name_job.json")" = 'raw report'
test "$(cat "$REPORTS_DIR/01_Name_verification_report.json")" = 'verification report'
test ! -e "$PENDING_REPORTS_DIR/01_Name_job.json"
test ! -e "$PENDING_REPORTS_DIR/01_Name_verification_report.json"

printf 'incomplete report\n' > "$PENDING_REPORTS_DIR/02_Name_job.json"
archive_pending_reports 02
archived_count=$(find "$INCOMPLETE_REPORTS_DIR" -type f -name '*_02_Name_job.json' | wc -l | tr -d ' ')
test "$archived_count" = 1

echo 'PASS: boxers-generate reporting library contract'
