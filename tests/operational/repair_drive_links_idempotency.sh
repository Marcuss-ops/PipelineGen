#!/usr/bin/env bash
# Verify that replaying repair-drive-links is idempotent.
#
# Required environment:
#   REPAIR_JOB_ID       completed job to repair
#   REPAIR_DB_PATH      canonical SQLite database path
# Optional:
#   REPAIR_DOC_IDS      comma-separated Google Doc IDs when they cannot be
#                       extracted from job result_json
#   QDRANT_URL          Qdrant REST root (default http://127.0.0.1:6333)
#   QDRANT_COLLECTION   collection/alias (default media_assets_current)
#   REPAIR_COMMAND      command used to invoke the admin CLI
#                       (default: go run ./cmd/admin)
#
# This script is deliberately opt-in. Set CONFIRM_REPAIR_LIVE=YES before
# running it because the first pass may mutate the selected database,
# enqueue Qdrant work, and refresh existing Google Docs.
set -Eeuo pipefail

: "${REPAIR_JOB_ID:?set REPAIR_JOB_ID to the controlled completed job}"
: "${REPAIR_DB_PATH:?set REPAIR_DB_PATH to the canonical SQLite database}"
: "${REPAIR_SCRIPT_ID:?set REPAIR_SCRIPT_ID to the script row persisted by the controlled job}"
: "${CONFIRM_REPAIR_LIVE:?set CONFIRM_REPAIR_LIVE=YES to authorize the two live repair runs}"
[[ "$CONFIRM_REPAIR_LIVE" == YES ]] || {
    echo "FAIL: refusing live repair; set CONFIRM_REPAIR_LIVE=YES explicitly" >&2
    exit 2
}
[[ -f "$REPAIR_DB_PATH" ]] || { echo "FAIL: SQLite database not found: $REPAIR_DB_PATH" >&2; exit 2; }
[[ "$REPAIR_JOB_ID" =~ ^[A-Za-z0-9_.:-]+$ ]] || { echo "FAIL: invalid REPAIR_JOB_ID" >&2; exit 2; }
[[ "$REPAIR_SCRIPT_ID" =~ ^[0-9]+$ ]] || { echo "FAIL: invalid REPAIR_SCRIPT_ID" >&2; exit 2; }
command -v sqlite3 >/dev/null || { echo "FAIL: sqlite3 is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "FAIL: jq is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "FAIL: curl is required" >&2; exit 2; }
command -v sha256sum >/dev/null || { echo "FAIL: sha256sum is required" >&2; exit 2; }

QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
read -r -a REPAIR_COMMAND_ARGS <<< "${REPAIR_COMMAND:-go run ./cmd/admin}"
((${#REPAIR_COMMAND_ARGS[@]} > 0)) || { echo "FAIL: REPAIR_COMMAND is empty" >&2; exit 2; }
WORK_DIR="${TMPDIR:-/tmp}/repair-drive-links-idempotency.$$.${RANDOM}"
mkdir -p "$WORK_DIR"
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

run_repair() {
    local output="$1"
    "${REPAIR_COMMAND_ARGS[@]}" repair-drive-links \
        --job-id "$REPAIR_JOB_ID" \
        --remove-invalid --refresh-docs --audit >"$output"
    jq -e 'type == "object" and .job_id == $job and (.no_op | type == "boolean")' \
        --arg job "$REPAIR_JOB_ID" "$output" >/dev/null || {
        echo "FAIL: repair did not produce a valid audit report: $output" >&2
        cat "$output" >&2
        return 1
    }
}

snapshot_sqlite() {
    local output="$1" raw ids sql_ids jobs assets outbox uploads script result_hash
    raw=$(sqlite3 -noheader "$REPAIR_DB_PATH" \
        "SELECT COALESCE(result_json, '') FROM jobs WHERE id='$REPAIR_JOB_ID';")
    result_hash=$(printf '%s' "$raw" | sha256sum | awk '{print $1}')
    ids=$(printf '%s' "$raw" | jq -r '[.data.items[]?.result.output.specscene.scenes[]?.bindings.clip.clip_id,
                                           .data.items[]?.result.output.specscene.scenes[]?.bindings.stock.asset_id,
                                           .data.items[]?.result.output.specscene.scenes[]?.bindings.media[]?.asset_id,
                                           .items[]?.result.output.specscene.scenes[]?.bindings.clip.clip_id,
                                           .items[]?.result.output.specscene.scenes[]?.bindings.stock.asset_id,
                                           .items[]?.result.output.specscene.scenes[]?.bindings.media[]?.asset_id]
                                          | map(select(type == "string" and length > 0 and startswith("voiceover:") | not))
                                          | unique | .[]' || true)
    sql_ids=$(printf '%s\n' "$ids" | sed "s/'/''/g; s/.*/'&'/" | paste -sd, -)
    jobs=$(sqlite3 -json "$REPAIR_DB_PATH" \
        "SELECT id,status,length(COALESCE(result_json,'')) AS result_bytes,result_hash FROM (SELECT id,status,result_json,'$result_hash' AS result_hash FROM jobs WHERE id='$REPAIR_JOB_ID');")
    if [[ -n "$sql_ids" ]]; then
        assets=$(sqlite3 -json "$REPAIR_DB_PATH" \
            "SELECT id,drive_file_id,drive_link,lifecycle_state FROM media_assets WHERE id IN ($sql_ids) ORDER BY id;")
        outbox=$(sqlite3 -json "$REPAIR_DB_PATH" \
            "SELECT event_key,event_type,aggregate_id FROM outbox_events WHERE aggregate_id IN ($sql_ids) ORDER BY event_key;")
    else
        assets='[]'
        outbox='[]'
    fi
    uploads=$(sqlite3 -json "$REPAIR_DB_PATH" \
        "SELECT event_type,COUNT(*) AS count FROM outbox_events WHERE lower(event_type) LIKE '%upload%' GROUP BY event_type ORDER BY event_type;")
    script=$(sqlite3 -json "$REPAIR_DB_PATH" \
        "SELECT id,status,final_word_count,narrative_text,specscene,manifest_v2 FROM scripts WHERE id=$REPAIR_SCRIPT_ID;")
    jq -e --argjson script "$script" \
        '($script | length == 1) and ($script[0].status == "completed") and ($script[0].final_word_count > 0) and (($script[0].narrative_text // "") | length > 0) and (($script[0].specscene // "") | length > 0) and (($script[0].manifest_v2 // "") | length > 0) and ((($script[0].specscene // "") + ($script[0].manifest_v2 // "")) | test("OLD_ID|DELETED_ID|BROKEN_LINK") | not)' >/dev/null || {
        echo "FAIL: scripts row is incomplete or contains stale Drive references" >&2
        exit 2
    }
    jq -n --argjson jobs "$jobs" --argjson assets "$assets" --argjson outbox "$outbox" --argjson uploads "$uploads" --argjson script "$script" \
        '{jobs:$jobs,assets:$assets,outbox:$outbox,upload_events:$uploads,script:$script}' >"$output"
}

extract_asset_ids() {
    sqlite3 -noheader "$REPAIR_DB_PATH" \
        "SELECT result_json FROM jobs WHERE id='$REPAIR_JOB_ID';" |
        jq -r '[.data.items[]?.result.output.specscene.scenes[]?.bindings.clip.clip_id,
                .data.items[]?.result.output.specscene.scenes[]?.bindings.stock.asset_id,
                .data.items[]?.result.output.specscene.scenes[]?.bindings.media[]?.asset_id,
                .items[]?.result.output.specscene.scenes[]?.bindings.clip.clip_id,
                .items[]?.result.output.specscene.scenes[]?.bindings.stock.asset_id,
                .items[]?.result.output.specscene.scenes[]?.bindings.media[]?.asset_id]
               | map(select(type == "string" and length > 0 and startswith("voiceover:") | not))
               | unique | .[]'
}

extract_doc_ids() {
    if [[ -n "${REPAIR_DOC_IDS:-}" ]]; then
        printf '%s\n' "$REPAIR_DOC_IDS" | tr ',' '\\n' | sed '/^[[:space:]]*$/d; s/^[[:space:]]*//; s/[[:space:]]*$//'
        return
    fi
    sqlite3 -noheader "$REPAIR_DB_PATH" \
        "SELECT result_json FROM jobs WHERE id='$REPAIR_JOB_ID';" |
        jq -r '[.data.items[]?.result.provenance.doc_id,
                .items[]?.result.provenance.doc_id]
               | map(select(type == "string" and length > 0)) | unique | .[]'
}

qdrant_snapshot() {
    local output="$1" validate="${2:-yes}" ids filter response http_code expected attempt
    ids=$(extract_asset_ids)
    [[ -n "$ids" ]] || { echo "FAIL: no asset IDs available for Qdrant snapshot" >&2; exit 2; }
    filter=$(printf '%s\n' "$ids" | jq -Rn '[inputs | select(length > 0) | {key:"asset_id",match:{value:.}}] | {should:.}')
    expected=$(sqlite3 -json "$REPAIR_DB_PATH" \
        "SELECT id,drive_file_id,drive_link,lifecycle_state FROM media_assets WHERE id IN ($(printf '%s\n' "$ids" | sed "s/'/''/g; s/.*/'&'/" | paste -sd, -)) ORDER BY id;")
    jq -e --argjson requested "$(printf '%s\n' "$ids" | jq -Rn '[inputs | select(length > 0)]')" \
        --argjson assets "$expected" \
        '($assets | map(.id)) as $known | ($requested | all(.[]; . as $id | ($known | index($id)) != null))' >/dev/null || {
        echo "FAIL: SQLite has no row for every requested Qdrant asset" >&2
        exit 2
    }
    http_code=$(curl -sS --max-time 20 -o "$WORK_DIR/qdrant.response" -w '%{http_code}' -X POST \
        "$QDRANT_URL/collections/$QDRANT_COLLECTION/points/scroll" \
        -H 'Content-Type: application/json' \
        -d "$(jq -cn --argjson filter "$filter" '{filter:$filter,limit:1000,with_payload:true,with_vector:false}')" || true)
    [[ "$http_code" =~ ^2[0-9][0-9]$ ]] || {
        echo "FAIL: Qdrant snapshot failed (HTTP $http_code)" >&2
        cat "$WORK_DIR/qdrant.response" >&2 || true
        exit 2
    }
    jq -e '.status == "ok" and (.result.points | type == "array")' "$WORK_DIR/qdrant.response" >/dev/null || {
        echo "FAIL: Qdrant returned an invalid scroll response" >&2
        exit 2
    }
    jq -S '[.result.points[] | {id, payload}] | sort_by(.id|tostring)' "$WORK_DIR/qdrant.response" >"$output"
    if [[ "$validate" == no ]]; then
        return 0
    fi
    if ! jq -e --argjson expected "$expected" \
        --argjson points "$(cat "$output")" \
        '$expected | all(.[] as $asset;
            any($points[];
                (.payload.asset_id // "") == $asset.id
                and (.payload.drive_file_id // "") == ($asset.drive_file_id // "")
                and (.payload.drive_link // "") == ($asset.drive_link // "")
                and (.payload.lifecycle_state // "") == ($asset.lifecycle_state // "")
            )
            or ((($asset.drive_link // "") == "")
                and (($asset.lifecycle_state // "") | IN("ERROR", "MISSING", "TRASHED", "INACCESSIBLE"))
                and (($points | map(select((.payload.asset_id // "") == $asset.id)) | length) == 0))
        )' >/dev/null; then
        echo "FAIL: Qdrant payload does not match SQLite asset identity/location/state" >&2
        jq -n --argjson expected "$expected" --argjson points "$(cat "$output")" '{sqlite:$expected,qdrant:$points}' >&2
        return 1
    fi
    if ! jq -e --argjson points "$(cat "$output")" \
        '$points | all(.[]; ((.payload.drive_link // "") | test("OLD_ID|DELETED_ID|BROKEN_LINK") | not))' >/dev/null; then
        echo "FAIL: stale Drive link remains in Qdrant payload" >&2
        return 1
    fi
}

qdrant_converged_snapshot() {
    local output="$1" attempt
    for attempt in $(seq 1 30); do
        if qdrant_snapshot "$output" yes; then
            return 0
        fi
        sleep 2
    done
    echo "FAIL: Qdrant did not converge to SQLite after 60 seconds" >&2
    exit 2
}

doc_snapshot() {
    local output="$1" doc_id
    : >"$output"
    while IFS= read -r doc_id; do
        [[ -n "$doc_id" ]] || continue
        "${REPAIR_COMMAND_ARGS[@]}" audit-google-doc-links --doc-id "$doc_id" >"$WORK_DIR/doc-${doc_id}.json" || {
            # The audit intentionally exits non-zero when invalid links exist,
            # but still emits a machine-readable JSON report.
            jq -e 'type == "object" and .document_id == $doc' --arg doc "$doc_id" "$WORK_DIR/doc-${doc_id}.json" >/dev/null || {
                echo "FAIL: document audit failed without a valid report for $doc_id" >&2
                exit 2
            }
        }
        jq -e 'type == "object" and .document_id == $doc and (.document_drive_links_invalid | type == "number")' \
            --arg doc "$doc_id" "$WORK_DIR/doc-${doc_id}.json" >/dev/null || exit 2
        jq -S --arg doc "$doc_id" '{document_id:$doc,document_drive_links_total,document_drive_links_valid,document_drive_links_invalid,links}' \
            "$WORK_DIR/doc-${doc_id}.json" >>"$output"
    done < <(extract_doc_ids)
    [[ -s "$output" ]] || { echo "FAIL: no Google Doc IDs available for document snapshot" >&2; exit 2; }
    jq -s 'sort_by(.document_id)' "$output" >"$output.tmp" && mv "$output.tmp" "$output"
}

# Verify the collection before any mutation, then snapshot the selected job.
curl -fsS --max-time 10 "$QDRANT_URL/collections/$QDRANT_COLLECTION" >"$WORK_DIR/qdrant.collection.json" || {
    echo "FAIL: Qdrant collection is unavailable: $QDRANT_COLLECTION" >&2
    exit 2
}
jq -e '.status == "ok"' "$WORK_DIR/qdrant.collection.json" >/dev/null || {
    echo "FAIL: Qdrant collection did not return status=ok: $QDRANT_COLLECTION" >&2
    exit 2
}
snapshot_sqlite "$WORK_DIR/sqlite_before.json"
qdrant_snapshot "$WORK_DIR/qdrant_before.json" no
doc_snapshot "$WORK_DIR/docs_before.json"

run_repair "$WORK_DIR/first.json"
jq -e '.no_op == false and (.specscene_repaired == true or .sqlite_updated == true or .qdrant_events_emitted > 0 or .documents_refreshed > 0)' "$WORK_DIR/first.json" >/dev/null || {
    echo "FAIL: first repair did not report an actual mutation" >&2
    cat "$WORK_DIR/first.json" >&2
    exit 1
}
snapshot_sqlite "$WORK_DIR/sqlite_after_first.json"
qdrant_converged_snapshot "$WORK_DIR/qdrant_after_first.json"
doc_snapshot "$WORK_DIR/docs_after_first.json"
jq -e 'all(.[]; .document_drive_links_invalid == 0)' "$WORK_DIR/docs_after_first.json" >/dev/null || {
    echo "FAIL: first repair left invalid Google Doc Drive links" >&2
    cat "$WORK_DIR/docs_after_first.json" >&2
    exit 1
}

run_repair "$WORK_DIR/second.json"
snapshot_sqlite "$WORK_DIR/sqlite_after_second.json"
qdrant_converged_snapshot "$WORK_DIR/qdrant_after_second.json"
doc_snapshot "$WORK_DIR/docs_after_second.json"
jq -e 'all(.[]; .document_drive_links_invalid == 0)' "$WORK_DIR/docs_after_second.json" >/dev/null || {
    echo "FAIL: second repair has invalid Google Doc Drive links" >&2
    cat "$WORK_DIR/docs_after_second.json" >&2
    exit 1
}

jq -e '.no_op | type == "boolean"' "$WORK_DIR/first.json" >/dev/null || {
    echo "FAIL: first repair did not expose a boolean no_op field" >&2
    cat "$WORK_DIR/first.json" >&2
    exit 1
}
jq -e '.no_op == true and .sqlite_updated == false and .qdrant_events_emitted == 0 and .documents_refreshed == 0 and (.warnings // [] | length) == 0' \
    "$WORK_DIR/second.json" >/dev/null || {
    echo "FAIL: second repair reported work or warnings" >&2
    cat "$WORK_DIR/second.json" >&2
    exit 1
}

# The second run must not alter the durable SQLite/job state or enqueue a new
# outbox/upload event. The Qdrant targeted payload must also be unchanged.
if ! cmp -s "$WORK_DIR/sqlite_after_first.json" "$WORK_DIR/sqlite_after_second.json"; then
    echo "FAIL: SQLite/job/outbox snapshot changed on second run" >&2
    diff -u "$WORK_DIR/sqlite_after_first.json" "$WORK_DIR/sqlite_after_second.json" >&2 || true
    exit 1
fi
if ! cmp -s "$WORK_DIR/qdrant_after_first.json" "$WORK_DIR/qdrant_after_second.json"; then
    echo "FAIL: targeted Qdrant payload changed on second run" >&2
    diff -u "$WORK_DIR/qdrant_after_first.json" "$WORK_DIR/qdrant_after_second.json" >&2 || true
    exit 1
fi
if ! cmp -s "$WORK_DIR/docs_after_first.json" "$WORK_DIR/docs_after_second.json"; then
    echo "FAIL: Google Doc audit changed on second run" >&2
    diff -u "$WORK_DIR/docs_after_first.json" "$WORK_DIR/docs_after_second.json" >&2 || true
    exit 1
fi

first_uploads=$(jq -c '.upload_events // []' "$WORK_DIR/sqlite_after_first.json")
second_uploads=$(jq -c '.upload_events // []' "$WORK_DIR/sqlite_after_second.json")
[[ "$first_uploads" == "$second_uploads" ]] || {
    echo "FAIL: upload event count changed on second run" >&2
    exit 1
}

sqlite_delta=$(cmp -s "$WORK_DIR/sqlite_after_first.json" "$WORK_DIR/sqlite_after_second.json" && echo 0 || echo 1)
qdrant_delta=$(cmp -s "$WORK_DIR/qdrant_after_first.json" "$WORK_DIR/qdrant_after_second.json" && echo 0 || echo 1)
document_delta=$(cmp -s "$WORK_DIR/docs_after_first.json" "$WORK_DIR/docs_after_second.json" && echo 0 || echo 1)
upload_delta=$(cmp -s <(jq -c '.upload_events' "$WORK_DIR/sqlite_after_first.json") <(jq -c '.upload_events' "$WORK_DIR/sqlite_after_second.json") && echo 0 || echo 1)

jq -n --slurpfile first "$WORK_DIR/first.json" --slurpfile second "$WORK_DIR/second.json" \
    --arg scenario drive_database_repair_idempotency \
    --argjson sqlite_delta "$sqlite_delta" --argjson qdrant_delta "$qdrant_delta" \
    --argjson document_delta "$document_delta" --argjson upload_delta "$upload_delta" \
    '{scenario:$scenario,
      job_id:$first[0].job_id,
      first_run:{no_op:$first[0].no_op,sqlite_updated:$first[0].sqlite_updated,qdrant_events_emitted:($first[0].qdrant_events_emitted // 0),documents_refreshed:$first[0].documents_refreshed},
      second_run:{no_op:$second[0].no_op,sqlite_updated:$second[0].sqlite_updated,qdrant_events_emitted:($second[0].qdrant_events_emitted // 0),documents_refreshed:$second[0].documents_refreshed},
      sqlite_delta:$sqlite_delta,qdrant_delta:$qdrant_delta,document_delta:$document_delta,upload_delta:$upload_delta,
      final:(if ($sqlite_delta + $qdrant_delta + $document_delta + $upload_delta) == 0 then "PASS" else "FAIL" end)}'
