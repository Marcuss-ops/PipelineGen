// Package adapters — media_resolver_entity_binding.go: the entity-image
// projection half of the media resolver image stage.
//
// It answers one question: given a scene's certified PrimaryEntities and the
// materialized segment candidates, which provider candidate (if any) may be
// bound as that entity's card image? Generic scene candidates are never
// promoted to an entity image, and only internet-images candidates may bind.
//
// Extracted 2026-09-12 from media_resolver_image_stage.go to keep both halves
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// isPersonOrgOrPlace is the query-building subset of the imageable kinds:
// people, organisations and places. The spellings come from
// kernel/entitytype, so GPE, LOC, CITY and LOCATION all agree.
func isPersonOrgOrPlace(raw string) bool {
	switch entitytype.Fold(raw) {
	case entitytype.Person, entitytype.Org, entitytype.Place:
		return true
	}
	return false
}

// projectEntityImageBindings attaches only provider candidates that are
// explicitly relevant to the primary entity. Generic scene candidates are
// never promoted to an entity image, preventing unrelated images from being
// presented as a person/org/place match.
func projectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	if !policy.Enabled || len(spec.Scenes) == 0 {
		return spec
	}
	out := cloneSpecSceneOutput(spec)
	// The default imageable set IS the kernel taxonomy registry
	// (script.AnnotationEntityKinds): PERSON/ORG/GPE entity cards plus the
	// PRODUCT/LOGO media kinds. An explicit policy replaces the set.
	allowed := make(map[string]bool, 5)
	for _, kind := range scriptpkg.AnnotationEntityKinds() {
		allowed[kind] = true
	}
	if len(policy.EntityTypes) > 0 {
		allowed = make(map[string]bool, len(policy.EntityTypes))
		for _, raw := range policy.EntityTypes {
			allowed[normalizeAnnotationType(raw)] = true
		}
	}
	// Pre-index segments by SegmentID and SceneID (first occurrence wins, so
	// the min-index semantics of the old linear scan are preserved). The
	// per-scene lookup then becomes O(1) instead of a full O(segments) scan
	// for every scene.
	bySegmentID := make(map[string]int, len(segments))
	bySceneID := make(map[string]int, len(segments))
	for i := range segments {
		if segments[i].SegmentID != "" {
			if _, ok := bySegmentID[segments[i].SegmentID]; !ok {
				bySegmentID[segments[i].SegmentID] = i
			}
		}
		if segments[i].SceneID != "" {
			if _, ok := bySceneID[segments[i].SceneID]; !ok {
				bySceneID[segments[i].SceneID] = i
			}
		}
	}
	resolvedEntityAssets := make(map[string]struct{}, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	// usedCandidates is the run-scoped distribution ledger. The entity image
	// library is SHARED across runs on purpose (published once per entity,
	// reused for cost). Within one run each occurrence still PREFERS an unused
	// variant, but a person with only one verified portrait must be able to
	// reuse it in every scene: per_scene controls overlay placement, not the
	// obligation to find N distinct photos of one face.
	usedCandidates := make(map[string]struct{})
	for i := range out.Scenes {
		if out.Scenes[i].Annotations == nil {
			continue
		}
		seg := findSegmentForScene(out.Scenes[i], segments, bySegmentID, bySceneID)
		bindEntity := func(entity *scriptpkg.AnnotatedEntity) {
			if !allowed[normalizeAnnotationType(entity.Type)] {
				return
			}
			// A content-addressed binding written by the semantic production
			// plan is a fallback the media stage must not erase when it has no
			// candidate of its own: the hit was verified, it simply was not
			// discovered by this segment's fan-out.
			previousImage := entity.Image
			entity.Image = &scriptpkg.EntityImageBinding{Status: "not_found"}
			if seg == nil {
				entity.Image = missingEntityImageBinding(previousImage, "SEGMENT_MISSING")
				return
			}
			variantSeed := strings.TrimSpace(out.Scenes[i].ID) + "|" + entityImageIdentity(*entity)
			candidate, ok := findEntityImageCandidateFrom(*entity, *seg, variantSeed, usedCandidates)
			if !ok {
				// The unused-variant pass is a preference, not a requirement.
				// Reusing one verified portrait across scenes is correct when the
				// entity pool has a single usable image.
				candidate, ok = findEntityImageCandidateFrom(*entity, *seg, variantSeed)
			}
			if ok {
				assetKey := vidRushCandidateIdentity(candidate)
				if assetKey == "" {
					assetKey = strings.TrimSpace(candidate.AssetID)
				}
				if assetKey != "" {
					usedCandidates[assetKey] = struct{}{}
					if _, alreadyBound := resolvedEntityAssets[assetKey]; !alreadyBound {
						if len(resolvedEntityAssets) >= capabilityoverlay.MaxEntityImageOverlaysPerRun {
							// Keep the entity in the semantic result, but do not bind
							// another distinct asset once the run-level render budget is
							// full. Reusing an already-admitted asset stays free: the budget
							// counts unique images, not scene occurrences.
							entity.Image = missingEntityImageBinding(previousImage, "BUDGET_EXHAUSTED", candidate.Query, candidate.AssetID, candidate.SelectionReason)
							return
						}
						resolvedEntityAssets[assetKey] = struct{}{}
					}
				}
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					Query: candidate.Query, CandidateID: firstNonEmptyImageValue(candidate.AssetID, vidRushCandidateIdentity(candidate)),
					SelectionReason: candidate.SelectionReason,
					DriveLink:       candidate.DriveLink, DriveFileID: entityImageDriveFileID(candidate), Source: candidate.Provider,
					LocalPath:  candidate.LocalPath,
					MediaType:  candidate.MIMEType,
					License:    candidate.RightsBasis,
					PreviewURL: entityImagePreviewURL(candidate),
					// The verified content address is what lets the binding be
					// promoted into the content-addressed EntityMediaIndex for
					// the entity card asset (bindings without it stay plain
					// references).
					//
					// MEDIA-IDENTITY (Sept 2026): the candidate field is named
					// ContentSHA256 for historical reasons, so it is RESOLVED through
					// the canonical rule rather than forwarded verbatim — only a 64-hex
					// SHA-256 is a content address, and a legacy MD5 becomes "" so the
					// binding stays a plain reference instead of claiming an identity
					// it cannot prove.
					SHA256: asset.ResolveContentAddress(candidate.ContentSHA256),
				}
			} else {
				entity.Image = missingEntityImageBinding(previousImage, entityImageFailureReason(*entity, *seg), entityImageSearchQuery(*entity, *seg))
			}
		}
		for entityIndex := range out.Scenes[i].Annotations.PrimaryEntities {
			bindEntity(&out.Scenes[i].Annotations.PrimaryEntities[entityIndex])
		}
		for entityIndex := range out.Scenes[i].Annotations.SecondaryEntities {
			bindEntity(&out.Scenes[i].Annotations.SecondaryEntities[entityIndex])
		}
	}
	return out
}

