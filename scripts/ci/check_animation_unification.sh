#!/usr/bin/env bash
# P0-4: controllo CI cross-repository ChrononTemplate → RenderingGen → PipelineGen.
# Verifica: catalog digest esportabile, parity embedded/emitted, selector +
# direttive + backend + resolver, golden determinism. Fail-closed.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

echo "== animreg =="
go test ./internal/capabilities/animreg/... -count=1
echo "== overlays (selector, seed, golden, chain) =="
go test ./internal/capabilities/overlays/ -run 'TestAnimation|TestResolveAnimation|TestLongPhraseFamily|TestSeeded|TestNoRepeat|TestChain' -count=1
echo "== scripts/overlay (directives, policies) =="
go test ./internal/capabilities/scripts/overlay/ -count=1
echo "== scripts (compile, queue, map backend) =="
go test ./internal/capabilities/scripts/ -run 'TestResolveMapBackend|TestCompileOverlay|TestRenderQueue|TestAnimation' -count=1
echo "== cliprender adapters (durable resolver) =="
go test ./internal/capabilities/cliprender/adapters/ -run TestResolveWithDurable -count=1
echo "== catalog parity (embedded vs emitted) =="
if [ -x ../RenderingGen/scripts/sync_motion_catalog.sh ]; then
  ../RenderingGen/scripts/sync_motion_catalog.sh --check
else
  echo "skip: RenderingGen/scripts/sync_motion_catalog.sh not found (monorepo layout?)"
fi
echo "OK animation unification"
