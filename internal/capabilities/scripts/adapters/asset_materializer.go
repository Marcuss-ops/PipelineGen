// Package adapters — asset_materializer.go is the canonical
// implementation of scripts/ports.AssetMaterializer. It uses the
// mediamemory CandidateRepository + MaterializeWorker to promote the
// winning asset from Cold/Warm to Hot (download/upload) on demand.
package adapters

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

// defaultAssetMaterializer implements ports.AssetMaterializer.
type defaultAssetMaterializer struct {
	candidates mediamemory.CandidateRepository
	worker     mediamemory.MaterializeWorker
	log        *zap.Logger
}

// NewDefaultAssetMaterializer builds an AssetMaterializer.
// Either port may be nil; the implementation degrades to a no-op
// and logs a warning rather than failing the visual planning pipeline.
func NewDefaultAssetMaterializer(candidates mediamemory.CandidateRepository, worker mediamemory.MaterializeWorker, log *zap.Logger) ports.AssetMaterializer {
	if log == nil {
		log = zap.NewNop()
	}
	return &defaultAssetMaterializer{candidates: candidates, worker: worker, log: log}
}

// Materialize ensures the winning layer's asset is available locally.
// It is idempotent: already-local assets or missing dependencies
// return the original asset ID without error.
func (m *defaultAssetMaterializer) Materialize(ctx context.Context, layer mediamemory.Layer) (ports.MaterializedAsset, error) {
	out := ports.MaterializedAsset{
		AssetID:    layer.AssetID,
		Provider:   layer.Provider,
		DurationMs: layerDuration(layer),
	}

	if !needsMaterialization(layer.Provider) {
		return out, nil
	}
	if m.candidates == nil || m.worker == nil {
		m.log.Warn("asset materializer dependencies missing; skipping materialization",
			zap.String("asset_id", layer.AssetID),
			zap.String("provider", layer.Provider),
		)
		return out, nil
	}
	if strings.TrimSpace(layer.CandidateID) == "" {
		m.log.Warn("layer has no candidate_id; cannot materialize external asset",
			zap.String("asset_id", layer.AssetID),
			zap.String("provider", layer.Provider),
		)
		return out, nil
	}

	candidate, err := m.candidates.FindByID(ctx, layer.CandidateID)
	if err != nil {
		m.log.Warn("candidate lookup failed; keeping original asset",
			zap.String("candidate_id", layer.CandidateID),
			zap.Error(err),
		)
		return out, nil
	}

	mat, err := m.worker.PromoteOnDemand(ctx, candidate, mediamemory.MaterializeOptions{
		TargetSlot: layer.Slot,
		HotCache:   true,
		ProjectID:  "",
	})
	if err != nil {
		m.log.Warn("on-demand materialization failed; keeping original asset",
			zap.String("candidate_id", candidate.ID),
			zap.Error(err),
		)
		return out, nil
	}
	if mat.AssetID == "" {
		m.log.Warn("materialize worker returned empty asset_id; keeping original asset",
			zap.String("candidate_id", candidate.ID),
		)
		return out, nil
	}

	provider := mat.Provider
	if strings.TrimSpace(provider) == "" {
		provider = "drive"
	}
	return ports.MaterializedAsset{
		AssetID:    mat.AssetID,
		Provider:   provider,
		DurationMs: mat.DurationMs,
	}, nil
}

// needsMaterialization reports whether a provider tag indicates the
// asset is not yet local and may need on-demand download/upload.
func needsMaterialization(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "", "drive", "local", "mediamemory.semantic":
		return false
	}
	return true
}

// layerDuration returns the layer duration in milliseconds. It prefers
// the explicit EndMs/StartMs window; when both are zero it falls back
// to zero (the duration will come from the materialized asset).
func layerDuration(layer mediamemory.Layer) int64 {
	if layer.EndMs > layer.StartMs {
		return layer.EndMs - layer.StartMs
	}
	return 0
}
