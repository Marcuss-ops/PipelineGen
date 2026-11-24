package vidrush

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ValidateVidRushSegmentAssetBindings is the fail-closed provenance gate for
// the final asset surface. It verifies all required identity fields and rejects
// an asset id appearing under more than one segment.
func ValidateVidRushSegmentAssetBindings(segments []scriptpkg.VidRushSegmentResult) error {
	assetSegments := make(map[string]string)
	for _, segment := range segments {
		segmentID := strings.TrimSpace(segment.SegmentID)
		if segmentID == "" {
			return fmt.Errorf("asset provenance: segment_id is required")
		}
		if strings.TrimSpace(segment.TextHash) == "" {
			return fmt.Errorf("asset provenance: text_hash is required for segment %s", segmentID)
		}
		candidates := append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...)
		candidates = append(candidates, segment.Assets.SecondaryImages...)
		candidates = append(candidates, segment.Assets.GeneratedImages...)
		if segment.Assets.PrimaryVideo != nil {
			candidates = append(candidates, *segment.Assets.PrimaryVideo)
		}
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.SegmentID) != segmentID || candidate.Position != segment.Position || strings.TrimSpace(candidate.TextHash) != strings.TrimSpace(segment.TextHash) {
				return fmt.Errorf("asset provenance: asset %q is bound to segment %s with conflicting segment identity", candidate.AssetID, segmentID)
			}
			for field, value := range map[string]string{
				"entity_id": candidate.EntityID, "query": candidate.Query,
				"asset_id": candidate.AssetID, "provider": candidate.Provider,
			} {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("asset provenance: %s is required for asset in segment %s", field, segmentID)
				}
			}
			assetID := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if previous, exists := assetSegments[assetID]; exists && previous != segmentID {
				return fmt.Errorf("asset provenance: asset %q is bound to segments %s and %s", candidate.AssetID, previous, segmentID)
			}
			assetSegments[assetID] = segmentID
		}
	}
	return nil
}

func normalizeVidRushSegmentAssets(segment *scriptpkg.VidRushSegmentResult) {
	if segment == nil || strings.TrimSpace(segment.SegmentID) == "" {
		return
	}
	segment.Assets.Candidates = normalizeVidRushCandidateList(segment.Assets.Candidates, *segment)
	segment.Assets.SecondaryImages = normalizeVidRushCandidateList(segment.Assets.SecondaryImages, *segment)
	segment.Assets.GeneratedImages = normalizeVidRushCandidateList(segment.Assets.GeneratedImages, *segment)
	if segment.Assets.PrimaryVideo != nil {
		if primary, ok := normalizeVidRushCandidate(*segment.Assets.PrimaryVideo, *segment); ok {
			segment.Assets.PrimaryVideo = &primary
		} else {
			segment.Assets.PrimaryVideo = nil
		}
	}
}

func CloneVidRushSegmentResult(in scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := in
	out.Warnings = append([]string(nil), in.Warnings...)
	if strings.TrimSpace(out.TextHash) == "" {
		identityText := out.Text
		if strings.TrimSpace(identityText) == "" {
			identityText = out.SegmentID
		}
		if strings.TrimSpace(identityText) != "" {
			out.TextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
		}
	}
	out.Insights.Entities = append([]scriptpkg.ExtractedEntity(nil), in.Insights.Entities...)
	if in.Insights.VisualProfile != nil {
		visualProfile := *in.Insights.VisualProfile
		visualProfile.Terms = append([]string(nil), in.Insights.VisualProfile.Terms...)
		out.Insights.VisualProfile = &visualProfile
	}
	out.Insights.ImportantPhrases = append([]string(nil), in.Insights.ImportantPhrases...)
	out.Insights.ImportantWords = append([]string(nil), in.Insights.ImportantWords...)
	out.Insights.SpecialNames = append([]string(nil), in.Insights.SpecialNames...)
	out.Insights.YouTubeQueries = append([]string(nil), in.Insights.YouTubeQueries...)
	out.Insights.ImageQueries = append([]string(nil), in.Insights.ImageQueries...)
	out.Insights.ResearchSources = append([]scriptpkg.ResearchWebSource(nil), in.Insights.ResearchSources...)
	out.Insights.EntityMediaLinks = append([]scriptpkg.EntityMediaLink(nil), in.Insights.EntityMediaLinks...)
	if in.Insights.ImageEntityCanonicalIDs != nil {
		out.Insights.ImageEntityCanonicalIDs = make(map[string]string, len(in.Insights.ImageEntityCanonicalIDs))
		for key, id := range in.Insights.ImageEntityCanonicalIDs {
			out.Insights.ImageEntityCanonicalIDs[key] = id
		}
	}
	if in.Insights.ImageEntityCanonicalNames != nil {
		out.Insights.ImageEntityCanonicalNames = make(map[string]string, len(in.Insights.ImageEntityCanonicalNames))
		for key, name := range in.Insights.ImageEntityCanonicalNames {
			out.Insights.ImageEntityCanonicalNames[key] = name
		}
	}
	out.Assets.SecondaryImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.SecondaryImages...)
	out.Assets.GeneratedImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.GeneratedImages...)
	out.Assets.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.Candidates...)
	if in.Assets.PrimaryVideo != nil {
		primary := *in.Assets.PrimaryVideo
		out.Assets.PrimaryVideo = &primary
	}
	normalizeVidRushSegmentAssets(&out)
	return out
}

