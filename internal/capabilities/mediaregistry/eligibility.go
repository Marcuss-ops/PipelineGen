// Package mediaregistry — eligibility.go: canonical index eligibility policy.
//
// "Registered in the Media Registry" is NOT the same as "must be projected
// into the semantic index". This file owns that distinction: every real asset
// is REGISTERED, but only a subset is SEARCHABLE (and therefore embedded and
// projected into Qdrant). The decision is taxonomy-derived (media_type +
// asset_kind), never name-prefix or filename heuristic.
//
// godlike/06 SSOT: IndexEligibility + IndexEligibility() are the single owner
// of the searchability policy. The MediaIndexer consults this before doing any
// embedding work.
package mediaregistry

import (
)

// IndexEligibility is the searchability decision for a registered asset.
type IndexEligibility string

const (
	// IndexEligibilitySearchable means the asset must be embedded and
	// projected into the semantic index (Qdrant).
	IndexEligibilitySearchable IndexEligibility = "SEARCHABLE"

	// IndexEligibilityRegistered means the asset is a real registry asset but
	// is NOT semantic-searchable (e.g. voiceover, final_audio). It stays
	// REGISTERED; the MediaIndexer skips embedding/projection for it.
	IndexEligibilityRegistered IndexEligibility = "REGISTERED"
)

// IndexEligibility returns the canonical default searchability for a taxonomy.
// Video and image kinds are SEARCHABLE; audio (voiceover, bgm, sfx, clip_audio,
// final_audio), text and document kinds are REGISTERED only.
func (t AssetTaxonomy) IndexEligibility() IndexEligibility {
	switch t.MediaType {
	case MediaVideo, MediaImage:
		return IndexEligibilitySearchable
	default:
		return IndexEligibilityRegistered
	}
}
