// Package texttracks — backfill_candidates.go (LEAF):
//
// Pre-filter helpers for the backfill pipeline.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "list candidate clips" + "is this clip missing for the target
// set" decisions. The high-level Run loop delegates here;
// inline ListReadyLanguages / List calls are forbidden in
// other leaves.
//
// Cross-file callers (same package):
//   - backfill.go        : declares the MediaAssetLister port
//     that this leaf consumes.
//   - backfill_run.go    : calls ListCandidates (full pipeline)
//     and IsAssetMissingForTargetSet
//     (--only-missing pre-filter).
//   - cmd/admin/text_tracks_backfill.go: calls ListCandidates
//     directly on the dry-run path (--
//     dry-run is list-only; no DB writes).
package texttracks

import (
	"context"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ListCandidates queries media_assets per the operator's filter.
// Only the assets matching the source filter are returned; the
// --only-missing filter is applied at the per-clip level (via
// ListReadyLanguages) so the SQL stays simple.
//
// godlike/06 SSOT: the source-filter decision (Source field +
// MediaType="clip" exclusion) is owned ONLY here. Other leaves
// MUST NOT inline a List() call with bespoke filters.
func (s *BackfillService) ListCandidates(
	ctx context.Context,
	opts BackfillOptions,
) ([]*asset.Asset, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	filter := asset.Filter{
		Source: opts.Source,
	}
	if len(opts.AssetIDs) > 0 {
		filter.IDs = append([]string{}, opts.AssetIDs...)
	} else {
		filter.MediaType = "clip" // exclude folders on broad scans
	}
	if opts.Limit > 0 {
		filter.Limit = opts.Limit
	}
	assets, err := s.clips.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("texttracks.BackfillService.ListCandidates: list: %w", err)
	}
	return assets, nil
}

// IsAssetMissingForTargetSet reports whether the asset has fewer
// READY target-language tracks than the operator's target set.
// The check uses the TextTrackRepository's ListReadyLanguages
// (NOT inline SQL) so the canonical "READY-only" decision stays
// in one place (godlike/06 SSOT).
//
// Returns (true, nil) when the asset is missing ≥ 1 target
// language. Returns (false, nil) when the asset has all target
// languages READY (or the target set is empty).
//
// godlike/06 SSOT: the target-set vs READY-set diff lives ONLY
// here. Other leaves MUST delegate to this method instead of
// re-implementing the lookup.
func (s *BackfillService) IsAssetMissingForTargetSet(
	ctx context.Context,
	repo detail.TextTrackRepository,
	assetID string,
	opts BackfillOptions,
) (bool, error) {
	if len(opts.TargetLanguages) == 0 {
		return true, nil
	}
	if repo == nil {
		return true, nil
	}
	ready, err := repo.ListReadyLanguages(ctx, assetID, opts.TextKind)
	if err != nil {
		return false, fmt.Errorf("texttracks.BackfillService.IsAssetMissingForTargetSet: list ready: %w", err)
	}
	readySet := make(map[string]struct{}, len(ready))
	for _, lang := range ready {
		readySet[lang] = struct{}{}
	}
	for _, target := range opts.TargetLanguages {
		if _, ok := readySet[target]; !ok {
			return true, nil
		}
	}
	return false, nil
}
