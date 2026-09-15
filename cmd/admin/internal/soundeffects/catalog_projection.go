package soundeffects

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"

// canonicalProvidedSoundEffects builds the admin indexing input from the
// shared editorial registry first, then keeps the larger historical supplied
// catalog as a compatibility projection. Duplicate Drive identities are
// dropped, so BGM/whop assets are indexed exactly once with canonical metadata.
func canonicalProvidedSoundEffects(legacy []providedSoundEffect) []providedSoundEffect {
	canonical := mediaregistry.EditorialAudioAssets()
	out := make([]providedSoundEffect, 0, len(canonical)+len(legacy))
	seen := make(map[string]struct{}, len(canonical)+len(legacy))
	for _, asset := range canonical {
		out = append(out, providedSoundEffect{
			driveID: asset.DriveFileID, filename: asset.Filename, name: asset.Name,
			family: asset.Family, subtype: asset.Subtype, mood: asset.Mood,
			energy: asset.Energy, bestFor: append([]string(nil), asset.BestFor...),
			tags: append([]string(nil), asset.Tags...),
		})
		seen[asset.DriveFileID] = struct{}{}
	}
	for _, asset := range legacy {
		if _, exists := seen[asset.driveID]; exists {
			continue
		}
		out = append(out, asset)
		seen[asset.driveID] = struct{}{}
	}
	return out
}
