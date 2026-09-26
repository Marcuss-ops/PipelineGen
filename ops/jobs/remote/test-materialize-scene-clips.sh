#!/usr/bin/env bash
# Hermetic tests for materialize-scene-clips.sh. No real Drive requests.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for tool in ffmpeg ffprobe jq sha256sum md5sum; do
  command -v "$tool" >/dev/null 2>&1 || { echo "test-materialize-scene-clips: missing $tool" >&2; exit 1; }
done
TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT
mkdir -p "$TMP/mock" "$TMP/oauth" "$TMP/data/tmp/audioassets/assets" "$TMP/out"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'color=c=black:s=320x240:r=24:d=2' -c:v libx264 -pix_fmt yuv420p -an "$TMP/video.mp4"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'sine=frequency=440:sample_rate=48000:duration=2' -c:a aac -profile:a aac_low -b:a 128k -ar 48000 -ac 2 "$TMP/source.m4a"
SOURCE_SHA="$(sha256sum "$TMP/source.m4a" | cut -d' ' -f1)"
jq -n '{installed:{client_id:"fixture-client",client_secret:"fixture-secret"}}' > "$TMP/oauth/credentials.json"
jq -n '{refresh_token:"fixture-refresh"}' > "$TMP/oauth/token.json"
jq -n --arg sha "$SOURCE_SHA" '{scenes:[{duration_seconds:2,clip:{asset_id:"drive_asset",drive_file_id:"drive-id-1",sha256:$sha}}]}' > "$TMP/resolved.json"

