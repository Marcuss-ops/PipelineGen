package mediaregistry

import "fmt"

// ── The unified editorial asset registry ──────────────────────────────────
//
// Backgrounds, BGM and SFX are three editorial asset domains, but they share
// ONE identity model: a stable public alias bound to exactly one Drive
// identity, resolved through the same media registry the renderer already
// reads. Before this file the domains were validated in isolation, so nothing
// rejected an alias or a Drive identity that crossed domains. That is exactly
// the class of bug the retired `whoop*` aliases represented (one Drive
// identity addressable both as a BGM and as a one-shot effect).
//
// ValidateEditorialCatalog is the single gate over the whole registry.

// EditorialAssetIdentities returns the alias -> Drive identity map for every
// editorial asset domain (backgrounds, BGM, SFX) in one place. Consumers that
// need "which asset ids exist" must read this instead of unioning the
// per-domain accessors themselves, so a new domain is added in one place.
func EditorialAssetIdentities() map[string]string {
	out := make(map[string]string, len(editorialBackgroundAssets)+len(editorialAudioAssets))
	for alias, driveID := range EditorialBackgroundIdentities() {
		out[alias] = driveID
	}
	for alias, driveID := range EditorialAudioAliases() {
		out[alias] = driveID
	}
	return out
}

// ValidateEditorialCatalog enforces the cross-domain identity invariant on the
// canonical catalogs: one public alias and one Drive identity may belong to
// exactly one editorial asset.
func ValidateEditorialCatalog() error {
	if err := ValidateEditorialAudioCatalog(); err != nil {
		return err
	}
	if err := ValidateEditorialBackgroundCatalog(); err != nil {
		return err
	}
	return validateEditorialCatalogIdentity(editorialBackgroundAssets, editorialAudioAssets)
}

// validateEditorialCatalogIdentity is the domain-comparison core. It is split
// from ValidateEditorialCatalog so tests can feed synthetic catalogs that
// collide, which the canonical catalogs must never do.
func validateEditorialCatalogIdentity(backgrounds []EditorialBackgroundAsset, audio []EditorialAudioAsset) error {
	aliases := make(map[string]string, len(backgrounds)+len(audio))
	identities := make(map[string]string, len(backgrounds)+len(audio))
	record := func(domain, alias, driveID string) error {
		if previous, exists := aliases[alias]; exists {
			return fmt.Errorf("editorial catalog: alias %q is bound by both %q and %q", alias, previous, domain)
		}
		aliases[alias] = domain
		if previous, exists := identities[driveID]; exists && previous != domain {
			return fmt.Errorf("editorial catalog: Drive identity %q is shared across %q and %q", driveID, previous, domain)
		}
		identities[driveID] = domain
		return nil
	}
	for _, asset := range backgrounds {
		if err := record("backgrounds", asset.ID, asset.DriveFileID); err != nil {
			return err
		}
	}
	for _, asset := range audio {
		if err := record("audio", asset.Alias, asset.DriveFileID); err != nil {
			return err
		}
	}
	return nil
}
