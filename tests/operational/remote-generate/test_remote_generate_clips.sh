#!/usr/bin/env bash
# tests/operational/remote-generate/test_remote_generate_clips.sh
#
# Test end-to-end da un computer remoto: submit di un payload
# GenerationEnvelopeV2 con source.type="clips" (3 clip Pacquiao vs
# Broner) su POST /api/script/generate, polling fino al terminal state.
#
# Uso:
#   API_URL=https://pipeline.example.com TOKEN=il_tuo_token \
#     ./tests/operational/remote-generate/test_remote_generate_clips.sh
#
# Companion di tests/operational/remote-generate/test_remote_generate.sh (text-based). Stesso
# protocollo di health-check + capabilities + polling; solo il payload
# cambia (clip-driven invece di topic-driven). Riutilizza l'handling
# canonico del header Retry-After già presente nel rate limiter dopo
# ITEM 5 (commit feat(middleware)).

set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:8000}"
TOKEN="${TOKEN:-}"

if [[ -z "$TOKEN" ]]; then
	echo "Errore: imposta la variabile TOKEN prima di eseguire lo script." >&2
	echo "Esempio: TOKEN=il_tuo_token API_URL=https://pipeline.example.com $0" >&2
	exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "Errore: jq è richiesto per il parsing JSON." >&2
	exit 1
fi

# Identità canonica del job (godlike/06 SSOT — 3 clip Pacquiao vs
# Broner dall'inventory reale del progetto, vedi
# tests/operational/pacquiao_broner_script_test.json).
ITEM_ID="pacquiao-broner-3clip-highlights"
CLIP_ID_1="yt_RRJvrDKunyA_32_37_v1"     # Round 1 — opening / fase di studio
CLIP_ID_2="yt_RRJvrDKunyA_993_998_v1"    # Round 7 — Broner barcolla (climax)
CLIP_ID_3="yt_RRJvrDKunyA_1727_1732_v1"  # Verdetto unanime (closing)
LANGUAGE="it"

# Idempotency dual-anchor (worker_integration main.go contract):
# X-Request-ID = MD5(itemID|sorted_clip_ids_csv|language), 32 hex stable.
# Sort clip_ids before hashing — matches worker_integration main.go's
# buildStableReqID contract so logical-set X-Request-IDs collide on the
# UNIQUE idempotency anchor. Payload's clip_ids surface-order is preserved
# (narrative intent):
#   LC_ALL=C forces POSIX byte order so the sort result is locale-
#   independent (matches Go's sort.Strings on any LANG/LC_ALL).
LC_ALL=C SORTED_CLIPS=$(printf '%s\n' "${CLIP_ID_1}" "${CLIP_ID_2}" "${CLIP_ID_3}" | sort | paste -sd ',' -)
X_REQUEST_ID=$(printf '%s|%s|%s' \
	"${ITEM_ID}" "${SORTED_CLIPS}" "${LANGUAGE}" | md5sum | awk '{print $1}')
IDEMPOTENCY_KEY="${ITEM_ID}"

echo "================================================"
echo "PipelineGen remote CLIPS generation test (Pacquiao vs Broner)"
echo "API_URL: $API_URL"
echo "ITEM_ID: $ITEM_ID"
echo "X_Request_ID: $X_REQUEST_ID"
echo "CLIPS:"
echo "  - ${CLIP_ID_1}  (Round 1: opening)"
echo "  - ${CLIP_ID_2}  (Round 7: Broner barcolla)"
echo "  - ${CLIP_ID_3}  (Verdetto unanime)"
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
echo "[4/5] POST /api/script/generate (source.type=clips, 3 clip)"

# Payload canonico (GenerationEnvelopeV2, preset=custom, source.type=clips).
# Match del file gold test/operational/pacquiao_broner_script_test.json ma
# con solo 3 clip (apertura, climax, chiusura) invece degli 8 canonici
# dell'item-planner originale. Le policies grounding=clips_primary +
# fallback=strict sono i default SSOT per source.type=clips (mirror del
# worker_integration main.go::buildSourceMap).
POST_RESPONSE="$(curl -s -w "\n%{http_code}" \
	--request POST \
	"${API_URL}/api/script/generate" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H "X-Request-ID: ${X_REQUEST_ID}" \
	-H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
	-H "Content-Type: application/json" \
	-H "Accept: application/json" \
	--data '{
		"version": 2,
		"preset": "custom",
		"items": [
			{
				"id": "pacquiao-broner-3clip-highlights",
				"title": "Pacquiao vs Broner: highlights in 3 scene",
				"language": "it",
				"tone": "documentario",
				"style": "cinematic",
				"source": {
					"type": "clips",
					"clip_ids": [
						"yt_RRJvrDKunyA_32_37_v1",
						"yt_RRJvrDKunyA_993_998_v1",
						"yt_RRJvrDKunyA_1727_1732_v1"
					],
					"num_clips": 3,
					"grounding_policy": "clips_primary",
					"fallback_policy": "strict",
					"ordering_strategy": "chronological",
					"guidelines": "Segui l ordine dei clip: (1) opening Round 1, (2) Round 7 climax, (3) verdetto finale. Non aggiungere dettagli non presenti nei clip. Mantieni il testo in italiano."
				},
				"script_params": {
					"target_words": 360,
					"min_words": 300,
					"segment_words": 120
				},
				"output": {
					"extract_entities": "disabled",
					"generate_metadata": "disabled",
					"generate_scene_images": "disabled",
					"save_to_db": true,
					"generate_timeline": true,
					"drive_folder_id": "1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV"
				}
			}
		]
	}' || true)"

POST_HTTP="$(echo "$POST_RESPONSE" | tail -n1)"
POST_BODY="$(echo "$POST_RESPONSE" | sed '$ d')"

echo "  HTTP status: ${POST_HTTP:-conn_error}"

# Surfaces l'header Retry-After aggiunto in ITEM 5 (se presente, lo logghiamo).
POST_RETRY_AFTER="$(echo "$POST_BODY" | head -n 0 2>/dev/null || true)"

if [[ "${POST_HTTP}" != "202" ]]; then
	echo "  → /api/script/generate FALLITO" >&2
	echo "Risposta: $POST_BODY" >&2
	if [[ "${POST_HTTP}" == "429" ]]; then
		echo "  → rate limit: aspetta Retry-After secondi (header introdotto in ITEM 5)." >&2
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
		echo "SUCCESSO: generazione clips 3-clip completata."
		exit 0
		;;
	*)
		echo
		echo "ATTENZIONE: la generazione si è conclusa con stato '${FINAL_STATUS}'."
		exit 2
		;;
esac