# Mock OAuth, Drive download, source-video identity lookup, resumable upload,
# metadata lookup, and byte reread. All mock requests remain inside this process.
curl() {
  local url="" output="" headers="" upload_file="" data="" code=200 source_query=0 auth_header=0
  local -a args=("$@")
  for ((i=0; i<${#args[@]}; i++)); do
    case "${args[$i]}" in
      https://*) url="${args[$i]}" ;;
      -o) output="${args[$((i+1))]}" ;;
      -D) headers="${args[$((i+1))]}" ;;
      --upload-file) upload_file="${args[$((i+1))]}" ;;
      --data-binary) data="${args[$((i+1))]}" ;;
      -H) [[ "${args[$((i+1))]}" == Authorization:* ]] && auth_header=1 ;;
    esac
    [[ "${args[$i]}" == *".f4v'"* ]] && source_query=1
  done
  case "$url" in
    https://oauth2.googleapis.com/token) printf '{"access_token":"fixture-access"}' ;;
    https://www.googleapis.com/drive/v3/files/drive-id-1\?alt=media)
      if [[ "${MOCK_BAD_REDIRECT:-0}" == 1 ]]; then
        printf 'HTTP/1.1 302 Found\r\nLocation: https://evil.example/file\r\n\r\n' > "$headers"
      else
        printf 'HTTP/1.1 302 Found\r\nLocation: https://drive.google.com/uc?export=download\r\n\r\n' > "$headers"
      fi
      code=302 ;;
    https://drive.google.com/uc\?*)
      [[ "$auth_header" == 0 ]] || { echo 'OAuth header forwarded to redirect' >&2; return 1; }
      if [[ "${MOCK_BAD_BYTES:-0}" == 1 ]]; then printf wrong > "$output"; else cp "$MOCK_SOURCE" "$output"; fi ;;
    https://www.googleapis.com/drive/v3/files)
      if [[ "$source_query" == 1 && "${MOCK_SOURCE_LOOKUP:-0}" == 1 ]]; then
        name="$(sha256sum "$MOCK_VIDEO" | cut -d' ' -f1).f4v"
        size="$(wc -c < "$MOCK_VIDEO" | tr -d '[:space:]')"
        md5="$(md5sum "$MOCK_VIDEO" | cut -d' ' -f1)"
        if [[ "${MOCK_SOURCE_NO_MATCH:-0}" == 1 ]]; then
          printf '{"files":[]}'
        elif [[ "${MOCK_SOURCE_AMBIGUOUS:-0}" == 1 ]]; then
          jq -cn --arg name "$name" --arg size "$size" --arg md5 "$md5" \
            '{files:[{id:"fixture-source-a",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]},{id:"fixture-source-b",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]}]}'
        else
          parent='fixture-job-folder'
          [[ "${MOCK_SOURCE_NO_PARENT:-0}" == 0 ]] || parent=''
          [[ "${MOCK_SOURCE_MISMATCH:-0}" == 0 ]] || { size=1; md5=wrong; }
          jq -cn --arg name "$name" --arg size "$size" --arg md5 "$md5" --arg parent "$parent" --arg paged "${MOCK_SOURCE_PAGED:-0}" \
            '{files:[{id:"fixture-source",name:$name,size:$size,md5Checksum:$md5,parents:(if $parent == "" then [] else [$parent] end)}],nextPageToken:(if $paged == "1" then "next-page" else null end)}'
        fi
      elif [[ "${MOCK_DUPLICATE:-0}" == 1 ]]; then
        name="$(sha256sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1).mp4"
        size="$(wc -c < "$MOCK_EXISTING_FILE" | tr -d '[:space:]')"
        md5="$(md5sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1)"
        jq -cn --arg name "$name" --arg size "$size" --arg md5 "$md5" \
          '{files:[{id:"fixture-existing-a",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]},{id:"fixture-existing-b",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]}]}'
      elif [[ "${MOCK_EXISTING:-0}" == 1 && "${MOCK_LIST_PAGED:-0}" == 1 ]]; then
        name="$(sha256sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1).mp4"
        size="$(wc -c < "$MOCK_EXISTING_FILE" | tr -d '[:space:]')"
        md5="$(md5sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1)"
        jq -cn --arg name "$name" --arg size "$size" --arg md5 "$md5" \
          '{files:[{id:"fixture-existing",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]}],nextPageToken:"next-page"}'
      elif [[ "${MOCK_EXISTING:-0}" == 1 ]]; then
        name="$(sha256sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1).mp4"
        size="$(wc -c < "$MOCK_EXISTING_FILE" | tr -d '[:space:]')"
        md5="$(md5sum "$MOCK_EXISTING_FILE" | cut -d' ' -f1)"
        jq -cn --arg name "$name" --arg size "$size" --arg md5 "$md5" \
          '{files:[{id:"fixture-existing",name:$name,size:$size,md5Checksum:$md5,parents:["fixture-job-folder"]}]}'
      else printf '{"files":[]}'; fi ;;
    https://www.googleapis.com/upload/drive/v3/files\?*)
      printf '%s' "$data" > "$MOCK_ROOT/create-metadata.json"
      printf 'HTTP/1.1 200 OK\r\nLocation: https://www.googleapis.com/upload/session/fixture\r\n\r\n' > "$headers"
      printf upload >> "$MOCK_ROOT/uploads.log" ;;
    https://www.googleapis.com/upload/session/fixture)
      cp "$upload_file" "$MOCK_ROOT/uploaded.mp4"
      printf '{"id":"fixture-file"}' > "$output" ;;
    https://www.googleapis.com/drive/v3/files/fixture-source\?alt=media)
      if [[ "${MOCK_SOURCE_BAD_BYTES:-0}" == 1 ]]; then printf wrong > "$output"; else cp "$MOCK_VIDEO" "$output"; fi ;;
    https://www.googleapis.com/drive/v3/files/fixture-file\?fields=*)
      name="$(jq -r '.name' "$MOCK_ROOT/create-metadata.json")"
      parent="$(jq -r '.parents[0]' "$MOCK_ROOT/create-metadata.json")"
      size="$(wc -c < "$MOCK_ROOT/uploaded.mp4" | tr -d '[:space:]')"
      md5="$(md5sum "$MOCK_ROOT/uploaded.mp4" | cut -d' ' -f1)"
      jq -cn --arg name "$name" --arg parent "$parent" --arg size "$size" --arg md5 "$md5" \
        '{id:"fixture-file",name:$name,size:$size,mimeType:"video/mp4",parents:[$parent],md5Checksum:$md5}' ;;
    https://www.googleapis.com/drive/v3/files/fixture-file\?alt=media)
      cp "$MOCK_ROOT/uploaded.mp4" "$output" ;;
    https://www.googleapis.com/drive/v3/files/fixture-existing\?alt=media)
      cp "$MOCK_EXISTING_FILE" "$output" ;;
    *) echo "test-materialize-scene-clips: unexpected mocked URL: $url" >&2; return 1 ;;
  esac
  if [[ "$*" == *"%{http_code}"* ]]; then printf '%s' "$code"; fi
  return 0
}
export -f curl
export MOCK_ROOT="$TMP/mock" MOCK_SOURCE="$TMP/source.m4a" MOCK_VIDEO="$TMP/video.mp4"
CREDENTIALS="$TMP/oauth/credentials.json" TOKEN="$TMP/oauth/token.json"

