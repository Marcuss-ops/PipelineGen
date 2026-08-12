#!/usr/bin/env bash
# tests/operational/boxers-generate/lib/reporting.sh — report lifecycle helpers.
# Source-only library for the boxers-generate operational runner.

atomic_publish_file() {
    local source="$1"
    local destination="$2"
    local temporary="${destination}.tmp"
    mkdir -p "$(dirname "$destination")"
    cp "$source" "$temporary"
    mv -f "$temporary" "$destination"
}

archive_pending_reports() {
    local num="$1"
    local stamp
    stamp=$(date -u +%Y%m%dT%H%M%SZ)
    local archived=0
    local path
    for path in "$PENDING_REPORTS_DIR/${num}_"*; do
        [[ -e "$path" ]] || continue
        mv -f "$path" "$INCOMPLETE_REPORTS_DIR/${stamp}_$(basename "$path")"
        archived=$((archived + 1))
    done
    if (( archived > 0 )); then
        printf '%sArchived %d incomplete report artifact(s) for scenario %s%s\\n' \
            "$YELLOW" "$archived" "$num" "$RESET" >&2
    fi
}

publish_scenario_reports() {
    local num="$1"
    local name="$2"
    local pending_raw="$PENDING_REPORTS_DIR/${num}_${name}_job.json"
    local pending_verification="$PENDING_REPORTS_DIR/${num}_${name}_verification_report.json"
    local raw_destination="$REPORTS_DIR/raw/${num}_${name}_job.json"
    local verification_destination="$REPORTS_DIR/${num}_${name}_verification_report.json"
    local raw_tmp="${raw_destination}.tmp"
    local verification_tmp="${verification_destination}.tmp"
    local raw_backup="${raw_destination}.bak.$$"
    local verification_backup="${verification_destination}.bak.$$"
    local had_raw=0
    local had_verification=0

    mkdir -p "$(dirname "$raw_destination")" "$(dirname "$verification_destination")"
    if [[ -e "$raw_destination" ]]; then
        cp "$raw_destination" "$raw_backup" || return 1
        had_raw=1
    fi
    if [[ -e "$verification_destination" ]]; then
        cp "$verification_destination" "$verification_backup" || {
            rm -f "$raw_backup"
            return 1
        }
        had_verification=1
    fi
    if ! cp "$pending_raw" "$raw_tmp" || ! cp "$pending_verification" "$verification_tmp"; then
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    if ! mv -f "$raw_tmp" "$raw_destination"; then
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    if ! mv -f "$verification_tmp" "$verification_destination"; then
        if (( had_raw )); then
            mv -f "$raw_backup" "$raw_destination" || true
        else
            rm -f "$raw_destination"
        fi
        if (( had_verification )); then
            mv -f "$verification_backup" "$verification_destination" || true
        else
            rm -f "$verification_destination"
        fi
        rm -f "$raw_tmp" "$verification_tmp" "$raw_backup" "$verification_backup"
        return 1
    fi
    rm -f "$pending_raw" "$pending_verification" "$raw_backup" "$verification_backup"
}