// FinalizeVidRushBindings is the single binding finalization step for the
// per-segment result. It accepts only provider candidates with provenance,
// computes a stable candidate-set hash, and records binding cache state.
// Provider processors remain responsible for searching; this function only
// normalizes and selects from their closed candidate set.
func FinalizeVidRushBindings(segments []scriptpkg.VidRushSegmentResult, forceRefresh bool) []scriptpkg.VidRushSegmentResult {
	return FinalizeVidRushBindingsWithCache(context.Background(), segments, forceRefresh, nil)
}

// DeduplicateVidRushSegments collapses repeated processor deltas by the
// canonical segment id while preserving provider candidates and insights.
func DeduplicateVidRushSegments(segments []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	return mergeVidRushSegments(nil, segments)
}

type vidRushAcquireVerifyResult struct {
	candidate scriptpkg.SegmentAssetCandidate
	verified  scriptports.VerifiedArtifact
	err       error
	stage     string
}

// acquireAndVerify is the sole remote lifecycle boundary. It owns only
// provider calls, their timeouts, and canonical operation measurements; the
// caller owns warnings, catalog side effects, retries, and finalization.
func acquireAndVerify(ctx context.Context, provider scriptports.VidRushAssetProvider, candidate scriptpkg.SegmentAssetCandidate, providerName string, metrics VidRushTimingMetrics) vidRushAcquireVerifyResult {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, vidRushProviderTimeout(providerName))
	var local scriptports.LocalArtifact
	err := measureVidRushProvider(acquireCtx, metrics, kernobs.OperationInfo{
		Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "acquire", Provider: providerName,
	}, func(callCtx context.Context) error {
		var acquireErr error
		local, acquireErr = provider.Acquire(callCtx, candidate)
		return acquireErr
	})
	cancelAcquire()
	if err != nil {
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusFailed
		return vidRushAcquireVerifyResult{candidate: candidate, err: err, stage: "acquire"}
	}

	verifyCtx, cancelVerify := context.WithTimeout(ctx, vidRushVerifyTimeout)
	var verified scriptports.VerifiedArtifact
	err = measureVidRushProvider(verifyCtx, metrics, kernobs.OperationInfo{
		Stage: kernobs.StageVerify, Component: "vidrush", Operation: "verify", Provider: providerName,
	}, func(callCtx context.Context) error {
		var verifyErr error
		verified, verifyErr = provider.Verify(callCtx, local)
		return verifyErr
	})
	cancelVerify()
	if err != nil {
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
		candidate.VerificationStatus = scriptpkg.VidRushStatusFailed
		return vidRushAcquireVerifyResult{candidate: candidate, err: err, stage: "verify"}
	}
	return vidRushAcquireVerifyResult{candidate: verified.Candidate, verified: verified}
}

const (
	// vidRushFinalizeAttempts bounds the synchronous persistence retry. A
	// transient Drive/SQLite/outbox failure must not defer an already
	// acquired+verified asset to the next run (ACC-9 class).
	vidRushFinalizeAttempts = 3
	// vidRushFinalizeRetryDelay is the base backoff; attempt N waits N×base.
	// Kept short: finalization runs inside the job's stage budget.
	vidRushFinalizeRetryDelay = 150 * time.Millisecond
)

