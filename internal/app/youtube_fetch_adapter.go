// Package app — sourcing fetch adapter extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

type sourcingFetchAdapter struct {
	registry *providers.Registry
}

func (a *sourcingFetchAdapter) Fetch(ctx context.Context, req sourcing.FetchRequest) (*sourcing.FetchedAsset, error) {
	if a.registry == nil {
		return nil, fmt.Errorf("register fetch provider registry not configured")
	}
	for _, p := range a.registry.ByCapability(providers.CapabilityFetch) {
		if p.Name() != "youtube" {
			continue
		}
		fp, ok := p.(providers.FetchProvider)
		if !ok {
			continue
		}
		res, err := fp.Fetch(ctx, providers.FetchRequest{
			AssetID:      req.AssetID,
			SourceRef:    req.SourceRef,
			SegmentStart: req.SegmentStart,
			SegmentEnd:   req.SegmentEnd,
		})
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		out := &sourcing.FetchedAsset{
			LocalPath: res.LocalPath,
			AssetID:   req.AssetID,
			Name:      "",
			Duration:  0,
			Bytes:     res.Bytes,
			Metadata:  map[string]string{},
		}
		if res.Asset != nil {
			out.AssetID = res.Asset.ID
			out.Name = res.Asset.Name
			out.Duration = res.Asset.Duration
			out.Metadata = map[string]string{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("youtube fetch provider not registered")
}
