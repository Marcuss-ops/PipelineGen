#!/usr/bin/env bash
# Monitor di carico per le finestre golden: campiona loadavg, processi di
# build (cc1plus/ninja/cargo/go) e occupazione GPU ogni 15 s. Un campione con
# build attivo durante una finestra misurata invalida la run come evidenza
# di capacità (contratto di misura: nessun altro carico pesante).
# Uso: load_monitor.sh <output.tsv> [interval_seconds]
set -Eeuo pipefail
OUT="${1:?uso: load_monitor.sh <output.tsv> [interval]}"
INTERVAL="${2:-15}"
printf 'ts\tload1\tload5\tload15\tbuild_procs\tgpu_pct\tmem_avail_mb\n' > "$OUT"
while :; do
  TS="$(date -u +%FT%TZ)"
  read -r L1 L5 L15 _ < /proc/loadavg
  BUILDS="$(pgrep -c -f 'cc1plus|cc1|ninja|cargo|cmake --build|go build|compile' 2>/dev/null || echo 0)"
  GPU="$(nvidia-smi --query-gpu=utilization.gpu --format=csv,noheader,nounits 2>/dev/null || echo -1)"
  MEM_AVAIL="$(awk '/MemAvailable/ {printf "%d", $2/1024}' /proc/meminfo)"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$TS" "$L1" "$L5" "$L15" "$BUILDS" "$GPU" "$MEM_AVAIL" >> "$OUT"
  sleep "$INTERVAL"
done
