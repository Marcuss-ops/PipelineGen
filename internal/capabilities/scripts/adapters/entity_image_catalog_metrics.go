package adapters

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func entityImageCatalogMetricsFor(metrics any) scriptmetrics.EntityImageCatalogMetrics {
	if metrics == nil {
		return nil
	}
	catalogMetrics, _ := metrics.(scriptmetrics.EntityImageCatalogMetrics)
	return catalogMetrics
}

func observeEntityImageCatalogLookup(metrics any, started time.Time) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.ObserveEntityImageCatalogLookup(time.Since(started).Seconds())
	}
}

func addEntityImageCatalogCandidates(metrics any, stage, outcome string, count int) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.AddEntityImageCatalogCandidates(stage, outcome, count)
	}
}

func incEntityImageCatalogLifecycle(metrics any, stage, outcome string) {
	if catalogMetrics := entityImageCatalogMetricsFor(metrics); catalogMetrics != nil {
		catalogMetrics.IncEntityImageCatalogLifecycle(stage, outcome)
	}
}

func filterPersonEntityImageCandidatesMeasured(identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate, metrics any) []scriptpkg.SegmentAssetCandidate {
	accepted, rejected := 0, 0
	for _, result := range results {
		ok, _, _, _ := evaluatePersonImageCandidate(identity, result)
		if ok {
			accepted++
		} else {
			rejected++
		}
	}
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageIdentity, scriptmetrics.EntityImageOutcomeAccepted, accepted)
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageIdentity, scriptmetrics.EntityImageOutcomeRejected, rejected)
	return filterPersonEntityImageCandidates(identity, results)
}

func persistEntityImageCatalogCandidatesMeasured(ctx context.Context, repo entitycatalog.Repository, identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate, metrics any) error {
	err := persistEntityImageCatalogCandidates(ctx, repo, identity, results)
	outcome := scriptmetrics.EntityImageOutcomeSuccess
	if err != nil {
		outcome = scriptmetrics.EntityImageOutcomeFailure
	} else {
		addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageCatalogPersist, scriptmetrics.EntityImageOutcomeAccepted, len(results))
	}
	incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageCatalogPersist, outcome)
	return err
}

func recordEntityImageVisualOutcomes(metrics any, candidates []scriptpkg.SegmentAssetCandidate, scoringErr error) {
	if scoringErr != nil {
		incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeFailure)
		return
	}
	if len(candidates) == 0 {
		incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeSkipped)
		return
	}
	accepted, rejected := 0, 0
	for _, candidate := range candidates {
		if candidate.SemanticStatus == entitycatalog.CandidateSemanticRejected ||
			(candidate.SemanticScore > 0 && candidate.SemanticScore < MinInternetImageSemanticScore) {
			rejected++
		} else {
			accepted++
		}
	}
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeAccepted, accepted)
	addEntityImageCatalogCandidates(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeRejected, rejected)
	incEntityImageCatalogLifecycle(metrics, scriptmetrics.EntityImageStageVisual, scriptmetrics.EntityImageOutcomeSuccess)
}

func observeEntityImageCatalogMaterialization(metrics any, started time.Time) {
	if started.IsZero() || metrics == nil {
		return
	}
	if catalogMetrics, ok := metrics.(scriptmetrics.EntityImageCatalogMetrics); ok {
		catalogMetrics.ObserveEntityImageCatalogMaterialization(time.Since(started).Seconds())
	}
}