# Selection-only manifests are rejected before any network or database lookup.
set +e
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" \
  --pre-payload "$SCRIPT_DIR/dolly5-pre.creator-77.json" --out "$TMP/out/selection.mp4" > "$TMP/selection.log" 2>&1
rc=$?
set -e
[[ "$rc" == 2 ]] && grep -q 'unresolved scene identity' "$TMP/selection.log" || { echo 'FAIL: selection-only manifest accepted' >&2; exit 1; }

# Verified temporary-cache hit migrates to durable storage; second run is offline.
LOCAL_PRE="$TMP/cache-pre.json"
jq -n --arg sha "$SOURCE_SHA" '{scenes:[{duration_seconds:2,clip:{asset_id:"cached_asset",sha256:$sha}}]}' > "$LOCAL_PRE"
cp "$TMP/source.m4a" "$TMP/data/tmp/audioassets/assets/cached_asset.m4a"
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$LOCAL_PRE" --out "$TMP/out/cache-first.mp4" > "$TMP/cache-first.log" 2>&1
test "$(sha256sum "$TMP/data/media/scene-clips/cached_asset.mp4" | cut -d' ' -f1)" = "$SOURCE_SHA"
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$LOCAL_PRE" --out "$TMP/out/cache-second.mp4" > "$TMP/cache-second.log" 2>&1
grep -q 'cache hit cached_asset' "$TMP/cache-second.log"
"$SCRIPT_DIR/mux-final-audio.sh" --verify "$TMP/out/cache-second.mp4" >/dev/null 2>&1

# Corrupt cache with no Drive identity fails closed and does not produce output.
printf corrupt > "$TMP/data/media/scene-clips/corrupt_asset.mp4"
jq -n --arg sha "$SOURCE_SHA" '{scenes:[{duration_seconds:1,clip:{asset_id:"corrupt_asset",sha256:$sha}}]}' > "$TMP/corrupt.json"
set +e
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$TMP/corrupt.json" --out "$TMP/out/corrupt.mp4" > "$TMP/corrupt.log" 2>&1
rc=$?
set -e
[[ "$rc" == 2 && ! -e "$TMP/out/corrupt.mp4" ]] || { echo 'FAIL: corrupt cache accepted' >&2; exit 1; }

# Conflicting metadata for a repeated asset ID and invalid hashes fail before mux.
jq -n --arg sha "$SOURCE_SHA" --arg other "$(printf '0%.0s' {1..64})" \
  '{scenes:[{duration_seconds:1,clip:{asset_id:"duplicate_asset",sha256:$sha}},{duration_seconds:1,clip:{asset_id:"duplicate_asset",sha256:$other}}]}' > "$TMP/conflicting.json"
set +e
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$TMP/conflicting.json" --out "$TMP/out/conflicting.mp4" > "$TMP/conflicting.log" 2>&1
rc=$?
set -e
[[ "$rc" == 2 && ! -e "$TMP/out/conflicting.mp4" ]] || { echo 'FAIL: conflicting repeated-asset metadata accepted' >&2; exit 1; }

# Output cannot alias or overwrite the source video or payload.
set +e
VELOX_DATA_DIR="$TMP/data" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$LOCAL_PRE" --out "$TMP/video.mp4" > "$TMP/output-alias.log" 2>&1
rc=$?
set -e
[[ "$rc" != 0 ]] || { echo 'FAIL: output aliased source video' >&2; exit 1; }

# A configured cache under disposable data/tmp is rejected before use.
set +e
VELOX_DATA_DIR="$TMP/data" VELOX_SCENE_CLIPS_DIR="$TMP/data/tmp/unsafe-cache" "$SCRIPT_DIR/materialize-scene-clips.sh" \
  --local-only --video "$TMP/video.mp4" --pre-payload "$LOCAL_PRE" --out "$TMP/out/unsafe-cache.mp4" > "$TMP/unsafe-cache.log" 2>&1
