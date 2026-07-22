#!/usr/bin/env bash
# examples/test_remote_generate.sh
#
# Test end-to-end da un computer remoto contro un'istanza PipelineGen.
# Esegue in sequenza:
#   1. GET  /health
#   2. GET  /ready
#   3. GET  /api/capabilities
#   4. POST /api/script/generate
#   5. Polling su /api/jobs/{job_id}/full fino al termine.
#
# Uso:
#   API_URL=https://pipeline.example.com TOKEN=il_tuo_token ./examples/test_remote_generate.sh

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

REQUEST_ID="$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo "req-$(date +%s%N)")"
IDEMPOTENCY_KEY="generate-$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo "idem-$(date +%s%N)")"

echo "=============================================="
echo "PipelineGen remote generation test"
echo "API_URL: $API_URL"
echo "REQUEST_ID: $REQUEST_ID"
echo "IDEMPOTENCY_KEY: $IDEMPOTENCY_KEY"
echo "=============================================="

# ── 1. Health ───────────────────────────────────────────────────────────────
echo
echo "[1/5] GET /health"
health_status="$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/health" || true)"
if [[ "$health_status" == "200" ]]; then
	echo "  → /health OK (200)"
else
	echo "  → /health FALLITO (status: ${health_status:-conn_error})" >&2
	exit 1
fi

# ── 2. Ready ─────────────────────────────────────────────────────────────────
echo
echo "[2/5] GET /ready"
ready_status="$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/ready" || true)"
if [[ "$ready_status" == "200" ]]; then
	echo "  → /ready OK (200)"
else
	echo "  → /ready FALLITO (status: ${ready_status:-conn_error})" >&2
fi

# ── 3. Capabilities ─────────────────────────────────────────────────────────
echo
echo "[3/5] GET /api/capabilities"
cap_response="$(curl -s -w "\n%{http_code}" \
	-H "Authorization: Bearer ${TOKEN}" \
	"${API_URL}/api/capabilities" || true)"
cap_http="$(echo "$cap_response" | tail -n1)"
cap_body="$(echo "$cap_response" | sed '$ d')"
if [[ "$cap_http" == "200" ]]; then
	echo "  → /api/capabilities OK (200)"
	echo "$cap_body" | jq '.' || echo "$cap_body"
else
	echo "  → /api/capabilities FALLITO (status: ${cap_http:-conn_error})" >&2
	echo "Risposta: $cap_body" >&2
	exit 1
fi

# ── 4. Generate ────────────────────────────────────────────────────────────
echo
echo "[4/5] POST /api/script/generate"

post_response="$(curl -s -w "\n%{http_code}" \
	--request POST \
	"${API_URL}/api/script/generate" \
	-H "Authorization: Bearer ${TOKEN}" \
	-H "Content-Type: application/json" \
	-H "Accept: application/json" \
	-H "X-Request-ID: ${REQUEST_ID}" \
	-H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
	--data '{
		"version": 2,
		"preset": "custom",
		"items": [
			{
				"id": "remote-test-001",
				"title": "Test generazione remota",
				"language": "it",
				"tone": "explanatory",
				"source": {
					"type": "text",
					"topic": "Test del forwarding API",
					"source_text": "Questo testo verifica che un computer remoto possa avviare correttamente una generazione."
				},
				"script_params": {
					"target_words": 200
				},
				"output": {
					"generate_metadata": false,
					"extract_entities": false
				}
			}
		]
	}' || true)"

post_http="$(echo "$post_response" | tail -n1)"
post_body="$(echo "$post_response" | sed '$ d')"

echo "  HTTP status: ${post_http:-conn_error}"

if [[ "$post_http" != "202" ]]; then
	echo "  → /api/script/generate FALLITO" >&2
	echo "Risposta: $post_body" >&2
	exit 1
fi

job_id="$(echo "$post_body" | jq -r '.job_id // empty')"
if [[ -z "$job_id" ]]; then
	echo "  → job_id mancante nella risposta" >&2
	echo "Risposta: $post_body" >&2
	exit 1
fi

echo "  → job_id: $job_id"

# ── 5. Poll ──────────────────────────────────────────────────────────────────
echo
echo "[5/5] Polling /api/jobs/${job_id}/full ogni 3s"

max_attempts=60
attempt=0
final_status=""
while (( attempt < max_attempts )); do
	poll_response="$(curl -s -w "\n%{http_code}" \
		-H "Authorization: Bearer ${TOKEN}" \
		-H "Accept: application/json" \
		"${API_URL}/api/jobs/${job_id}/full" || true)"

	poll_http="$(echo "$poll_response" | tail -n1)"
	poll_body="$(echo "$poll_response" | sed '$ d')"

	if [[ "$poll_http" != "200" ]]; then
		echo "  → richiesta di polling fallita (status: ${poll_http:-conn_error})" >&2
		echo "$poll_body" >&2
		exit 1
	fi

	final_status="$(echo "$poll_body" | jq -r '.status // empty')"
	echo "  [$((attempt+1))/${max_attempts}] status: ${final_status}"

	case "$final_status" in
		completed|failed|cancelled|dead_letter)
			echo
			echo "Job terminato con stato: ${final_status}"
			echo "$poll_body" | jq '.' || echo "$poll_body"
			break
			;;
	esac

	(( attempt++ ))
	sleep 3
done

if [[ -z "$final_status" ]]; then
	echo "  → timeout di attesa per il job" >&2
	exit 1
fi

case "$final_status" in
	completed)
		echo
		echo "SUCCESSO: generazione completata."
		exit 0
		;;
	*)
		echo
		echo "ATTENZIONE: la generazione si è conclusa con stato '${final_status}'."
		exit 2
		;;
esac
