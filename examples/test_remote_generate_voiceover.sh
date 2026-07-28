#!/usr/bin/env bash
# examples/test_remote_generate_voiceover.sh
#
# Test end-to-end da un computer remoto: submit di un payload
# GenerateVoiceoversRequest (1 item: it-IT + it-IT-DiegoNeural) su
# POST /api/media/voiceover/generate, polling fino al terminal state.
#
# Uso:
#   API_URL=https://pipeline.example.com TOKEN=il_tuo_token \
#     DRIVE_FOLDER_ID=<folder_id_drive> \
#     ./examples/test_remote_generate_voiceover.sh
#
# Companion di examples/test_remote_generate_clips.sh (clip-driven) e
# examples/test_remote_generate.sh (text-driven). Stesso protocollo
# di health-check + capabilities + polling; solo il payload cambia
# (voiceover-driven invece di topic/clip-driven). Riutilizza
# l'handling canonico del header Retry-After già presente nel rate
# limiter dopo ITEM 5 (commit feat(middleware)).
#
# ITEM 6: questo script + examples/worker_integration/main.go::runVoiceoverMode
# formano la CLI canonica per /api/media/voiceover/generate. Il worker
# Go e questo bash devono produrre lo STESSO X-Request-ID per gli
# STESSI logical inputs (itemID|locale|voice), così la dedup
# server-side (type, correlation_id) UNIQUE collassa correttamente.

set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:8000}"
TOKEN="${TOKEN:-}"
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-}"

if [[ -z "$TOKEN" ]]; then
	echo "Errore: imposta la variabile TOKEN prima di eseguire lo script." >&2
	echo "Esempio: TOKEN=il_tuo_token DRIVE_FOLDER_ID=<id> API_URL=https://pipeline.example.com $0" >&2
	exit 1
fi

if [[ -z "$DRIVE_FOLDER_ID" ]]; then
	echo "Errore: imposta la variabile DRIVE_FOLDER_ID (folder Drive di destinazione)." >&2
	echo "Esempio: DRIVE_FOLDER_ID=1AbC...xYz $0" >&2
	exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "Errore: jq è richiesto per il parsing JSON." >&2
	exit 1
fi

# Identità canonica del job voiceover (godlike/06 SSOT — 1 item
# canonico per smoke test B1, see tests/operational/voiceover_b1_smoke.sh).
ITEM_ID="vo-b1-single-it"
TEXT="Questo e un test della pipeline voiceover di PipelineGen."
LOCALE="it-IT"
VOICE="it-IT-DiegoNeural"
FILENAME="vo_b1_it.mp3"

# Idempotency dual-anchor (worker_integration main.go::runVoiceoverMode
# contract):
# X-Request-ID = MD5(itemID|locale|voice), 32 hex stable.
# Same contract as the Go worker so logical-set X-Request-IDs collide
# on the (type, correlation_id) UNIQUE anchor. Locale+voice are the
# TTS-differentiation axes — same text + different voice = different
# server job, which is the intended semantic. For 1-element locale/voice
# the sort below is idempotent, but LC_ALL=C is enforced for parity
# with the clips contract (matches Go's hashutil.MD5String on any
# LANG/LC_ALL).
LC_ALL=C SORTED_LOCALE=$(printf '%s\n' "${LOCALE}" | sort | paste -sd ',' -)
LC_ALL=C SORTED_VOICE=$(printf '%s\n' "${VOICE}" | sort | paste -sd ',' -)
X_REQUEST_ID=$(printf '%s|%s|%s' \
	"${ITEM_ID}" "${SORTED_LOCALE}" "${SORTED_VOICE}" | md5sum | awk '{print $1}')
IDEMPOTENCY_KEY="${ITEM_ID}"

echo "================================================"
echo "PipelineGen remote VOICEOVER generation test (1 item: it-IT DiegoNeural)"
echo "API_URL: $API_URL"
echo "ITEM_ID: $ITEM_ID"
echo "X_Request_ID: $X_REQUEST_ID"
echo "ITEM:"
echo "  - text:     ${TEXT}"
echo "  - locale:   ${LOCALE}"
echo "  - voice:    ${VOICE}"
echo "  - filename: ${FILENAME}"
echo "  - drive_folder_id: ${DRIVE_FOLDER_ID}"
echo "================================================"

# ── 1. Health ──────────────────────────────────────────────────────────────
echo
echo "[1/5] GET /health"
HEALTH_STATUS="$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/health" || true)"
if [[ "${HEALTH_STATUS}" == "200" ]]; then
	echo "  → /health OK (200)"
else
	echo "  → /health FALLITO (status: ${HEALTH_STATUS:-conn_error})" >&2
	exit 1
fi

# ── 2. Ready ───────────────────────────────────────────────────────────────
echo
echo "[2/5] GET /ready"
READY_STATUS="$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/ready" || true)"
if [[ "${READY_STATUS}" == "200" ]]; then
	echo "  → /ready OK (200)"
else
	echo "  → /ready FALLITO (status: ${READY_STATUS:-conn_error})" >&2
fi

# ── 3. Capabilities ────────────────────────────────────────────────────────
echo
echo "[3/5] GET /api/capabilities"
CAP_RESPONSE="$(curl -s -w "\n%{http_code}" \
	-H "Authorization: Bearer ${TOKEN}" \
	"${API_URL}/api/capabilities" || true)"
