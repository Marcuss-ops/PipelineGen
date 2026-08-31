#!/usr/bin/env bash
# Specialist persistence/document assertions.

generate_assert_drive() {
    local result="$1" script_id db_path doc_link
    script_id=$(jq -r '.script_id // empty' <<<"$result")
    db_path="${SMOKE_DB_PATH:?SMOKE_DB_PATH must be explicitly set to an isolated or approved database}"
    [[ -f "$db_path" ]] || { echo "FAIL: SQLite database not found: $db_path" >&2; return 1; }
    [[ "$script_id" =~ ^[0-9]+$ && "$script_id" -gt 0 ]] || { echo "FAIL: API did not return a valid script_id: $script_id" >&2; return 1; }
    local row
    row=$(sqlite3 -json "$db_path" "SELECT id,title,language,status,final_word_count,idempotency_key,narrative_text,full_document,specscene,manifest_v2 FROM scripts WHERE id=$script_id LIMIT 1;") || { echo "FAIL: could not query SQLite scripts table" >&2; return 1; }
    jq -e --argjson script_id "$script_id" 'length==1 and .[0].id==$script_id and .[0].status=="completed" and (.[0].language|type=="string" and length>0) and (.[0].final_word_count|type=="number" and .>0) and (.[0].idempotency_key|type=="string" and length>0) and ((.[0].narrative_text // .[0].full_document // "")|length>0) and (.[0].specscene|fromjson|(.version==1 and (.scenes|length>0) and all(.scenes[]; (.id|type=="string" and length>0) and (.text|type=="string" and length>0)))) and (.[0].manifest_v2|fromjson|((.items|length>0) and .no_inline_assets==true))' <<<"$row" >/dev/null || { echo "FAIL: persisted scripts row is incomplete" >&2; jq . <<<"$row" >&2; return 1; }
    local persisted_payload
    persisted_payload=$(jq -r '.[0] | [(.narrative_text // ""), (.full_document // ""), (.specscene // ""), (.manifest_v2 // "")] | join("\\n")' <<<"$row") || { echo "FAIL: could not extract persisted payload" >&2; jq . <<<"$row" >&2; return 1; }
    if grep -Eq 'OLD_ID|DELETED_ID|BROKEN_LINK' <<<"$persisted_payload"; then
        echo "FAIL: stale or unusable Drive marker persisted in scripts/narrative/specscene/manifest_v2" >&2
        grep -En 'OLD_ID|DELETED_ID|BROKEN_LINK' <<<"$persisted_payload" >&2 || true
        jq . <<<"$row" >&2
        return 1
    fi
    doc_link=$(jq -r '.artifacts.document.doc_link // .documents.it.link // empty' <<<"$result" 2>/dev/null | grep -m1 'docs.google.com' || true)
    if [[ -z "$doc_link" ]]; then
        for _ in 1 2 3 4 5; do
            sleep 1
            smoke_curl GET "/api/jobs/${GENERATE_JOB_ID}/full" >/dev/null
            GENERATE_FULL_BODY="$SMOKE_LAST_BODY"
            GENERATE_RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$GENERATE_FULL_BODY")
            doc_link=$(jq -r '.artifacts.document.doc_link // .documents.it.link // empty' <<<"$GENERATE_RESULT" 2>/dev/null | grep -m1 'docs.google.com' || true)
            [[ -n "$doc_link" ]] && break
        done
    fi
    [[ -n "$doc_link" ]] || { echo "FAIL: docs.enabled=true but no Google Doc artifact was returned" >&2; return 1; }
    printf 'Drive assertions: SQLite row and Google Doc validated\n'
}