// finalizeWithRetry is the synchronous persistence boundary: the finalizer is
// idempotent by content identity ("vidrush:<provider>:<asset>:<sha256>"), so
// retrying a transient failure in-run cannot duplicate an artifact, while a
// failure that exhausts the attempts is recorded with its structured cause
// instead of leaving persistence_status=failed without a reason.
func finalizeWithRetry(ctx context.Context, finalizer scriptports.VidRushArtifactFinalizer, verified scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	if finalizer == nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: not configured")
	}
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= vidRushFinalizeAttempts; attempt++ {
		attempts = attempt
		persisted, err := finalizer.Finalize(ctx, verified)
		if err == nil {
			return persisted, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == vidRushFinalizeAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt) * vidRushFinalizeRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("finalize retry aborted after %d attempt(s): %w", attempts, lastErr)
		case <-timer.C:
		}
	}
	return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("finalize failed after %d attempt(s): %w", attempts, lastErr)
}

// normalizeVidRushCandidate stamps the canonical segment envelope onto a
// provider candidate. Existing legacy candidates may omit the envelope and
// are upgraded from their owning segment; an already-stamped candidate with
// a conflicting identity is rejected rather than rebound to another segment.
func normalizeVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
	segmentID := strings.TrimSpace(segment.SegmentID)
	if segmentID == "" {
		return candidate, false
	}
	if stamped := strings.TrimSpace(candidate.SegmentID); stamped != "" && stamped != segmentID {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if stamped := strings.TrimSpace(candidate.TextHash); stamped != "" && strings.TrimSpace(segment.TextHash) != "" && stamped != strings.TrimSpace(segment.TextHash) {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if strings.TrimSpace(candidate.SegmentID) != "" && candidate.Position != segment.Position {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	candidate.SegmentID = segmentID
	candidate.Position = segment.Position
	candidate.TextHash = strings.TrimSpace(candidate.TextHash)
	if candidate.TextHash == "" {
		candidate.TextHash = strings.TrimSpace(segment.TextHash)
	}
	if candidate.TextHash == "" {
		identityText := segment.Text
		if strings.TrimSpace(identityText) == "" {
			identityText = segment.SegmentID
		}
		candidate.TextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
	}
	candidate.Query = strings.TrimSpace(candidate.Query)
	if candidate.Query == "" {
		candidate.Query = firstSegmentAssetQuery(segment)
	}
	candidate.Provider = strings.TrimSpace(candidate.Provider)
	candidate.AssetID = strings.TrimSpace(candidate.AssetID)
	candidate.Entity = strings.TrimSpace(candidate.Entity)
	if strings.TrimSpace(candidate.EntityID) == "" {
		identity := candidate.Entity
		if identity == "" {
			identity = candidate.Query
		}
		if identity == "" {
			identity = segmentID
		}
		candidate.EntityID = "entity:" + provenanceSlug(identity)
	}
	return candidate, candidate.AssetID != "" && candidate.Provider != ""
}

func firstSegmentAssetQuery(segment scriptpkg.VidRushSegmentResult) string {
	for _, query := range [][]string{segment.Insights.ImageQueries, segment.Insights.YouTubeQueries} {
		for _, value := range query {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	if text := strings.TrimSpace(segment.Text); text != "" {
		return text
	}
	return strings.TrimSpace(segment.SegmentID)
}

func provenanceSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeVidRushCandidateList(candidates []scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	// Steady-state fast path: once a candidate is stamped, every clone pass
	// re-normalizes it to a byte-identical copy. Returning the caller-owned
	// slice instead of allocating a fresh one removes three heap allocations
	// per segment per processor pass (Candidates/SecondaryImages/Generated
	// Images). Every call site owns the slice exclusively or passes it as a
	// read-only merge input, so aliasing is safe. The copy-on-write path is
	// taken only when a candidate is rewritten or dropped, producing the same
	// output the old always-copy loop did.
	out := candidates
	rewritten := false
	for i, candidate := range candidates {
		normalized, ok := normalizeVidRushCandidate(candidate, segment)
		if !ok {
			// Conflicting/stale candidate: dropped, so the output differs from
			// the input and the copy path is required.
			if !rewritten {
				out = make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
				out = append(out, candidates[:i]...)
				rewritten = true
			}
			continue
		}
		if rewritten {
			out = append(out, normalized)
			continue
		}
		if normalized != candidate {
			out = make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
			out = append(out, candidates[:i]...)
			out = append(out, normalized)
			rewritten = true
		}
	}
	return out
}