// entityImageDriveFileID resolves the canonical Drive file id from the
// candidate's public link. The finalizer stores only DriveLink on a
// SegmentAssetCandidate, but the plan/audit contract requires the id to prove
// that an uploaded entity image is drive-addressable; deriving it here keeps
// the projection lossless without widening the candidate schema.
func entityImageDriveFileID(candidate scriptpkg.SegmentAssetCandidate) string {
	link := strings.TrimSpace(candidate.DriveLink)
	if link == "" {
		return ""
	}
	id, err := urlutil.FileIDFromDriveLink(link)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

// preservedPlanImageBinding keeps a verified binding that the planner wrote
// when the media stage has nothing to replace it with. Only a bound, hashed
// image qualifies: an unhashed reference cannot become a card asset, and a
// media-stage "resolved" binding follows its own re-projection rules.
func preservedPlanImageBinding(binding *scriptpkg.EntityImageBinding) *scriptpkg.EntityImageBinding {
	if binding != nil && strings.EqualFold(strings.TrimSpace(binding.Status), "bound") && strings.TrimSpace(binding.SHA256) != "" {
		return binding
	}
	return &scriptpkg.EntityImageBinding{Status: "not_found"}
}

func missingEntityImageBinding(previous *scriptpkg.EntityImageBinding, reason string, provenance ...string) *scriptpkg.EntityImageBinding {
	if preserved := preservedPlanImageBinding(previous); preserved != nil && preserved.Status != "not_found" {
		return preserved
	}
	binding := &scriptpkg.EntityImageBinding{Status: "not_found", FailureReason: reason}
	if len(provenance) > 0 {
		binding.Query = provenance[0]
	}
	if len(provenance) > 1 {
		binding.CandidateID = provenance[1]
	}
	if len(provenance) > 2 {
		binding.SelectionReason = provenance[2]
	}
	return binding
}

func entityImageSearchQuery(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) string {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	for query, canonicalID := range seg.Insights.ImageEntityCanonicalIDs {
		if normalizeEntityMatch(query) == want && canonicalID != "" {
			return strings.TrimSpace(query)
		}
	}
	return strings.TrimSpace(entity.CanonicalName)
}

// entityImageFailureReason distinguishes why an entity image could not bind.
// NO_QUERY, PROVIDER_EMPTY, IDENTITY_MISMATCH, UNVERIFIED and NOT_MATERIALIZED
// are different operational problems; collapsing them into SEARCH_EMPTY made
// a provider outage look like a missing plan query.
func entityImageFailureReason(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) string {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	want = strings.TrimSpace(strings.TrimPrefix(want, "describe "))
	candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	matching := 0
	unverified := false
	unmaterialized := false
	for _, candidate := range candidates {
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) {
			continue
		}
		identity := normalizeEntityMatch(candidate.Entity)
		query := normalizeEntityMatch(candidate.Query)
		if want != "" && identity != want && !strings.Contains(query, want) {
			continue
		}
		matching++
		switch strings.ToLower(strings.TrimSpace(candidate.LifecycleFailureStage)) {
		case scriptpkg.VidRushLifecycleStageAcquire:
			return "DOWNLOAD_FAILED"
		case scriptpkg.VidRushLifecycleStageVerify:
			return "UNVERIFIED_IMAGE"
		case scriptpkg.VidRushLifecycleStageFinalize:
			return "UPLOAD_FAILED"
		}
		if strings.EqualFold(candidate.AcquisitionStatus, "failed") {
			return "DOWNLOAD_FAILED"
		}
		if strings.EqualFold(candidate.VerificationStatus, "failed") {
			return "UNVERIFIED_IMAGE"
		}
		if strings.EqualFold(candidate.PersistenceStatus, "failed") {
			return "UPLOAD_FAILED"
		}
		if !validVidRushCandidate(candidate) || !imagegate.CandidateEligibleForSlot(candidate, false) {
			unverified = true
			continue
		}
		if !readyVidRushCandidate(candidate) && !verifiedImageCandidateWithoutDrive(candidate) {
			unmaterialized = true
		}
	}
	if matching == 0 {
		if len(seg.Insights.ImageQueries) == 0 {
			return "NO_QUERY"
		}
		return "PROVIDER_EMPTY"
	}
	if unverified {
		return "UNVERIFIED"
	}
	if unmaterialized {
		return "NOT_MATERIALIZED"
	}
	return "IDENTITY_MISMATCH"
}

func firstNonEmptyImageValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func entityImageIdentity(entity scriptpkg.AnnotatedEntity) string {
	name := normalizeEntityMatch(entity.CanonicalName)
	if name == "" {
		name = normalizeEntityMatch(entity.Text)
	}
	return strings.ToUpper(strings.TrimSpace(entity.Type)) + "|" + name
}

// ProjectEntityImageBindings reapplies the canonical identity-image
// projection to a final scene envelope. It is intentionally exported for the
// persistence boundary: later postprocessors may rebuild annotations while
// retaining the already materialized segment candidates.
func ProjectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	return projectEntityImageBindings(spec, segments, policy)
}

// sceneIdentityIndex pre-indexes spec.Scenes by SegmentID and ID so the
// per-segment scene lookup is O(1) instead of a full O(scenes) scan for
// every segment. firstNoID records the first scene carrying neither
// identity key (the "matches any segment" fallback).
