package ingest

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

// ingestSourceFromClipPlan is the single mapping boundary between the legacy
// planner DTO and the neutral ingest contract. It intentionally performs no
// deduplication; the step owns ordering and calls ingest.UniqueSources.
func ingestSourceFromClipPlan(plan core.ClipPlan) Source {
	return Source{ID: plan.SourceID, URL: StagingSourceURL(plan)}
}

// ingestSourcesFromClipPlans maps the plan onto the ingest boundary: ONE entry
// per distinct SourceID in first-appearance order.
//
// Grouping (rather than the previous one-entry-per-plan list) is what makes the
// sections_only download window derivable: the span to download is a property of
// the source's whole clip group, not of a single plan. The returned list is
// therefore already deduplicated — the caller's ingest.UniqueSources remains a
// harmless no-op — and every entry carries the DownloadSection the stager must
// fetch (empty when the run stages whole sources).
func ingestSourcesFromClipPlans(plans []core.ClipPlan, sectioned bool) []Source {
	groups := make(map[string][]core.ClipPlan, len(plans))
	order := make([]string, 0, len(plans))
	for _, plan := range plans {
		if _, seen := groups[plan.SourceID]; !seen {
			order = append(order, plan.SourceID)
		}
		groups[plan.SourceID] = append(groups[plan.SourceID], plan)
	}

	sources := make([]Source, 0, len(order))
	for _, sourceID := range order {
		source := ingestSourceFromClipPlan(groups[sourceID][0])
		if sectioned {
			// A group that is not one contiguous slice leaves the section empty:
			// the stager then downloads the whole source, which is the only safe
			// answer for a plan it cannot bound.
			if start, end, ok := core.SectionWindowForPlans(groups[sourceID]); ok {
				source.DownloadSection = core.SectionDownloadString(start, end)
			}
		}
		sources = append(sources, source)
	}
	return sources
}

// StagerPreparer adapts the canonical acquisition port to the neutral
// ingest boundary. It keeps acquisition request construction in the parent
// package, where policy-version and caller identity are already available.
type StagerPreparer struct {
	stager        acquisition.SourceStager
	policyVersion string
}

var _ SourcePreparer = (*StagerPreparer)(nil)

func (p *StagerPreparer) Prepare(ctx context.Context, source Source) (*PreparedSource, error) {
	if p == nil || p.stager == nil {
		return nil, fmt.Errorf("stock ingest: source stager is not wired")
	}

	// DownloadSection MUST travel to the stager: dropping it here (as this
	// adapter did) silently turned every sections_only run into a full-source
	// download. ForceKeyframes re-encodes the cut so the staged file starts at
	// exactly the requested timestamp — a keyframe-snapped section would shift
	// every subsequent local seek by up to one GOP.
	section := strings.TrimSpace(source.DownloadSection)
	ref := acquisition.SourceRef{URL: source.URL, DownloadSection: section, ForceKeyframes: section != ""}
	// The idempotency key includes the section, so two different slices of the
	// same URL can never share a staged artifact.
	keyRef := acquisition.SourceRef{URL: source.URL, DownloadSection: section, PolicyVersion: p.policyVersion}
	prepared, err := p.stager.Prepare(ctx, acquisition.PrepareRequest{
		Source:         ref,
		IdempotencyKey: acquisition.DeriveIdempotencyKey(keyRef),
		CallerRef:      "stock.stage_sources",
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return nil, nil
	}
	return &PreparedSource{
		SourceID:     source.ID,
		LocalPath:    prepared.LocalPath,
		Bytes:        prepared.SizeBytes,
		SourceWindow: prepared.SourceWindow,
	}, nil
}

// NewStagerPreparer adapts an acquisition.SourceStager to this package's
// neutral SourcePreparer boundary.
func NewStagerPreparer(stager acquisition.SourceStager, policyVersion string) SourcePreparer {
	return &StagerPreparer{stager: stager, policyVersion: policyVersion}
}
