// Package scriptgeneration — vidrush_mediacert_barrier.go: the run-scoped
// final barrier that turns the certified media result into the job's
// terminal verdict.
//
// MediaCertBarrier wraps a VidRushBarrier so a SUCCEEDED run with
// CERTIFIED=false fails the job — the explicit rejection of the count-only
// test that declared success at a semantically broken pipeline (e.g. a boxing
// clip bound to Greek Salad). The port contracts it implements live in
// vidrush_semantic_ports.go.
//
// Extracted 2026-09-12 from vidrush_semantic_chain.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MediaCertBarrier wraps a VidRushBarrier and runs mediacert.Certify on the
// completed results before returning them. A CERTIFIED=false report fails the
// job even when the underlying barrier returned no error. This is the explicit
// rejection of the count-only test that declared success at a semantically
// broken pipeline (e.g. a boxing clip bound to Greek Salad).
type MediaCertBarrier struct {
	inner     VidRushBarrier
	certifier MediaCertifierPort
	spec      mediacert.Spec
}

// NewMediaCertBarrier wraps a barrier with a MediaCertifierPort + Spec. The
// spec is the golden Mediterranean fixture's expected contract in production;
// tests pass a synthetic spec. inner and certifier must be non-nil.
func NewMediaCertBarrier(inner VidRushBarrier, certifier MediaCertifierPort, spec mediacert.Spec) (*MediaCertBarrier, error) {
	if inner == nil {
		return nil, fmt.Errorf("scriptgeneration: inner VidRushBarrier is required for MediaCertBarrier")
	}
	if certifier == nil {
		return nil, fmt.Errorf("scriptgeneration: MediaCertifierPort is required for MediaCertBarrier")
	}
	return &MediaCertBarrier{inner: inner, certifier: certifier, spec: spec}, nil
}

// WaitForVidRush delegates to the inner barrier, then certifies the result.
// A CERTIFIED=false report returns an error so the runner fails the job.
func (b *MediaCertBarrier) WaitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	segments, err := b.inner.WaitForVidRush(ctx, runID)
	if err != nil {
		return nil, err
	}
	segments = filterEntityRenderSurface(segments)
	result := mediacert.MediaResult{
		JobStatus: "SUCCEEDED",
		Segments:  toMediaResultSegments(segments),
	}
	report, err := b.certifier.Certify(ctx, b.spec, result)
	if err != nil {
		return nil, fmt.Errorf("mediacert certify: %w", err)
	}
	if !report.Certified {
		var violations []string
		for _, c := range report.Checks {
			if !c.Passed {
				detail := string(c.Name)
				if len(c.Violations) > 0 {
					detail += ": " + c.Violations[0].Detail
				}
				violations = append(violations, detail)
			}
		}
		return nil, fmt.Errorf("vidrush semantic certification failed: CERTIFIED=false (%s)", strings.Join(violations, ", "))
	}
	return segments, nil
}

// filterEntityRenderSurface is the explicit product policy for the entity
// render path: only imageable named entities and important phrases cross the
// VidRush→render boundary. Value entities, concepts, keywords and important
// words remain useful to other editorial paths, but must not become entity
// overlays or affect the entity certification counts.
func filterEntityRenderSurface(segments []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, len(segments))
	for i, seg := range segments {
		out[i] = seg
		entities := make([]scriptpkg.ExtractedEntity, 0, len(seg.Insights.Entities))
		for _, entity := range seg.Insights.Entities {
			kind := scriptpkg.NormalizeAnnotationType(entity.Type)
			if !scriptpkg.IsAnnotationEntityKind(kind) {
				continue
			}
			entity.Type = kind
			entity.Value = strings.TrimSpace(entity.Value)
			if entity.Value == "" {
				continue
			}
			entities = append(entities, entity)
		}
		out[i].Insights.Entities = entities
		out[i].Insights.ImportantWords = nil
		// This render surface is intentionally narrower than the full media
		// retrieval surface: no stock-video or YouTube query may leak into an
		// entity-only run. The caller can run those providers in a separate
		// clip path, but they are not extracted here.
		out[i].Insights.ArtlistQueries = nil
		out[i].Insights.YouTubeQueries = nil
		// The entity value is the only allowed image query at this boundary.
		// Do not trust provider-generated/enriched queries: they can introduce
		// a second subject or a generic scene image and break entity↔asset
		// provenance. One deterministic query is emitted per imageable entity.
		queries := make([]string, 0, len(entities))
		seenQueries := make(map[string]struct{}, len(entities))
		for _, entity := range entities {
			q := strings.TrimSpace(entity.Value)
			key := strings.ToLower(q)
			if q == "" || key == "" {
				continue
			}
			if _, ok := seenQueries[key]; ok {
				continue
			}
			seenQueries[key] = struct{}{}
			queries = append(queries, q)
		}
		out[i].Insights.ImageQueries = queries
	}
	return out
}

// toMediaResultSegments projects the VidRushSegmentResult slice into the
// mediacert.ResultSegment shape so the certifier can check identity, profile,
// grounding, ownership, relevance and fanout without depending on the full
// VidRush wire shape.
func toMediaResultSegments(segments []scriptpkg.VidRushSegmentResult) []mediacert.ResultSegment {
	out := make([]mediacert.ResultSegment, 0, len(segments))
	for _, seg := range segments {
		profile := seg.CanonicalSemanticProfile()
		insights := seg.Insights
		if insights.VisualProfile == nil {
			visual := scriptpkg.BuildSegmentVisualProfile(profile)
			insights.VisualProfile = &visual
		}
		out = append(out, mediacert.ResultSegment{
			SegmentID:       seg.SegmentID,
			Position:        seg.Position,
			SourceText:      seg.Text,
			SourceTextHash:  seg.TextHash,
			SemanticProfile: &profile,
			Insights:        insights,
			Assets:          seg.Assets,
		})
	}
	return out
}

// Compile-time contract assertions: the new implementations satisfy the
// existing port interfaces so VidRushPipeline can swap them in without the
// coordinator knowing about the new chain.
var (
	_ SegmentEnricher         = (*SceneIRSegmentEnricher)(nil)
	_ SegmentProviderResolver = (*SemanticProviderResolver)(nil)
	_ VidRushBarrier          = (*MediaCertBarrier)(nil)
)
