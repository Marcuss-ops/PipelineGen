package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

type fakeCandidateRepo struct {
	candidates map[string]mediamemory.MediaCandidate
	err        error
}

func (r *fakeCandidateRepo) UpsertInsert(_ context.Context, c mediamemory.MediaCandidate) (mediamemory.MediaCandidate, error) {
	return c, nil
}
func (r *fakeCandidateRepo) FindByID(_ context.Context, id string) (mediamemory.MediaCandidate, error) {
	if r.err != nil {
		return mediamemory.MediaCandidate{}, r.err
	}
	c, ok := r.candidates[id]
	if !ok {
		return mediamemory.MediaCandidate{}, errors.New("not found")
	}
	return c, nil
}
func (r *fakeCandidateRepo) ListByProvider(_ context.Context, _ string, _ int) ([]mediamemory.MediaCandidate, error) {
	return nil, nil
}
func (r *fakeCandidateRepo) ListPendingMaterialization(_ context.Context, _ int) ([]mediamemory.MediaCandidate, error) {
	return nil, nil
}
func (r *fakeCandidateRepo) UpdateStatus(_ context.Context, _ string, _ mediamemory.DiscoveryStatus, _ mediamemory.MaterializationStatus) error {
	return nil
}

type fakeMaterializeWorker struct {
	result mediamemory.MediaCandidate
	err    error
}

func (w *fakeMaterializeWorker) Materialize(_ context.Context, _ mediamemory.MaterializationRequest) (mediamemory.MaterializationResult, error) {
	return mediamemory.MaterializationResult{}, nil
}
func (w *fakeMaterializeWorker) PromoteOnDemand(_ context.Context, _ mediamemory.MediaCandidate, _ mediamemory.MaterializeOptions) (mediamemory.MediaCandidate, error) {
	if w.err != nil {
		return mediamemory.MediaCandidate{}, w.err
	}
	return w.result, nil
}

func TestAssetMaterializer_SkipsLocalProvider(t *testing.T) {
	repo := &fakeCandidateRepo{candidates: map[string]mediamemory.MediaCandidate{
		"c1": {ID: "c1", AssetID: "asset-1", Provider: "drive"},
	}}
	worker := &fakeMaterializeWorker{result: mediamemory.MediaCandidate{ID: "c1", AssetID: "asset-2", Provider: "drive", DurationMs: 7000}}
	m := NewDefaultAssetMaterializer(repo, worker, nil)

	mat, err := m.Materialize(context.Background(), mediamemory.Layer{Slot: media.SlotPrimaryVideo, AssetID: "asset-1", CandidateID: "c1", Provider: "drive"})
	if err != nil {
		t.Fatal(err)
	}
	if mat.AssetID != "asset-1" {
		t.Fatalf("expected asset-1 to remain unchanged, got %q", mat.AssetID)
	}
	if mat.Provider != "drive" {
		t.Fatalf("expected provider drive, got %q", mat.Provider)
	}
}

func TestAssetMaterializer_MaterializesExternalAsset(t *testing.T) {
	repo := &fakeCandidateRepo{candidates: map[string]mediamemory.MediaCandidate{
		"c1": {ID: "c1", AssetID: "ext-1", Provider: "artlist", SourceURL: "http://example.com/vid.mp4", DurationMs: 5000},
	}}
	worker := &fakeMaterializeWorker{result: mediamemory.MediaCandidate{ID: "c1", AssetID: "local-1", Provider: "drive", DurationMs: 5000}}
	m := NewDefaultAssetMaterializer(repo, worker, nil)

	mat, err := m.Materialize(context.Background(), mediamemory.Layer{Slot: media.SlotPrimaryVideo, AssetID: "ext-1", CandidateID: "c1", Provider: "artlist"})
	if err != nil {
		t.Fatal(err)
	}
	if mat.AssetID != "local-1" {
		t.Fatalf("expected local-1, got %q", mat.AssetID)
	}
	if mat.Provider != "drive" {
		t.Fatalf("expected provider drive, got %q", mat.Provider)
	}
	if mat.DurationMs != 5000 {
		t.Fatalf("expected duration 5000, got %d", mat.DurationMs)
	}
}

func TestAssetMaterializer_NoCandidateID_ReturnsOriginal(t *testing.T) {
	m := NewDefaultAssetMaterializer(nil, nil, nil)
	mat, err := m.Materialize(context.Background(), mediamemory.Layer{Slot: media.SlotPrimaryVideo, AssetID: "ext-1", Provider: "artlist"})
	if err != nil {
		t.Fatal(err)
	}
	if mat.AssetID != "ext-1" {
		t.Fatalf("expected ext-1, got %q", mat.AssetID)
	}
}

func TestAssetMaterializer_NilDependencies_ReturnsOriginal(t *testing.T) {
	m := NewDefaultAssetMaterializer(nil, nil, nil)
	mat, err := m.Materialize(context.Background(), mediamemory.Layer{Slot: media.SlotPrimaryVideo, AssetID: "ext-1", CandidateID: "c1", Provider: "artlist"})
	if err != nil {
		t.Fatal(err)
	}
	if mat.AssetID != "ext-1" {
		t.Fatalf("expected ext-1, got %q", mat.AssetID)
	}
}