CAP_HTTP="$(echo "$CAP_RESPONSE" | tail -n1)"
CAP_BODY="$(echo "$CAP_RESPONSE" | sed '$ d')"
if [[ "${CAP_HTTP}" == "200" ]]; then
	echo "  → /api/capabilities OK (200)"
	echo "$CAP_BODY" | jq '.' || echo "$CAP_BODY"
else
	echo "  → /api/capabilities FALLITO (status: ${CAP_HTTP:-conn_error})" >&2
	echo "Risposta: $CAP_BODY" >&2
	exit 1
fi

# ── 4. Generate ────────────────────────────────────────────────────────────
echo
echo "[4/5] POST /api/media/voiceover/generate (1 item: it-IT DiegoNeural)"

# Payload canonico GenerateVoiceoversRequest (see
# internal/api/assets/voiceover/types.go). Mirror di
# tests/operational/voiceover_b1_smoke.sh::post_single_item ma con
# request_id esplicito (itemID) per garantire la collisione
# sull'idempotency-anchor del server.
PAYLOAD=$(jq -n \
	--arg rid "${ITEM_ID}" \
	--arg txt "${TEXT}" \
	--arg loc "${LOCALE}" \
	--arg voi "${VOICE}" \
	--arg fn "${FILENAME}" \
	--arg fid "${DRIVE_FOLDER_ID}" \
	'{
		request_id: $rid,
		items: [
			{
				text: $txt,
				language: $loc,
				voice: $voi,
				filename: $fn,
				required: true
			}
		],
		destination: {
			kind: "explicit",
			folder_id: $fid
		},
		options: {
			remove_silence: false,
			strategy: "verify",
			parallelism: 1
		}
	}')

POST_RESPONSE="$(curl -s -w "\n%{http_code}" \
	--request POST \
	"${API_URL}/api/media/voiceover/generate" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H "X-Request-ID: ${X_REQUEST_ID}" \
	-H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
	-H "Content-Type: application/json" \
	-H "Accept: application/json" \
	--data "${PAYLOAD}" || true)"

POST_HTTP="$(echo "$POST_RESPONSE" | tail -n1)"
POST_BODY="$(echo "$POST_RESPONSE" | sed '$ d')"

echo "  HTTP status: ${POST_HTTP:-conn_error}"

if [[ "${POST_HTTP}" != "202" ]]; then
	echo "  → /api/media/voiceover/generate FALLITO" >&2
	echo "Risposta: $POST_BODY" >&2
	if [[ "${POST_HTTP}" == "429" ]]; then
		echo "  → rate limit: aspetta Retry-After secondi (header introdotto in ITEM 5)." >&2
	elif [[ "${POST_HTTP}" == "401" ]]; then
		echo "  → token non valido o scaduto (ErrUnauthorized)." >&2
	elif [[ "${POST_HTTP}" == "400" ]]; then
		echo "  → payload malformato (ErrBadRequest) — controlla destination.kind/folder_id e items[].text non vuoto." >&2
	elif [[ "${POST_HTTP}" == "404" ]]; then
		echo "  → endpoint non trovato (ErrNotFound) — controlla che il server esponga /api/media/voiceover/generate." >&2
	fi
	exit 1
fi

JOB_ID="$(echo "$POST_BODY" | jq -r '.job_id // empty')"
if [[ -z "$JOB_ID" ]]; then
	echo "  → job_id mancante nella risposta" >&2
	echo "Risposta: $POST_BODY" >&2
	exit 1
fi

echo "  → job_id: ${JOB_ID}"

# ── 5. Poll ─────────────────────────────────────────────────────────────────
echo
echo "[5/5] Polling /api/jobs/${JOB_ID}/full ogni 3s"

MAX_ATTEMPTS=60
ATTEMPT=0
FINAL_STATUS=""
while (( ATTEMPT < MAX_ATTEMPTS )); do
	POLL_RESPONSE="$(curl -s -w "\n%{http_code}" \
		-H "Authorization: Bearer ${TOKEN}" \
		-H "Accept: application/json" \
		"${API_URL}/api/jobs/${JOB_ID}/full" || true)"

	POLL_HTTP="$(echo "$POLL_RESPONSE" | tail -n1)"
	POLL_BODY="$(echo "$POLL_RESPONSE" | sed '$ d')"

	if [[ "$POLL_HTTP" != "200" ]]; then
		echo "  → richiesta di polling fallita (status: ${POLL_HTTP:-conn_error})" >&2
		echo "$POLL_BODY" >&2
		exit 1
	fi

	FINAL_STATUS="$(echo "$POLL_BODY" | jq -r '.status // empty')"
	echo "  [$((ATTEMPT+1))/${MAX_ATTEMPTS}] status: ${FINAL_STATUS}"

	case "$FINAL_STATUS" in
		completed|failed|cancelled|dead_letter)
			echo
			echo "Job terminato con stato: ${FINAL_STATUS}"
			echo "$POLL_BODY" | jq '.' || echo "$POLL_BODY"
			break
			;;
	esac

	(( ATTEMPT++ ))
	sleep 3
done

if [[ -z "$FINAL_STATUS" ]]; then
	echo "  → timeout di attesa per il job" >&2
	exit 1
fi

case "$FINAL_STATUS" in
	completed)
		echo
		echo "SUCCESSO: generazione voiceover 1-item (it-IT DiegoNeural) completata."
		exit 0
		;;
	*)
		echo
		echo "ATTENZIONE: la generazione si è conclusa con stato '${FINAL_STATUS}'."
		exit 2
		;;
esac