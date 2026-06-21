#!/usr/bin/env bash
set +e
echo "=== go vet internal/app ==="
go vet ./internal/app/ 2>&1 | head -80
echo "---vet-exit=$?"
echo ""
echo "=== go build internal/app ==="
go build ./internal/app/ 2>&1 | head -50
echo "---build-exit=$?"
echo ""
echo "=== go build ./internal/... ==="
go build ./internal/... 2>&1 | head -30
echo "---whole-build-exit=$?"
echo ""
echo "=== go test ./internal/app/... -count=1 -short ==="
go test ./internal/app/... -count=1 -short -timeout 60s 2>&1 | tail -30
echo "---test-exit=$?"
echo ""
echo "=== confirm 9 Wire<Module>() signatures ==="
for f in internal/app/module_*.go; do
  sig=$(grep -E '^func Wire' "$f" | head -1)
  echo "$(basename $f): $sig"
done
echo ""
echo "=== any remaining coreDeps.X reads (should be 0) ==="
grep -cE '[Cc]ore[Dd]eps\.' internal/app/module_*.go
echo ""
echo "=== bundle struct definitions ==="
grep -A12 'type ArtlistBundle struct' internal/app/artlist_bundle.go
echo "---"
grep -A12 'type AssetsBundle struct' internal/app/*bundle*.go
echo ""
echo "=== Cd.X reads in registry.go (post-projection writes should be eliminated) ==="
grep -nE '\bcd\.[A-Z]' internal/app/registry.go | head -30
