#!/usr/bin/env bash
set +e
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored
echo "=== go vet internal/app ==="
go vet ./internal/app/ 2>&1 | head -120
echo "---vet-exit=$?"
echo ""
echo "=== go build internal/app ==="
go build ./internal/app/ 2>&1 | head -80
echo "---build-exit=$?"
echo ""
echo "=== go build ./internal/... ==="
go build ./internal/... 2>&1 | head -80
echo "---whole-build-exit=$?"
echo ""
echo "=== go test ./internal/app/... -count=1 -short ==="
go test ./internal/app/... -count=1 -short -timeout 90s 2>&1 | tail -40
echo "---test-exit=$?"
echo ""
echo "=== Any remaining CoreDeps references? ==="
grep -rn 'CoreDeps\|projectRootToCoreDeps\|initCoreMinimal\|services struct' internal/app/ | grep -v _test.go | head -30
echo ""
echo "=== All Wire<Module> signatures ==="
for f in internal/app/module_*.go; do
  sig=$(grep -E '^func Wire' "$f" | head -1)
  echo "$(basename $f): $sig"
done
echo ""
echo "=== WireRegistry signature ==="
grep -E '^func WireRegistry' internal/app/registry.go
echo ""
echo "=== WireServices signature ==="
grep -E '^func WireServices' internal/app/bootstrap.go
