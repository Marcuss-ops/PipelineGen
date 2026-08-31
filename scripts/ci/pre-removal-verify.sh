#!/usr/bin/env bash
# pre-removal-verify.sh
#
# Gate fail-closed per la rimozione del finalizer assembly locale (PipelineGen
# producer-only -> Master finalizer). Verifica che siano soddisfatte le
# precondizioni prima di eliminare internal/capabilities/assembly:
#
#   1. Nessun import residuo del package capabilities/assembly al di fuori
#      del package stesso (il solo riferimento ammesso fuori dal package è
#      l'elenco dei prefix di persistenza in scripts/archcheck/checks_coupling.go,
#      che va ripulito contestualmente).
#   2. Nessun payload/fixture/script applicativo usa i flag legacy di final
#      assembly (assemble_final, assembleFinalVideo, FinalVideoAssembler,
#      PublishFinalVideo, final_video_path). Solo i guardrail negativi dei test
#      Go -- internal/capabilities/scripts/runner_final_video_boundary_test.go,
#      internal/capabilities/script/handler_*_test.go, handler_generate_request.go
#      -- possono citarli, esplicitamente come "removed contract fields".
#
# Il contratto Velox SSOT (internal/kernel/assembly, media/assembly_contract)
# e il registro job assembly (registry_assembly.go) NON rientrano in questa
# rimozione: sono la source of truth del Master e vanno conservati.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
pushd "$root" >/dev/null

fail=0
warn() { echo "FAIL: $*" >&2; fail=1; }

# --- 1. import residui di capabilities/assembly (esclusa la cartella stessa) ---
mapfile -t importers < <(grep -rn "internal/capabilities/assembly" --include="*.go" . \
    | grep -v "/internal/capabilities/assembly/" || true)

if [[ "${#importers[@]}" -gt 0 ]]; then
    for line in "${importers[@]}"; do
        warn "import residuo di capabilities/assembly: $line"
    done
else
    echo "OK: nessun import residuo di internal/capabilities/assembly"
fi

# --- 2. flag legacy di final assembly in payload/fixture/script applicativi ---
# Solo file non-Go e solo i flag legacy; i guardrail Go negativi sono esclusi.
flag_pattern='assemble_final|assembleFinalVideo|FinalVideoAssembler|PublishFinalVideo|final_video_path'
mapfile -t fixture_hits < <(grep -rnE "$flag_pattern" \
    --include="*.sh" --include="*.py" --include="*.json" --include="*.yaml" --include="*.yml" \
    . 2>/dev/null | grep -v node_modules | grep -v 'scripts/ci/pre-removal-verify.sh' || true)

if [[ "${#fixture_hits[@]}" -gt 0 ]]; then
    for line in "${fixture_hits[@]}"; do
        warn "flag legacy di final assembly in fixture/script: $line"
    done
else
    echo "OK: nessun flag legacy assemble_* in payload/fixture/script"
fi

# --- 3. il contratto Velox SSOT deve essere conservato (guard rail inverso) ---
for p in internal/kernel/assembly/contract.go \
         internal/kernel/media/assembly_contract.go \
         internal/capabilities/jobs/registry_assembly.go; do
    if [[ ! -f "$p" ]]; then
        warn "contratto/registro Velox SSOT mancante (non deve essere rimosso): $p"
    fi
done
if grep -q "capabilities/assembly" scripts/archcheck/checks_coupling.go 2>/dev/null; then
    echo "NOTA: scripts/archcheck/checks_coupling.go cita ancora capabilities/assembly — va aggiornato contestualmente alla rimozione."
fi

popd >/dev/null

if [[ "$fail" -eq 1 ]]; then
    echo "GATE: RED — prerequisiti di rimozione non soddisfatti." >&2
    exit 1
fi
echo "GATE: GREEN — prerequisiti di rimozione soddisfatti (solo residui)."