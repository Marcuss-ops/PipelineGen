#!/usr/bin/env bash
# Recover verified scene clips to durable storage, mux with the existing tool,
# and publish a separate A/V file beside the source video's Drive job artifact.
# Requires a resolved PRE payload (scenes[].clip/stock.asset_id, drive_file_id,
# sha256). Selection-only manifests must be resolved from the media SSOT first.
# The original Drive artifact is never replaced. --local-only skips publication.
#
# Usage: materialize-scene-clips.sh --video VIDEO --pre-payload RESOLVED.json --out FINAL.mp4
# Cache: ${VELOX_SCENE_CLIPS_DIR:-${VELOX_DATA_DIR:-<repo>/data}/media/scene-clips}
set -euo pipefail
umask 077
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
VIDEO="" PRE="" OUT="" FOLDER="" LOCAL_ONLY=0
usage() { sed -n '2,10p' "${BASH_SOURCE[0]}"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --video) VIDEO="${2:?--video needs a file}"; shift 2 ;;
    --pre-payload) PRE="${2:?--pre-payload needs a file}"; shift 2 ;;
    --out) OUT="${2:?--out needs a file}"; shift 2 ;;
    --drive-folder-id) FOLDER="${2:?--drive-folder-id needs a folder ID}"; shift 2 ;;
    --local-only) LOCAL_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "materialize-scene-clips: unknown argument $1" >&2; exit 1 ;;
  esac
