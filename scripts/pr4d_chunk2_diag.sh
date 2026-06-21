#!/usr/bin/env bash
set +e
echo "=== full go vet internal/app (all errors) ==="
go vet ./internal/app/ 2>&1
echo "---vet-exit=$?"
echo ""
echo "=== bundle struct definitions (where are AssetsBundle + ArtlistBundle defined?) ==="
grep -rn 'type ArtlistBundle struct' internal/app/
echo "---"
grep -rn 'type AssetsBundle struct' internal/app/
echo "---"
echo "=== AssetsBundle FULL struct ==="
grep -A20 'type AssetsBundle struct' internal/app/module_assets.go
echo "---"
echo "=== registry.go lines 280-310 (AssetIndex/maintenance deletions) ==="
sed -n '280,310p' internal/app/registry.go
echo "---"
echo "=== registry.go lines 125-145 (SetHarvestService) ==="
sed -n '125,145p' internal/app/registry.go
echo "---"
echo "=== bootstrap.go residual MaintenanceService/ArtifactService refs ==="
grep -nE 'MaintenanceService|ArtifactService|DeletionService' internal/app/bootstrap.go
echo "---"
echo "=== module_artlist.go coreDeps.* residual refs ==="
grep -nE '[Cc]ore[Dd]eps\.' internal/app/module_artlist.go
echo "---"
echo "=== AutoHarvestService interface definition ==="
grep -rn 'type AutoHarvestService' internal/
echo "---"
echo "=== module_artlist.go Resolver field write + AutoHarvest callers ==="
grep -nE 'AutoHarvest|Resolver|SetHarvestService' internal/app/registry.go
