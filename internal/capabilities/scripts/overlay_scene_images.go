package scriptgeneration

import (
	"encoding/hex"
	"log"
	"net/url"
	"os"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	cryptodigest "github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sceneEntityImageHashes returns content hashes already used by entity cards.
// Generic scene stills must use a different source image so the same portrait
// is not replayed immediately after its captioned entity card.
func sceneEntityImageHashes(result *GenerateResult) map[string]struct{} {
	hashes := make(map[string]struct{})
	if result == nil {
		return hashes
	}
	for _, scene := range result.Scenes {
		if scene.Annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			if entity.Image == nil {
				continue
			}
			if hash := strings.ToLower(strings.TrimSpace(entity.Image.SHA256)); hash != "" {
				hashes[hash] = struct{}{}
			}
		}
	}
	return hashes
}

// sceneImageCandidate projects one already-materialized still image onto its
// owning scene. Scene images are independent of entity cards: recurring people
// can keep their canonical portrait while each scene gets its own contextual
// visual. Timing is anchored at the certified scene start, never estimated
// from the narration text.
func sceneImageCandidate(result *GenerateResult, sceneID string, startUS int64, excludedHashes map[string]struct{}, sceneEndUS int64) (capabilityoverlay.ImageCandidate, bool) {
	candidates := sceneImageCandidates(result, sceneID, startUS, excludedHashes, sceneEndUS, 1)
	if len(candidates) == 0 {
		return capabilityoverlay.ImageCandidate{}, false
	}
	return candidates[0], true
}

// sceneImageCandidates projects distinct contextual stills onto one scene.
// When a scene has enough certified duration, later stills run sequentially
// after the first one, preserving the replace-only image timeline contract.
func sceneImageCandidates(result *GenerateResult, sceneID string, startUS int64, excludedHashes map[string]struct{}, sceneEndUS int64, limit int) []capabilityoverlay.ImageCandidate {
	if result == nil || strings.TrimSpace(sceneID) == "" || startUS < 0 {
		return nil
	}
	if limit <= 0 {
		return nil
	}
	localHashes := make(map[string]struct{}, len(excludedHashes)+limit)
	for hash := range excludedHashes {
		localHashes[hash] = struct{}{}
	}
	var out []capabilityoverlay.ImageCandidate
	// Per-scene decision counters. They explain an image-free scene with
	// numbers (R23: 482 materialized, 1 planned) instead of requiring a
	// re-run with ad-hoc instrumentation.
	segmentCandidates, readyCandidates, excludedByHash := 0, 0, 0
	logSelection := func() []capabilityoverlay.ImageCandidate {
		log.Printf("scene images: scene_id=%s segment_candidates=%d ready=%d excluded_by_hash=%d limit=%d emitted=%d",
			strings.TrimSpace(sceneID), segmentCandidates, readyCandidates, excludedByHash, limit, len(out))
		return out
	}
	nextStartUS := ((startUS / 1000) * 1000) + capabilityoverlay.MaxImageOverlayDurationMS*1000
	for _, pin := range result.OverlayImages {
		if !strings.EqualFold(strings.TrimSpace(pin.SceneID), strings.TrimSpace(sceneID)) {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(pin.SHA256))
		path := strings.TrimSpace(pin.LocalPath)
		if strings.TrimSpace(pin.AssetID) == "" || len(digest) != 64 || path == "" {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if cryptodigest.SHA256Bytes(data) != digest {
			continue
		}
		if _, duplicate := localHashes[digest]; duplicate {
			continue
		}
		durationUS := int64(capabilityoverlay.MaxImageOverlayDurationMS * 1000)
		if sceneEndUS > 0 {
			if remainingUS := sceneEndUS - nextStartUS; remainingUS <= 0 {
				break
			} else if remainingUS < durationUS {
				durationUS = remainingUS
			}
		}
		out = append(out, capabilityoverlay.ImageCandidate{
			AssetID: digest, LocalPath: path, SHA256: digest, MediaType: strings.TrimSpace(pin.MIMEType),
			StartMs: nextStartUS / 1000, EndMs: (nextStartUS + durationUS + 999) / 1000,
			StartUS: nextStartUS, DurationUS: durationUS, Score: 1, Pinned: true,
		})
		localHashes[digest] = struct{}{}
		if durationUS < int64(capabilityoverlay.MaxImageOverlayDurationMS*1000) || len(out) >= limit {
			return logSelection()
		}
		nextStartUS += durationUS
	}
	for _, segment := range result.Segments {
		if segment.SceneID != sceneID && segment.SegmentID != sceneID {
			continue
		}
		candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.SecondaryImages...), segment.Assets.Candidates...)
		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			segmentCandidates++
			assetID := strings.TrimSpace(candidate.AssetID)
			digest := strings.TrimSpace(candidate.ContentSHA256)
			if _, duplicate := seen[assetID]; duplicate {
				continue
			}
			seen[assetID] = struct{}{}
			if !sceneImageCandidateReady(candidate, digest) {
				continue
			}
			readyCandidates++
			digest = strings.ToLower(digest)
			if _, duplicate := localHashes[digest]; duplicate {
				excludedByHash++
				continue
			}
			// Prefer the durable Drive copy: the provider URL is not stable for
			// a content-addressed asset and can return different bytes later,
			// failing the renderer's prefetch with HTTP 422.
			resourceURL := ""
			if id := driveFileID(candidate.DriveLink); id != "" {
				resourceURL = "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.DriveLink)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.SourceURL)
			}
			if resourceURL == "" {
				resourceURL = strings.TrimSpace(candidate.PreviewURL)
			}
			// Entity cards keep the opening beat. Context images start after the
			// five-second card window, then run sequentially without overlap.
			durationUS := int64(capabilityoverlay.MaxImageOverlayDurationMS * 1000)
			// A scene image starts after the opening entity-card window. Short
			// scenes may not have a full image interval left, so end it exactly
			// at the owning scene boundary instead of failing timeline preflight.
			if sceneEndUS > 0 {
				if remainingUS := sceneEndUS - nextStartUS; remainingUS <= 0 {
					break
				} else if remainingUS < durationUS {
					durationUS = remainingUS
				}
			}
			out = append(out, capabilityoverlay.ImageCandidate{
				AssetID: digest, URL: resourceURL, LocalPath: strings.TrimSpace(candidate.LocalPath),
				SHA256: digest, MediaType: strings.TrimSpace(candidate.MIMEType),
				StartMs: nextStartUS / 1000, EndMs: (nextStartUS + durationUS + 999) / 1000,
				StartUS: nextStartUS, DurationUS: durationUS,
				Score: 1,
			})
			localHashes[digest] = struct{}{}
			if durationUS < int64(capabilityoverlay.MaxImageOverlayDurationMS*1000) {
				break
			}
			if len(out) >= limit {
				return logSelection()
			}
			nextStartUS += durationUS
		}
		return logSelection()
	}
	return logSelection()
}