done
[[ -n "$VIDEO" && -n "$PRE" && -n "$OUT" ]] || { usage >&2; exit 1; }
[[ -z "$FOLDER" || "$FOLDER" =~ ^[A-Za-z0-9_-]{1,200}$ ]] || { echo "materialize-scene-clips: invalid Drive folder ID" >&2; exit 1; }
[[ -r "$VIDEO" && -r "$PRE" ]] || { echo "materialize-scene-clips: unreadable video or payload" >&2; exit 2; }
for t in jq curl sha256sum md5sum realpath; do command -v "$t" >/dev/null || { echo "materialize-scene-clips: required tool missing: $t" >&2; exit 1; }; done
VIDEO_REAL="$(realpath -- "$VIDEO")"; PRE_REAL="$(realpath -- "$PRE")"; OUT_REAL="$(realpath -m -- "$OUT")"
[[ "$VIDEO_REAL" != "$OUT_REAL" && "$PRE_REAL" != "$OUT_REAL" ]] || { echo "materialize-scene-clips: output must differ from input video and payload" >&2; exit 1; }
[[ ! -e "$OUT" || ( ! "$VIDEO" -ef "$OUT" && ! "$PRE" -ef "$OUT" ) ]] || { echo "materialize-scene-clips: output must not alias the input video or payload" >&2; exit 1; }
jq -e '.scenes | type == "array" and length > 0' "$PRE" >/dev/null || { echo "materialize-scene-clips: payload needs non-empty scenes" >&2; exit 2; }
DATA="${VELOX_DATA_DIR:-$REPO_ROOT/data}"; [[ "$DATA" == /* ]] || DATA="$REPO_ROOT/$DATA"
CACHE="${VELOX_SCENE_CLIPS_DIR:-$DATA/media/scene-clips}"; [[ "$CACHE" == /* ]] || CACHE="$REPO_ROOT/$CACHE"
CACHE_REAL="$(realpath -m -- "$CACHE")"
DATA_TMP_REAL="$(realpath -m -- "$DATA/tmp")"
REPO_TMP_REAL="$(realpath -m -- "$REPO_ROOT/data/tmp")"
case "$CACHE_REAL" in
  "$DATA_TMP_REAL"|"$DATA_TMP_REAL"/*|"$REPO_TMP_REAL"|"$REPO_TMP_REAL"/*)
    echo "materialize-scene-clips: scene clip cache must be outside disposable data/tmp" >&2; exit 1 ;;
esac
mkdir -p "$CACHE" "$(dirname "$OUT")"

# Resolve each field clip-first then stock. Top-level selection-only asset IDs
# are deliberately excluded; no DB/M2M lookup or guessing is performed here.
mapfile -t ROWS < <(jq -c '.scenes[] |
  ([.clip,.stock] | map(select((.asset_id // "") != "")) | .[0] // {}) as $source |
  ($source.asset_id // "") as $id |
  ([.clip,.stock] | map(select((.asset_id // "") == $id and $id != ""))) as $matching |
  [$id,
   ([$matching[].drive_file_id] | map(select(type == "string" and length > 0)) | .[0] // ""),
   ([$matching[].sha256] | map(select(type == "string" and length > 0)) | .[0] // "")]' "$PRE")
IDS=() SHAS=() DRIVE=(); declare -A INDEX=()
for row in "${ROWS[@]}"; do
  id="$(jq -r '.[0]' <<<"$row")"; did="$(jq -r '.[1]' <<<"$row")"; sha="$(jq -r '.[2]' <<<"$row")"
  [[ "$id" =~ ^[A-Za-z0-9:_-]{1,200}$ && "$sha" =~ ^[A-Fa-f0-9]{64}$ ]] || { echo "materialize-scene-clips: unresolved scene identity or SHA-256 for '$id'" >&2; exit 2; }
  [[ -z "$did" || "$did" =~ ^[A-Za-z0-9_-]{1,200}$ ]] || { echo "materialize-scene-clips: invalid Drive ID for $id" >&2; exit 2; }
  sha="${sha,,}"
  if [[ -n "${INDEX[$id]+x}" ]]; then
    i="${INDEX[$id]}"; [[ "${SHAS[$i]}" == "$sha" && ( -z "$did" || -z "${DRIVE[$i]}" || "$did" == "${DRIVE[$i]}" ) ]] || { echo "materialize-scene-clips: conflicting source metadata for $id" >&2; exit 2; }
    [[ -z "$did" || -n "${DRIVE[$i]}" ]] || DRIVE[$i]="$did"
  else INDEX["$id"]="${#IDS[@]}"; IDS+=("$id"); SHAS+=("$sha"); DRIVE+=("$did"); fi
done

SOURCES=("$CACHE" "$DATA/tmp/materialized/assets" "$DATA/tmp/audioassets/assets" "$DATA/tmp/localization")
[[ "$DATA" == "$REPO_ROOT/data" ]] || SOURCES+=("$REPO_ROOT/data/tmp/materialized/assets" "$REPO_ROOT/data/tmp/audioassets/assets" "$REPO_ROOT/data/tmp/localization")
SUFFIXES=(.mp4 .en.mp4 .m4a .wav)
WORK="" PART=""; cleanup(){ [[ -z "$PART" ]] || rm -f -- "$PART"; [[ -z "$WORK" ]] || rm -rf -- "$WORK"; }; trap cleanup EXIT
new_part(){ PART="$(mktemp "$CACHE/.materialize.XXXXXX")"; mv -- "$PART" "$PART.part"; PART="$PART.part"; }
find_cache(){
  local id="$1" expected="$2" d s f actual
  FOUND=""
  for d in "${SOURCES[@]}"; do [[ -d "$d" ]] || continue; for s in "${SUFFIXES[@]}"; do
    f="$d/$id$s"; [[ -f "$f" ]] || continue; actual="$(sha256sum -- "$f" | cut -d' ' -f1)"
    if [[ "${actual,,}" == "$expected" ]]; then FOUND="$f"; return 0; fi
    echo "materialize-scene-clips: skipping hash-mismatched cache entry $f" >&2
  done; done; return 1
}
MISSING=() MISSING_SHA=() MISSING_DRIVE=() COPY_FROM=() COPY_ID=()
for i in "${!IDS[@]}"; do
  id="${IDS[$i]}" sha="${SHAS[$i]}" dest="$CACHE/${IDS[$i]}.mp4"
  if [[ -f "$dest" ]]; then
    actual="$(sha256sum -- "$dest" | cut -d' ' -f1)"
    if [[ "${actual,,}" == "$sha" ]]; then echo "materialize-scene-clips: cache hit $id"; continue; fi
    echo "materialize-scene-clips: ignoring hash-mismatched cache entry $dest" >&2
  fi
  if find_cache "$id" "$sha"; then COPY_FROM+=("$FOUND"); COPY_ID+=("$id"); continue; fi
  [[ -n "${DRIVE[$i]}" ]] || { echo "materialize-scene-clips: no verified cache or Drive source for $id" >&2; exit 2; }
  MISSING+=("$id"); MISSING_SHA+=("$sha"); MISSING_DRIVE+=("${DRIVE[$i]}")
done

# Promote verified legacy-suffix cache hits before deciding whether OAuth is needed.
for n in "${!COPY_ID[@]}"; do
  id="${COPY_ID[$n]}"; i="${INDEX[$id]}"; new_part
  if cp -- "${COPY_FROM[$n]}" "$PART" && [[ "$(sha256sum -- "$PART" | cut -d' ' -f1)" == "${SHAS[$i]}" ]]; then
    mv -f -- "$PART" "$CACHE/$id.mp4"; PART=""; echo "materialize-scene-clips: recovered verified cache $id"
  elif [[ -n "${DRIVE[$i]}" ]]; then
    rm -f "$PART"; PART=""; MISSING+=("$id"); MISSING_SHA+=("${SHAS[$i]}"); MISSING_DRIVE+=("${DRIVE[$i]}")
  else
    echo "materialize-scene-clips: cache changed and no Drive fallback for $id" >&2; exit 2
  fi
done

# OAuth is needed only for Drive recovery or the default publish operation.
CREDS="${VELOX_DRIVE_CREDENTIALS_FILE:-/home/pierone/.config/velox/credentials.json}"
TOKEN="${VELOX_DRIVE_TOKEN_FILE:-/home/pierone/.config/velox/token.json}"
ACCESS=""
if [[ ${#MISSING[@]} -gt 0 || "$LOCAL_ONLY" != 1 ]]; then
  [[ -r "$CREDS" && -r "$TOKEN" ]] || { echo "materialize-scene-clips: Drive OAuth files are required" >&2; exit 4; }
  cid="$(jq -r '.installed.client_id // .web.client_id // empty' "$CREDS")"; csecret="$(jq -r '.installed.client_secret // .web.client_secret // empty' "$CREDS")"; refresh="$(jq -r '.refresh_token // empty' "$TOKEN")"
  [[ -n "$cid" && -n "$csecret" && -n "$refresh" ]] || { echo "materialize-scene-clips: incomplete Drive OAuth files" >&2; exit 4; }
  form="$(jq -rn --arg id "$cid" --arg secret "$csecret" --arg refresh "$refresh" '"client_id=\($id|@uri)&client_secret=\($secret|@uri)&refresh_token=\($refresh|@uri)&grant_type=refresh_token"')"
  token_json="$(printf '%s' "$form" | curl -fSs --max-time 30 -X POST https://oauth2.googleapis.com/token -H 'Content-Type: application/x-www-form-urlencoded' --data-binary @-)" || { echo "materialize-scene-clips: OAuth refresh failed" >&2; exit 4; }
  ACCESS="$(jq -r '.access_token // empty' <<<"$token_json")"; [[ -n "$ACCESS" ]] || { echo "materialize-scene-clips: OAuth response lacks access token" >&2; exit 4; }
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/materialize-scene-clips.XXXXXX")"

  drive_get(){
    local id="$1" dest="$2" hdr code loc host
    hdr="$(mktemp "$WORK/headers.XXXXXX")"
    if ! code="$(curl -sS --max-time 300 -w '%{http_code}' "https://www.googleapis.com/drive/v3/files/$id?alt=media" -H "Authorization: Bearer $ACCESS" -D "$hdr" -o "$dest")"; then rm -f "$hdr"; return 1; fi
    if [[ "$code" =~ ^3[0-9][0-9]$ ]]; then
      loc="$(awk 'tolower($1)=="location:" {sub(/\r$/, "", $2); v=$2} END {print v}' "$hdr")"; host="${loc#https://}"; host="${host%%/*}"; host="${host%%:*}"
      [[ "$loc" == https://* ]] || { rm -f "$hdr"; return 1; }
      case "$host" in www.googleapis.com|drive.google.com|drive.usercontent.google.com|*.googleapis.com|*.googleusercontent.com) ;; *) rm -f "$hdr"; echo "materialize-scene-clips: refusing untrusted Drive redirect" >&2; return 1 ;; esac
      curl -fSs --max-time 300 "$loc" -o "$dest" || { rm -f "$hdr"; return 1; }
    elif [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then rm -f "$hdr"; return 1; fi
    rm -f "$hdr"
  }

  for n in "${!MISSING[@]}"; do
    id="${MISSING[$n]}"; new_part; drive_get "${MISSING_DRIVE[$n]}" "$PART" || { echo "materialize-scene-clips: Drive download failed for $id" >&2; exit 3; }
    actual="$(sha256sum -- "$PART" | cut -d' ' -f1)"; [[ "${actual,,}" == "${MISSING_SHA[$n]}" ]] || { echo "materialize-scene-clips: SHA-256 mismatch for $id" >&2; exit 3; }
    mv -f "$PART" "$CACHE/$id.mp4"; PART=""; echo "materialize-scene-clips: downloaded and verified $id"
  done
fi

"$SCRIPT_DIR/mux-final-audio.sh" --video "$VIDEO" --pre-payload "$PRE" --clips-dir "$CACHE" --out "$OUT"
"$SCRIPT_DIR/mux-final-audio.sh" --verify "$OUT"
[[ "$LOCAL_ONLY" == 1 ]] && { echo "materialize-scene-clips: local-only output $OUT"; exit 0; }

SHA="$(sha256sum -- "$OUT" | cut -d' ' -f1)"; MD5="$(md5sum -- "$OUT" | cut -d' ' -f1)"; SIZE="$(wc -c < "$OUT" | tr -d '[:space:]')"; NAME="$SHA.mp4"
API=https://www.googleapis.com/drive/v3/files; UPLOAD_API=https://www.googleapis.com/upload/drive/v3/files
if [[ -z "$FOLDER" ]]; then
  # Remote publication uses the source video's SHA as its .f4v name but stores
  # MP4 bytes. Require the exact size + MD5 and unique parent before using it.
  VIDEO_SHA="$(sha256sum -- "$VIDEO" | cut -d' ' -f1)"; VIDEO_MD5="$(md5sum -- "$VIDEO" | cut -d' ' -f1)"; VIDEO_SIZE="$(wc -c < "$VIDEO" | tr -d '[:space:]')"
  listing="$(curl -fSs --max-time 30 -G "$API" --data-urlencode "q=trashed=false and name='$VIDEO_SHA.f4v'" --data-urlencode 'fields=files(id,name,size,md5Checksum,parents),nextPageToken' --data-urlencode pageSize=1000 --data-urlencode spaces=drive -H "Authorization: Bearer $ACCESS")" || { echo "materialize-scene-clips: source video Drive lookup failed; pass --drive-folder-id" >&2; exit 5; }
  source_matches="$(jq -c --arg sha "$VIDEO_SHA" --arg md5 "$VIDEO_MD5" --arg size "$VIDEO_SIZE" '[.files[]? | select(.name == ($sha+".f4v") and .size == $size and ((.md5Checksum // "" | ascii_downcase) == ($md5 | ascii_downcase)))]' <<<"$listing")"
  source_count="$(jq 'length' <<<"$source_matches")"
  [[ "$source_count" == 1 && -z "$(jq -r '.nextPageToken // empty' <<<"$listing")" ]] || { echo "materialize-scene-clips: source Drive video is missing, mismatched or ambiguous; pass --drive-folder-id" >&2; exit 5; }
  source_id="$(jq -r '.[0].id // empty' <<<"$source_matches")"
  FOLDER="$(jq -r '.[0].parents[0] // empty' <<<"$source_matches")"
  parent_count="$(jq -r '.[0].parents // [] | length' <<<"$source_matches")"
  [[ "$source_id" =~ ^[A-Za-z0-9_-]{1,200}$ && "$FOLDER" =~ ^[A-Za-z0-9_-]{1,200}$ && "$parent_count" == 1 ]] || { echo "materialize-scene-clips: source video must have exactly one valid job folder parent" >&2; exit 5; }
  drive_get "$source_id" "$WORK/source-video.mp4" || { echo "materialize-scene-clips: could not read source video for identity check" >&2; exit 5; }
  [[ "$(sha256sum -- "$WORK/source-video.mp4" | cut -d' ' -f1)" == "$VIDEO_SHA" ]] || { echo "materialize-scene-clips: Drive source video SHA-256 does not match the local input" >&2; exit 5; }
fi
[[ "$FOLDER" =~ ^[A-Za-z0-9_-]{1,200}$ ]] || { echo "materialize-scene-clips: invalid/missing job folder ID" >&2; exit 5; }  listing="$(curl -fSs --max-time 30 -G "$API" --data-urlencode "q=trashed=false and '$FOLDER' in parents and name='$NAME'" --data-urlencode 'fields=files(id,size,mimeType,md5Checksum,parents),nextPageToken' --data-urlencode pageSize=1000 --data-urlencode spaces=drive -H "Authorization: Bearer $ACCESS")" || { echo "materialize-scene-clips: output Drive lookup failed" >&2; exit 5; }

[[ "$(jq -r '.nextPageToken // empty' <<<"$listing")" == "" ]] || { echo "materialize-scene-clips: output lookup is incomplete; refusing publication" >&2; exit 5; }
count="$(jq -r '.files // [] | length' <<<"$listing")"; [[ "$count" -le 1 ]] || { echo "materialize-scene-clips: duplicate output entries; refusing publication" >&2; exit 5; }
if [[ "$count" == 1 ]]; then
  file_id="$(jq -r '.files[0].id // empty' <<<"$listing")"; size="$(jq -r '.files[0].size // empty' <<<"$listing")"; mime="$(jq -r '.files[0].mimeType // empty' <<<"$listing")"; md5="$(jq -r '.files[0].md5Checksum // empty' <<<"$listing")"
  parent_match="$(jq -r --arg p "$FOLDER" '(.files[0].parents // []) | index($p) != null' <<<"$listing")"
  [[ "$file_id" =~ ^[A-Za-z0-9_-]{1,200}$ && "$size" == "$SIZE" && "$mime" == 'video/mp4' && "${md5,,}" == "${MD5,,}" && "$parent_match" == true ]] || { echo "materialize-scene-clips: same-name Drive file differs; refusing replacement" >&2; exit 5; }
  drive_get "$file_id" "$WORK/existing.mp4" || { echo "materialize-scene-clips: cannot verify existing output" >&2; exit 5; }
  [[ "$(sha256sum "$WORK/existing.mp4" | cut -d' ' -f1)" == "$SHA" ]] || { echo "materialize-scene-clips: existing file SHA mismatch" >&2; exit 5; }
  echo "materialize-scene-clips: identical A/V file already published: $file_id"
else
  meta="$(jq -cn --arg n "$NAME" --arg p "$FOLDER" '{name:$n,mimeType:"video/mp4",parents:[$p]}')"
  code="$(curl -sS --max-time 30 -X POST "$UPLOAD_API?uploadType=resumable&fields=id" -H "Authorization: Bearer $ACCESS" -H 'Content-Type: application/json; charset=UTF-8' -H 'X-Upload-Content-Type: video/mp4' -H "X-Upload-Content-Length: $SIZE" -D "$WORK/upload-headers" -o "$WORK/create.json" -w '%{http_code}' --data-binary "$meta")" || { echo "materialize-scene-clips: upload session creation failed" >&2; exit 5; }
  [[ "$code" =~ ^2[0-9][0-9]$ ]] || { echo "materialize-scene-clips: upload session HTTP $code" >&2; exit 5; }
  url="$(awk 'tolower($1)=="location:" {sub(/\r$/, "", $2); v=$2} END {print v}' "$WORK/upload-headers")"; [[ "$url" =~ ^https://www\.googleapis\.com/ ]] || { echo "materialize-scene-clips: untrusted upload session URL" >&2; exit 5; }
  # The resumable session URL itself is a bearer secret; don't forward OAuth credentials.
  code="$(curl -sS --max-time 1800 -X PUT "$url" -H 'Content-Type: video/mp4' -H "Content-Length: $SIZE" -o "$WORK/result.json" -w '%{http_code}' --upload-file "$OUT")" || { echo "materialize-scene-clips: A/V upload failed" >&2; exit 5; }
  [[ "$code" =~ ^2[0-9][0-9]$ ]] || { echo "materialize-scene-clips: upload HTTP $code" >&2; exit 5; }
  file_id="$(jq -r '.id // empty' "$WORK/result.json")"; [[ "$file_id" =~ ^[A-Za-z0-9_-]{1,200}$ ]] || { echo "materialize-scene-clips: upload response has no file ID" >&2; exit 5; }
  meta="$(curl -fSs --max-time 30 "$API/$file_id?fields=id,name,size,mimeType,parents,md5Checksum" -H "Authorization: Bearer $ACCESS")" || { echo "materialize-scene-clips: uploaded metadata lookup failed" >&2; exit 5; }
  meta_md5="$(jq -r '.md5Checksum // empty' <<<"$meta")"
  [[ "$(jq -r '.id // empty' <<<"$meta")" == "$file_id" && "$(jq -r '.name // empty' <<<"$meta")" == "$NAME" && "$(jq -r '.size // empty' <<<"$meta")" == "$SIZE" && "$(jq -r '.mimeType // empty' <<<"$meta")" == 'video/mp4' && "$(jq -r --arg p "$FOLDER" '(.parents // []) | index($p) != null' <<<"$meta")" == true && "${meta_md5,,}" == "${MD5,,}" ]] || { echo "materialize-scene-clips: uploaded metadata verification failed" >&2; exit 5; }
  drive_get "$file_id" "$WORK/uploaded.mp4" || { echo "materialize-scene-clips: uploaded bytes could not be reread" >&2; exit 5; }
  [[ "$(sha256sum "$WORK/uploaded.mp4" | cut -d' ' -f1)" == "$SHA" ]] || { echo "materialize-scene-clips: uploaded SHA-256 verification failed" >&2; exit 5; }
  echo "materialize-scene-clips: published new A/V file id=$file_id parent=$FOLDER sha256=$SHA"
fi
echo "materialize-scene-clips: OK — local output $OUT"
