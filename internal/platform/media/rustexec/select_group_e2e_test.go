package rustexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// resolveMediaSamplerBinary locates the real mediasampler binary for e2e
// tests. It returns "" when no binary is available so the test can skip
// cleanly.
func resolveMediaSamplerBinary() string {
	if fromEnv := os.Getenv("VELOX_RUST_MEDIASAMPLER_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return fromEnv
		}
	}
	candidate := filepath.Join("..", "..", "..", "..", "bin", "mediasampler")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return candidate
	}
	return ""
}

// e2eGroupCandidates builds one owned candidate pool for one scene: five
// ready internet-image candidates whose label grounds the scene subject, so
// the ownership and subject hard constraints pass and only the bound ledger
// decides the cross-scene refusal.
func e2eGroupCandidates(prefix string) []scriptpkg.SegmentAssetCandidate {
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0, 5)
	for i := 0; i < 5; i++ {
		candidates = append(candidates, scriptpkg.SegmentAssetCandidate{
			AssetID:        prefix + "_asset_" + fmt.Sprint(i+1),
			Entity:         "Afghanistan",
			Query:          fmt.Sprintf("afghanistan %d", i+1),
			Score:          float64(5-i) / 5,
			RelevanceScore: float64(9-i) / 10,
			Provider:       "internet_images",
			SegmentID:      prefix + "_scene",
		})
	}
	return candidates
}

// TestMediaSamplerSelectGroupRealBinaryDistinctRankedMembers runs the real
// mediasampler binary through the Go SampleGroup port: five owned
// candidates must return 3 distinct ranked members in group_ids order.
func TestMediaSamplerSelectGroupRealBinaryDistinctRankedMembers(t *testing.T) {
	binary := resolveMediaSamplerBinary()
	if binary == "" {
		t.Skip("mediasampler release binary not installed; run make rust-fetch")
	}
	executor := newRealExecutor(t, binary, resolveFFmpegBinary())
	adapter, err := NewMediaSamplerAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	candidates := e2eGroupCandidates("05_e2e_group")

	group, err := adapter.SampleGroup(context.Background(), "05_e2e_group_scene", "Afghanistan",
		[]string{"afghanistan"}, candidates, false, 3, nil)
	if err != nil {
		t.Fatalf("real select_group failed: %v", err)
	}
	if len(group) != 3 {
		t.Fatalf("expected 3 group members, got %d", len(group))
	}
	seen := make(map[string]struct{}, len(group))
	for _, winner := range group {
		if winner.AssetID == "" {
			t.Fatal("group member with empty AssetID")
		}
		if _, duplicate := seen[winner.AssetID]; duplicate {
			t.Fatalf("duplicate group member %q", winner.AssetID)
		}
		seen[winner.AssetID] = struct{}{}
		if winner.SegmentID != "05_e2e_group_scene" {
			t.Fatalf("group member %q carries foreign ownership %q", winner.AssetID, winner.SegmentID)
		}
	}
}

// TestMediaSamplerSelectGroupRealBinaryRefusesBoundAssets pins the
// cross-scene ledger through the real binary: assets bound to an earlier
// scene are refused as already_bound, so a second scene's group can only
// contain remaining distinct candidates owned by that scene.
func TestMediaSamplerSelectGroupRealBinaryRefusesBoundAssets(t *testing.T) {
	binary := resolveMediaSamplerBinary()
	if binary == "" {
		t.Skip("mediasampler release binary not installed; run make rust-fetch")
	}
	executor := newRealExecutor(t, binary, resolveFFmpegBinary())
	adapter, err := NewMediaSamplerAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}

	// Scene A owns three of the five shared asset ids first.
	sceneA := e2eGroupCandidates("05_e2e_bound_scene_a")
	for i := range sceneA {
		sceneA[i].AssetID = fmt.Sprintf("05_e2e_bound_asset_%d", i+1)
		sceneA[i].SegmentID = "05_e2e_bound_scene_a"
	}
	first, err := adapter.SampleGroup(context.Background(), "05_e2e_bound_scene_a", "Afghanistan",
		[]string{"afghanistan"}, sceneA, false, 3, nil)
	if err != nil {
		t.Fatalf("scene A select_group failed: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("scene A group = %d members, want 3", len(first))
	}
	bound := make([]scriptports.AssetBinding, 0, len(first))
	for _, winner := range first {
		bound = append(bound, scriptports.AssetBinding{AssetID: winner.AssetID, SceneID: "05_e2e_bound_scene_a"})
	}

	// Scene B asks for a group over the same asset ids (owned by B) and must
	// never receive one of A's three bound assets back.
	sceneB := e2eGroupCandidates("05_e2e_bound_scene_b")
	for i := range sceneB {
		sceneB[i].AssetID = fmt.Sprintf("05_e2e_bound_asset_%d", i+1)
		sceneB[i].SegmentID = "05_e2e_bound_scene_b"
	}
	second, err := adapter.SampleGroup(context.Background(), "05_e2e_bound_scene_b", "Afghanistan",
		[]string{"afghanistan"}, sceneB, false, 3, bound)
	if err != nil {
		t.Fatalf("scene B select_group failed: %v", err)
	}
	boundIDs := make(map[string]struct{}, len(bound))
	for _, entry := range bound {
		boundIDs[entry.AssetID] = struct{}{}
	}
	for _, winner := range second {
		if _, forbidden := boundIDs[winner.AssetID]; forbidden {
			t.Fatalf("scene B received an asset already bound to scene A: %q", winner.AssetID)
		}
		if winner.SegmentID != "05_e2e_bound_scene_b" {
			t.Fatalf("scene B member %q carries foreign ownership %q", winner.AssetID, winner.SegmentID)
		}
	}
}
