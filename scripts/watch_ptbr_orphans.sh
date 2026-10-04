#!/usr/bin/env bash
# Watcher run pt-BR post-fix K2: attende la prossima run script.generate in
# lingua pt, ne aspetta lo stato terminale e verifica zero upload orfani
# voiceover (drain del pool DENTRO la run). Uso:
#   scripts/watch_ptbr_orphans.sh            # attende una nuova run (max ~30min)
#   scripts/watch_ptbr_orphans.sh <job_id>   # verifica diretta di una run già finita
set -Eeuo pipefail
cd "$(dirname -- "$0")/.."
DB='file:data/jobs/jobs.db.sqlite?mode=ro'

if [[ $# -ge 1 ]]; then
  exec python3 scripts/verify_ptbr_orphans.py "$1"
fi

START=$(date -u +%Y-%m-%dT%H:%M)
echo "[watch] attesa prossima run pt-BR da $START (max 30min)..."
FOUND=""
for _ in $(seq 1 90); do
  CAND=$(sqlite3 "$DB" "SELECT id FROM jobs WHERE type='script.generate' AND started_at>='$START' ORDER BY started_at ASC LIMIT 1;" 2>/dev/null || true)
  if [[ -n "$CAND" ]]; then
    L=$(sqlite3 "$DB" "SELECT payload FROM job_payloads WHERE job_id='$CAND' LIMIT 1;" 2>/dev/null \
      | python3 -c 'import json,sys
try:
 d=json.load(sys.stdin); print((d.get("items") or [d])[0].get("language","?"))
except Exception: print("?")' 2>/dev/null) || L="?"
    if [[ "$L" == "pt" ]]; then FOUND=$CAND; echo "[watch] run pt trovata: $FOUND"; break; fi
  fi
  sleep 20
done
if [[ -z "$FOUND" ]]; then echo "[watch] nessuna run pt nella finestra"; exit 0; fi

ST=""
for _ in $(seq 1 180); do
  ST=$(sqlite3 "$DB" "SELECT status FROM jobs WHERE id='$FOUND';" 2>/dev/null || true)
  case "$ST" in SUCCEEDED|FAILED|CANCELLED) break;; esac
  sleep 10
done
echo "[watch] stato terminale: ${ST:-sconosciuto} → verifica orfani:"
exec python3 scripts/verify_ptbr_orphans.py "$FOUND"