rc=$?
set -e
[[ "$rc" != 0 ]] && grep -q 'outside disposable data/tmp' "$TMP/unsafe-cache.log" || { echo 'FAIL: disposable cache path accepted' >&2; exit 1; }

# Drive redirect is restricted to trusted HTTPS and payload SHA before caching.
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/downloaded.mp4" > "$TMP/download.log" 2>&1
test "$(sha256sum "$TMP/data/media/scene-clips/drive_asset.mp4" | cut -d' ' -f1)" = "$SOURCE_SHA"
"$SCRIPT_DIR/mux-final-audio.sh" --verify "$TMP/out/downloaded.mp4" >/dev/null 2>&1
grep -q 'downloaded and verified drive_asset' "$TMP/download.log"

# A corrupt durable entry is never trusted; a verified Drive download replaces it.
CORRUPT_DRIVE_PRE="$TMP/corrupt-drive.json"
jq -n --arg sha "$SOURCE_SHA" '{scenes:[{duration_seconds:2,clip:{asset_id:"corrupt_drive_asset",drive_file_id:"drive-id-1",sha256:$sha}}]}' > "$CORRUPT_DRIVE_PRE"
printf stale > "$TMP/data/media/scene-clips/corrupt_drive_asset.mp4"
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$CORRUPT_DRIVE_PRE" --out "$TMP/out/corrupt-drive.mp4" > "$TMP/corrupt-drive.log" 2>&1
test "$(sha256sum "$TMP/data/media/scene-clips/corrupt_drive_asset.mp4" | cut -d' ' -f1)" = "$SOURCE_SHA"

# Untrusted redirect and wrong bytes never enter the durable cache.
for kind in redirect bytes; do
  export MOCK_BAD_REDIRECT=0 MOCK_BAD_BYTES=0
  id="bad_$kind"
  [[ "$kind" == redirect ]] && export MOCK_BAD_REDIRECT=1 || export MOCK_BAD_BYTES=1
  jq -n --arg id "$id" --arg sha "$SOURCE_SHA" '{scenes:[{duration_seconds:1,clip:{asset_id:$id,drive_file_id:"drive-id-1",sha256:$sha}}]}' > "$TMP/$id.json"
  set +e
  VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
    "$SCRIPT_DIR/materialize-scene-clips.sh" --local-only --video "$TMP/video.mp4" --pre-payload "$TMP/$id.json" --out "$TMP/out/$id.mp4" > "$TMP/$id.log" 2>&1
  rc=$?
  set -e
  [[ "$rc" != 0 && ! -e "$TMP/data/media/scene-clips/$id.mp4" ]] || { echo "FAIL: $kind failure accepted" >&2; exit 1; }
done
unset MOCK_BAD_REDIRECT MOCK_BAD_BYTES

# Automatic folder selection rejects missing, ambiguous, parentless, or byte-mismatched source identities.
export MOCK_SOURCE_LOOKUP=1
for failure in no_match ambiguous no_parent bad_bytes mismatched_metadata incomplete_listing; do
  export MOCK_SOURCE_NO_MATCH=0 MOCK_SOURCE_AMBIGUOUS=0 MOCK_SOURCE_NO_PARENT=0 MOCK_SOURCE_BAD_BYTES=0 MOCK_SOURCE_MISMATCH=0 MOCK_SOURCE_PAGED=0
  case "$failure" in
    no_match) export MOCK_SOURCE_NO_MATCH=1 ;;
    ambiguous) export MOCK_SOURCE_AMBIGUOUS=1 ;;
    no_parent) export MOCK_SOURCE_NO_PARENT=1 ;;
    bad_bytes) export MOCK_SOURCE_BAD_BYTES=1 ;;
    mismatched_metadata) export MOCK_SOURCE_MISMATCH=1 ;;
    incomplete_listing) export MOCK_SOURCE_PAGED=1 ;;
  esac
  set +e
  VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
    "$SCRIPT_DIR/materialize-scene-clips.sh" --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/source-$failure.mp4" > "$TMP/source-$failure.log" 2>&1
  rc=$?
  set -e
  [[ "$rc" == 5 ]] || { echo "FAIL: source identity failure $failure was accepted" >&2; exit 1; }
