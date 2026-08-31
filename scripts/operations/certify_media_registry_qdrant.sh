#!/usr/bin/env bash
# Read-only final certification for the SQLite registry and the active Qdrant
# asset projection. It deliberately fails closed: a reachable service with a
# stale alias or incomplete registry is not a PASS.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=../lib/canonical_db_path.sh
source "$PROJECT_ROOT/scripts/lib/canonical_db_path.sh"
DB_PATH="$(canonical_primary_db_path "$PROJECT_ROOT")"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
ALIAS="${QDRANT_ALIAS:-media_assets_current}"
EXPECTED_COLLECTION="${EXPECTED_COLLECTION:-media_assets}"
EXPECTED_MODEL="${EXPECTED_MODEL:-intfloat/multilingual-e5-base}"
EXPECTED_REVISION="${EXPECTED_REVISION:-2026-06-26-v1}"
EXPECTED_CONTRACT_HASH="${EXPECTED_CONTRACT_HASH:-0223c229f0657f67a0c51a75b7190bdc7090e25745de47d78b8578efaab493f8}"

fail=0
pass() { printf 'PASS %s\n' "$1"; }
fail_check() { printf 'FAIL %s\n' "$1"; fail=1; }

if [[ ! -f "$DB_PATH" ]]; then
	printf 'FAIL database_missing path=%s\n' "$DB_PATH"
	exit 2
fi

incomplete=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE lifecycle_state IN ('ACTIVE','PUBLISHED') AND (COALESCE(namespace,'')='' OR COALESCE(asset_kind,'')='' OR COALESCE(source_type,'')='');")
invalid_taxonomy=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE lifecycle_state IN ('ACTIVE','PUBLISHED') AND NOT ((media_type='video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video')) OR (media_type='image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic')) OR (media_type='audio' AND asset_kind IN ('voiceover','bgm','sfx','clip_audio','final_audio')) OR (media_type='text' AND asset_kind='metadata') OR (media_type='document' AND asset_kind='document') OR media_type='folder');")
duplicates=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM (SELECT source_type, source_uri FROM media_asset_sources GROUP BY source_type, source_uri HAVING COUNT(DISTINCT asset_id)>1);")
# Search eligibility is taxonomy-derived, not inferred from the presence of a
# legacy embedding_json blob. Registered audio/document/text rows must not be
# counted as semantic-searchable, and legacy media_type=clip rows are not
# canonical until their taxonomy is repaired.
eligible_where="lifecycle_state IN ('ACTIVE','PUBLISHED') AND (deleted_at IS NULL OR deleted_at = '') AND ((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video')) OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic')))"
eligible=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE $eligible_where;")
embedding_contract_mismatches=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM media_assets WHERE $eligible_where AND (COALESCE(json_extract(metadata_json,'\$.embedding_model'),'') != '$EXPECTED_MODEL' OR COALESCE(json_extract(metadata_json,'\$.embedding_model_version'),'') != '$EXPECTED_REVISION' OR ('$EXPECTED_CONTRACT_HASH' != '' AND COALESCE(json_extract(metadata_json,'\$.embedding_contract_hash'),'') != '$EXPECTED_CONTRACT_HASH'));" )

printf 'IDENTITY incomplete_active=%s invalid_taxonomy=%s duplicate_source_identities=%s\n' "$incomplete" "$invalid_taxonomy" "$duplicates"
(( incomplete == 0 )) && pass "all active/published assets have namespace, asset_kind, source_type" || fail_check "canonical identity fields incomplete: $incomplete"
(( invalid_taxonomy == 0 )) && pass "all active/published taxonomies are valid" || fail_check "invalid taxonomy combinations: $invalid_taxonomy"
(( duplicates == 0 )) && pass "duplicate canonical source identities = 0" || fail_check "duplicate canonical source identities: $duplicates"
(( embedding_contract_mismatches == 0 )) && pass "embedding contract mismatches = 0" || fail_check "embedding contract mismatches: $embedding_contract_mismatches"

