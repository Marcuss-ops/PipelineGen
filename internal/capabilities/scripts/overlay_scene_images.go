package scriptgeneration

import (
	"encoding/hex"
	"os"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
			StartUS: nextStartUS, DurationUS: durationUS, Score: 1,
		})
		localHashes[digest] = struct{}{}
		if durationUS < int64(capabilityoverlay.MaxImageOverlayDurationMS*1000) || len(out) >= limit {
			return out
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
			assetID := strings.TrimSpace(candidate.AssetID)
			digest := strings.TrimSpace(candidate.ContentSHA256)
			if _, duplicate := seen[assetID]; duplicate {
				continue
			}
			seen[assetID] = struct{}{}
			if !sceneImageCandidateReady(candidate, digest) {
				continue
			}
			digest = strings.ToLower(digest)
			if _, duplicate := localHashes[digest]; duplicate {
				continue
			}
			url := strings.TrimSpace(candidate.SourceURL)
			if url == "" {
				url = strings.TrimSpace(candidate.PreviewURL)
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
				AssetID: digest, URL: url, LocalPath: strings.TrimSpace(candidate.LocalPath),
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
				return out
			}
			nextStartUS += durationUS
		}
		return out
	}
	return nil
}

// sceneImageCandidateSet is the per-scene input to the run-level image
// allocator. The allocator takes one candidate per scene in round-robin order
// before spending a second slot, so the global max_image_overlays budget
// cannot be consumed by the first scene. The old single-pass loop did exactly
// that: a four-image Floyd Mayweather run put all four stills in `record` and
// left `business` and `exhibitions` with none.
type sceneImageCandidateSet struct {
	SceneID    string
	Candidates []capabilityoverlay.ImageCandidate
}

// allocateSceneImageCandidates distributes the run-level image ceiling across
// scenes fairly. It preserves candidate order within each scene, skips hashes
// already used by entity cards or an earlier scene, and never returns more
// than ceiling images. A scene with no ready candidate contributes nothing;
// later scenes still get their turn.
func allocateSceneImageCandidates(sets []sceneImageCandidateSet, ceiling int, excluded map[string]struct{}) map[string][]capabilityoverlay.ImageCandidate {
	out := make(map[string][]capabilityoverlay.ImageCandidate, len(sets))
	if ceiling <= 0 || len(sets) == 0 {
		return out
	}
	used := make(map[string]struct{}, len(excluded))
	for hash := range excluded {
		hash = strings.ToLower(strings.TrimSpace(hash))
		if hash != "" {
			used[hash] = struct{}{}
		}
	}
	cursors := make([]int, len(sets))
	count := 0
	for count < ceiling {
		progressed := false
		for i := range sets {
			for cursors[i] < len(sets[i].Candidates) {
				candidate := sets[i].Candidates[cursors[i]]
				cursors[i]++
				hash := strings.ToLower(strings.TrimSpace(candidate.SHA256))
				if hash == "" {
					continue
				}
				if _, duplicate := used[hash]; duplicate {
					continue
				}
				used[hash] = struct{}{}
				out[sets[i].SceneID] = append(out[sets[i].SceneID], candidate)
				count++
				progressed = true
				break
			}
			if count >= ceiling {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

func sceneImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, digest string) bool {
	if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) ||
		strings.TrimSpace(candidate.AssetID) == "" || candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
		candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
		candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted ||
		!strings.EqualFold(strings.TrimSpace(candidate.IndexStatus), scriptpkg.VidRushStatusIndexed) || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	return strings.TrimSpace(candidate.LocalPath) != "" || strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}