done
[[ ! -e "$TMP/mock/uploads.log" ]] || { echo 'FAIL: upload attempted before source identity was verified' >&2; exit 1; }
unset MOCK_SOURCE_NO_MATCH MOCK_SOURCE_AMBIGUOUS MOCK_SOURCE_NO_PARENT MOCK_SOURCE_BAD_BYTES MOCK_SOURCE_MISMATCH MOCK_SOURCE_PAGED

# Publish a separate content-addressed file under the verified source job folder.
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/published.mp4" > "$TMP/publish.log" 2>&1
test "$(sha256sum "$TMP/mock/uploaded.mp4" | cut -d' ' -f1)" = "$(sha256sum "$TMP/out/published.mp4" | cut -d' ' -f1)"
test "$(jq -r '.name' "$TMP/mock/create-metadata.json")" = "$(sha256sum "$TMP/out/published.mp4" | cut -d' ' -f1).mp4"
test "$(jq -r '.parents[0]' "$TMP/mock/create-metadata.json")" = fixture-job-folder
grep -q 'published new A/V file' "$TMP/publish.log"

# A same-name/different-content Drive object is never replaced or uploaded over.
export MOCK_SOURCE_LOOKUP=0 MOCK_EXISTING=1 MOCK_EXISTING_FILE="$TMP/video.mp4"
set +e
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --drive-folder-id fixture-job-folder --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/conflict.mp4" > "$TMP/conflict.log" 2>&1
rc=$?
set -e
[[ "$rc" == 5 ]] && grep -q 'same-name Drive file differs' "$TMP/conflict.log" || { echo 'FAIL: conflicting Drive file accepted' >&2; exit 1; }
test "$(wc -c < "$TMP/mock/uploads.log" | tr -d '[:space:]')" = 6

# Duplicate destination names are rejected rather than choosing one arbitrarily.
export MOCK_DUPLICATE=1 MOCK_EXISTING_FILE="$TMP/out/published.mp4"
set +e
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --drive-folder-id fixture-job-folder --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/duplicate.mp4" > "$TMP/duplicate.log" 2>&1
rc=$?
set -e
[[ "$rc" == 5 ]] && grep -q 'duplicate output entries' "$TMP/duplicate.log" || { echo 'FAIL: duplicate destination entries accepted' >&2; exit 1; }
test "$(wc -c < "$TMP/mock/uploads.log" | tr -d '[:space:]')" = 6

# A paginated destination listing is incomplete and must fail closed.
export MOCK_DUPLICATE=0 MOCK_EXISTING=1 MOCK_LIST_PAGED=1 MOCK_EXISTING_FILE="$TMP/out/published.mp4"
set +e
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --drive-folder-id fixture-job-folder --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/paged.mp4" > "$TMP/paged.log" 2>&1
rc=$?
set -e
[[ "$rc" == 5 ]] && grep -q 'output lookup is incomplete' "$TMP/paged.log" || { echo "FAIL: paged destination listing accepted (rc=$rc): $(sed -n '1,8p' "$TMP/paged.log")" >&2; exit 1; }
test "$(wc -c < "$TMP/mock/uploads.log" | tr -d '[:space:]')" = 6

# An identical existing file is verified by bytes and treated idempotently.
unset MOCK_DUPLICATE MOCK_LIST_PAGED
export MOCK_SOURCE_LOOKUP=0 MOCK_EXISTING=1 MOCK_EXISTING_FILE="$TMP/out/published.mp4"
VELOX_DATA_DIR="$TMP/data" VELOX_DRIVE_CREDENTIALS_FILE="$CREDENTIALS" VELOX_DRIVE_TOKEN_FILE="$TOKEN" \
  "$SCRIPT_DIR/materialize-scene-clips.sh" --drive-folder-id fixture-job-folder --video "$TMP/video.mp4" --pre-payload "$TMP/resolved.json" --out "$TMP/out/published-again.mp4" > "$TMP/idempotent.log" 2>&1
grep -q 'identical A/V file already published' "$TMP/idempotent.log"
test "$(wc -c < "$TMP/mock/uploads.log" | tr -d '[:space:]')" = 6
printf 'materialize-scene-clips: all hermetic tests PASS\n'