// entityImageCardReservation counts the scene's unique image-bearing entity
// cards. Rust admits base candidates (generic stills and maps) before it merges
// entity cards, so a scene full of generic stills silently loses every entity
// image to the per-scene ceiling. Reserving their slots here keeps the generic
// lane from consuming the budget the certified portraits need.
func entityImageCardReservation(result *GenerateResult, sceneID string) int {
	if result == nil || strings.TrimSpace(sceneID) == "" {
		return 0
	}
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		if !strings.EqualFold(strings.TrimSpace(scene.ID), strings.TrimSpace(sceneID)) || scene.Annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			if entity.Image == nil {
				continue
			}
			switch capmodel.EntityTypeToKind(entity.Type) {
			case capmodel.KindEntityCard, capmodel.KindOrganization, capmodel.KindLocation, capmodel.KindConcept:
			default:
				continue
			}
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				name = strings.TrimSpace(entity.Text)
			}
			if name == "" {
				continue
			}
			seen[strings.ToLower(name)] = struct{}{}
		}
	}
	return len(seen)
}

// genericSceneImageLimit returns how many generic stills one scene may offer
// without evicting its certified entity images from the shared per-scene
// overlay budget. With no image-bearing entities the certified generic limit
// is unchanged.
func genericSceneImageLimit(result *GenerateResult, sceneID string, maxOverlaysPerScene int) int {
	limit := maxGenericSceneImagesPerScene
	if maxOverlaysPerScene <= 0 {
		return limit
	}
	reserved := entityImageCardReservation(result, sceneID)
	if reserved <= 0 {
		return limit
	}
	available := maxOverlaysPerScene - reserved
	if available < 0 {
		available = 0
	}
	if available < limit {
		limit = available
	}
	return limit
}

func sceneImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, digest string) bool {
	// IndexStatus is a post-persistence search-index lifecycle signal. The
	// current run already owns verified bytes, a stable hash and a durable
	// Drive/local reference, so waiting for asynchronous INDEXED here creates
	// a scene-order race and drops otherwise renderable images.
	if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) ||
		strings.TrimSpace(candidate.AssetID) == "" || candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
		candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
		candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted ||
		len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	return strings.TrimSpace(candidate.LocalPath) != "" || strings.TrimSpace(candidate.DriveLink) != "" ||
		strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}
