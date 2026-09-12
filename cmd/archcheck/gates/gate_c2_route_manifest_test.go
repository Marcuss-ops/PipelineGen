//go:build c2_route_manifest

package main

import "testing"

func TestComputeDiffIsSetBasedAndDeterministicByCaller(t *testing.T) {
	manifest := map[routeKey]bool{
		{method: "GET", path: "/health"}:    true,
		{method: "POST", path: "/api/jobs"}: true,
	}
	docs := map[routeKey]bool{
		{method: "GET", path: "/health"}: true,
		{method: "GET", path: "/ready"}:  true,
	}
	got := computeDiff(manifest, docs)
	if len(got) != 2 {
		t.Fatalf("diff=%d, want 2: %#v", len(got), got)
	}
	seen := map[string]bool{}
	for _, row := range got {
		seen[row.kind+" "+row.route.String()] = true
	}
	if !seen["manifest-only POST /api/jobs"] || !seen["docs-only GET /ready"] {
		t.Fatalf("unexpected diff rows: %#v", got)
	}
}

func TestDiffDataRuntimeArtifactsAgree(t *testing.T) {
	manifest := []byte("routes:\n- method: GET\n  path: /health\n")
	docs := []byte("| Method | Path | Description |\n|--------|------|-------------|\n| GET | `/health` | ok |\n")
	rows, manifestCount, docsCount, err := diffData(manifest, docs)
	if err != nil {
		t.Fatalf("diffData: %v", err)
	}
	if len(rows) != 0 || manifestCount != 1 || docsCount != 1 {
		t.Fatalf("rows=%#v counts=%d/%d, want no drift and 1/1", rows, manifestCount, docsCount)
	}
}
