package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SampleGroup is the capability-facing wrapper of the select_group
// operation: kernel candidates in, kernel winners out in rank order.

func TestSampleGroupMapsKernelCandidatesAndReturnsOrderedWinners(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":null,"group_ids":["c2","c1"]}`)}
	a, err := NewMediaSamplerAdapter(executorForSemantic(r))
	if err != nil {
		t.Fatal(err)
	}
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "c1", Entity: "Afghanistan", Query: "afghan mountains", RelevanceScore: 0.4, SegmentID: "seg-1"},
		{AssetID: "c2", Entity: "Hindu Kush", Query: "hindu kush", RelevanceScore: 0.9, SegmentID: "seg-1"},
	}
	winners, err := a.SampleGroup(context.Background(), "seg-1", "Afghanistan in 2024", []string{"afghanistan"},
		candidates, false, 2, nil)
	if err != nil {
		t.Fatalf("SampleGroup: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(r.input, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if got := request["operation"]; got != "select_group" {
		t.Fatalf("operation = %v", got)
	}
	if got := request["group_size"]; int(got.(float64)) != 2 {
		t.Fatalf("group_size = %v", got)
	}
	if got := request["version"]; got != "mediasampler.v1" {
		t.Fatalf("version = %v", got)
	}
	rows := request["candidates"].([]any)
	if len(rows) != 2 {
		t.Fatalf("candidates count = %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["id"] != "c1" || first["label"] != "Afghanistan" || first["owner_segment_id"] != "seg-1" {
		t.Fatalf("first wire candidate = %v", first)
	}
	if len(winners) != 2 || winners[0].AssetID != "c2" || winners[1].AssetID != "c1" {
		t.Fatalf("winners must follow group_ids order, got %+v", winners)
	}
	for _, winner := range winners {
		if winner.SegmentID != "seg-1" || winner.Entity == "" {
			t.Fatalf("winner lost its candidate identity: %+v", winner)
		}
		if !strings.Contains(winner.SelectionReason, "(group)") {
			t.Fatalf("winners must carry the group selection reason, got %q", winner.SelectionReason)
		}
	}
}

func TestSampleGroupConvertsBoundAssets(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":null,"group_ids":["c1","c2"]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "c1", RelevanceScore: 0.5, SegmentID: "seg-1"},
		{AssetID: "c2", RelevanceScore: 0.6, SegmentID: "seg-1"},
	}
	_, err := a.SampleGroup(context.Background(), "seg-1", "s", nil, candidates, false, 2,
		[]scriptports.AssetBinding{{AssetID: "a1", SceneID: "other"}})
	if err != nil {
		t.Fatalf("SampleGroup with bound assets: %v", err)
	}
	var request map[string]any
	if err := json.Unmarshal(r.input, &request); err != nil {
		t.Fatal(err)
	}
	bound, ok := request["bound_assets"].([]any)
	if !ok || len(bound) != 1 {
		t.Fatalf("bound_assets missing from request: %v", request["bound_assets"])
	}
	entry := bound[0].(map[string]any)
	if entry["asset_id"] != "a1" || entry["scene_id"] != "other" {
		t.Fatalf("bound asset entry = %v", entry)
	}
}

func TestSampleGroupSizesOutsideTwoToFiveFailClosed(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":null,"group_ids":[]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	candidates := []scriptpkg.SegmentAssetCandidate{{AssetID: "c1", RelevanceScore: 0.5, SegmentID: "seg-1"}}
	for _, bad := range []int{0, 1, 6} {
		if _, _, err := a.SelectGroup(context.Background(), "seg-1", "s", nil,
			[]mediaSamplerCandidate{{ID: "c1"}}, false, bad, nil); err == nil {
			t.Fatalf("SelectGroup accepted group_size %d", bad)
		}
		if _, err := a.SampleGroup(context.Background(), "seg-1", "s", nil, candidates, false, bad, nil); err == nil {
			t.Fatalf("SampleGroup accepted group_size %d", bad)
		}
	}
}

func TestSampleGroupRejectsUnknownGroupMembersWhenConvertingBack(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":null,"group_ids":["c9"]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	candidates := []scriptpkg.SegmentAssetCandidate{{AssetID: "c1", RelevanceScore: 0.5, SegmentID: "seg-1"}}
	_, err := a.SampleGroup(context.Background(), "seg-1", "s", nil, candidates, false, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown group member") {
		t.Fatalf("expected unknown-member error, got %v", err)
	}
}