health=$(curl -fsS --max-time 5 "$QDRANT_URL/healthz") || {
	fail_check "Qdrant health unavailable"
	health=''
}
if [[ -n "$health" ]]; then
	alias_json=$(curl -fsS --max-time 5 "$QDRANT_URL/aliases") || alias_json=''
	active=$(jq -r --arg alias "$ALIAS" '.result.aliases[]? | select(.alias_name==$alias) | .collection_name' <<<"$alias_json" | head -n1)
	if [[ -z "$active" ]]; then
		fail_check "active alias missing: $ALIAS"
	else
		info=$(curl -fsS --max-time 5 "$QDRANT_URL/collections/$active") || info=''
		points=$(jq -r '.result.points_count // -1' <<<"$info")
		printf 'QDRANT alias=%s collection=%s points=%s sqlite_eligible=%s\n' "$ALIAS" "$active" "$points" "$eligible"
		[[ "$active" == "$EXPECTED_COLLECTION" ]] && pass "active collection is the sole production collection" || fail_check "active collection mismatch: $active (want $EXPECTED_COLLECTION)"
		(( points >= 0 && points == eligible )) && pass "missing eligible Qdrant assets = 0" || fail_check "Qdrant/SQLite count mismatch: points=$points eligible=$eligible"

		# A count match is insufficient: verify the canonical asset_id set,
		# uniqueness, and observed embedding revision in every projected point.
		scroll=$(curl -fsS --max-time 10 -X POST "$QDRANT_URL/collections/$active/points/scroll" \
			-H 'content-type: application/json' \
			-d '{"limit":1000,"with_payload":true,"with_vector":false}') || scroll=''
		if [[ -z "$scroll" ]]; then
			fail_check "Qdrant point scroll unavailable"
		else
			tmpdir=$(mktemp -d)
			trap 'rm -rf "$tmpdir"' EXIT
			jq -r '.result.points[]?.payload.asset_id // empty' <<<"$scroll" | sort >"$tmpdir/qdrant_ids"
			sqlite3 "$DB_PATH" "SELECT id FROM media_assets WHERE $eligible_where ORDER BY id;" >"$tmpdir/sqlite_ids"
			q_duplicate_ids=$(uniq -d "$tmpdir/qdrant_ids" | wc -l | tr -d ' ')
			missing_ids=$(comm -23 "$tmpdir/sqlite_ids" "$tmpdir/qdrant_ids" | wc -l | tr -d ' ')
			orphan_ids=$(comm -13 "$tmpdir/sqlite_ids" "$tmpdir/qdrant_ids" | wc -l | tr -d ' ')
			(( q_duplicate_ids == 0 )) && pass "duplicate Qdrant asset IDs = 0" || fail_check "duplicate Qdrant asset IDs: $q_duplicate_ids"
			(( missing_ids == 0 )) && pass "missing eligible Qdrant assets = 0 (set comparison)" || fail_check "missing eligible Qdrant assets: $missing_ids"
			(( orphan_ids == 0 )) && pass "orphan Qdrant points = 0" || fail_check "orphan Qdrant points: $orphan_ids"
			point_contract_mismatches=$(jq --arg rev "$EXPECTED_REVISION" '[.result.points[]? | select((.payload.embedding_version_text // "") != $rev)] | length' <<<"$scroll")
			(( point_contract_mismatches == 0 )) && pass "Qdrant observed embedding revision matches" || fail_check "Qdrant embedding revision mismatches: $point_contract_mismatches"
			point_hash_mismatches=$(jq --arg hash "$EXPECTED_CONTRACT_HASH" '[.result.points[]? | select((.payload.embedding_contract_hash // "") != $hash)] | length' <<<"$scroll")
			(( point_hash_mismatches == 0 )) && pass "Qdrant embedding contract hash matches" || fail_check "Qdrant embedding contract hash mismatches: $point_hash_mismatches"
		fi
	fi
fi

if (( fail != 0 )); then
	printf 'CERTIFICATION NOT CERTIFIED\n'
	exit 1
fi
printf 'CERTIFICATION CERTIFIED\n'
