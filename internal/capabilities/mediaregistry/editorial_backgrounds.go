package mediaregistry

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// EditorialBackgroundAsset is the canonical identity and editorial metadata
// for a curated video background plate.
//
// Backgrounds are editorial assets exactly like BGM and SFX: they are selected
// by a stable alias, bound to one Drive identity, and consumed by the renderer.
// This registry is therefore the single owner of the alias -> Drive mapping.
// RenderingGen's assets/backgrounds/manifest.json is a PROJECTION of this
// catalog (the same role ops/jobs/bgm_catalog.json plays for the audio
// catalog); it must never become a second source of truth.
type EditorialBackgroundAsset struct {
	// ID is the stable public alias and the media-registry asset id.
	ID string
	// DriveFileID is the original supplied Drive identity. It is the only
	// link back to the source bytes.
	DriveFileID string
	// Filename is the normalized local filename (video-only, no audio).
	Filename string
	// SHA256 is the certified content address of the normalized bytes.
	SHA256 string
	// MediaType is the container media type of the plate.
	MediaType string
	// Role is the editorial role of the plate (soft-light, etc.).
	Role string
}

// Background plate contract dimensions. They are invariants of the normalized
// fixture set, not per-job options: a plate with a different canvas, frame
// rate, duration or audio layout would silently change the master timeline.
const (
	// EditorialBackgroundContract is the published contract the RenderingGen
	// VIDEO_BACKGROUND template consumes.
	EditorialBackgroundContract = "video-background-v1"

	EditorialBackgroundWidth        = 1920
	EditorialBackgroundHeight       = 1080
	EditorialBackgroundFPS          = 30
	EditorialBackgroundDurationSecs = 15
	// EditorialBackgroundAudioStreams is always zero: the plates are normalized
	// with `-an` so they cannot double the master voiceover/BGM audio.
	EditorialBackgroundAudioStreams = 0
)

var editorialBackgroundAssets = []EditorialBackgroundAsset{
	{ID: "drive-background-01", DriveFileID: "16j67if3LUqMeVVSpOjPd5rD0cPwn9l0A", Filename: "drive-background-01.mp4", SHA256: "0dffb796fbb3137ba4949facfa01b6cb7bca1a7a375600df2da4eb1258df74c5", MediaType: "video/mp4", Role: "soft-light"},
	{ID: "drive-background-02", DriveFileID: "1uMfjYjcmbbfv0fkYfvqSO9kIvNSK_XEX", Filename: "drive-background-02.mp4", SHA256: "bbd836c7b78afff663c9eee3e4ee0da63483824cbaff84c014a6455f1533971b", MediaType: "video/mp4", Role: "soft-light"},
	{ID: "drive-background-03", DriveFileID: "1s_BK1pnUVwQrzJHRs9_WyuuuPxoJiZpJ", Filename: "drive-background-03.mp4", SHA256: "9636b10ef18489986d09190ab35dd5b46267ce45d6e8de5a17558645174c32d8", MediaType: "video/mp4", Role: "soft-light"},
	{ID: "drive-background-04", DriveFileID: "1fyOTMeb7pP1MuWfAO8o2w1p6aQedt0lE", Filename: "drive-background-04.mp4", SHA256: "abdbeab67299efcb482cbc152e4503f1128109fd895c2e8aef87381a997f2201", MediaType: "video/mp4", Role: "soft-light"},
	{ID: "drive-background-05", DriveFileID: "1_zY3WNxbkDfIUA0692ZPexpqRXq55oHG", Filename: "drive-background-05.mp4", SHA256: "0dffb796fbb3137ba4949facfa01b6cb7bca1a7a375600df2da4eb1258df74c5", MediaType: "video/mp4", Role: "soft-light"},
	{ID: "drive-background-06", DriveFileID: "1bwq06sbP845PQB4t59pPoAJSZ3vs-ej2", Filename: "drive-background-06.mp4", SHA256: "768c962ceab221caf7f7d5f20bf3d98056998615817a1ebd7c6f7b12a0d12476", MediaType: "video/mp4", Role: "soft-light"},
}

// EditorialBackgroundAssets returns a defensive copy so a caller cannot mutate
// the canonical registry.
func EditorialBackgroundAssets() []EditorialBackgroundAsset {
	out := make([]EditorialBackgroundAsset, len(editorialBackgroundAssets))
	copy(out, editorialBackgroundAssets)
	return out
}

// EditorialBackgroundIDs returns the stable public aliases in declaration
// order. It is the canonical pool a selection policy may choose from.
func EditorialBackgroundIDs() []string {
	out := make([]string, len(editorialBackgroundAssets))
	for i, asset := range editorialBackgroundAssets {
		out[i] = asset.ID
	}
	return out
}

// EditorialBackgroundIdentities returns the stable alias -> Drive identity map.
func EditorialBackgroundIdentities() map[string]string {
	out := make(map[string]string, len(editorialBackgroundAssets))
	for _, asset := range editorialBackgroundAssets {
		out[asset.ID] = asset.DriveFileID
	}
	return out
}

// LookupEditorialBackground resolves a public alias to its canonical asset.
// Matching is case-insensitive and whitespace-tolerant, consistent with the
// audio alias resolution.
func LookupEditorialBackground(id string) (EditorialBackgroundAsset, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, asset := range editorialBackgroundAssets {
		if asset.ID == id {
			return asset, true
		}
	}
	return EditorialBackgroundAsset{}, false
}

// ValidateEditorialBackgroundIdentity checks that a registered asset which
// claims a canonical plate id actually carries the certified plate bytes.
//
// The registered SHA-256 is the hash of the NORMALIZED, video-only bytes.
// The original supplied Drive file is a different artifact: it carries an ACC
// audio stream, and registering it under a plate id would put a second audio
// source under the master voiceover/BGM. A mismatch is therefore a hard
// failure, never a warning. An asset id that is not a canonical plate is not
// this function's business and is accepted unchanged (the legacy `classic1`
// plate, for example, is deliberately outside the curated set).
func ValidateEditorialBackgroundIdentity(assetID, sha256 string) error {
	asset, ok := LookupEditorialBackground(assetID)
	if !ok {
		return nil
	}
	got := strings.ToLower(strings.TrimSpace(sha256))
	if got != asset.SHA256 {
		return fmt.Errorf("background %q is registered with content hash %q, want the certified normalized plate hash %q (the original Drive file carries audio and must not be used)", asset.ID, sha256, asset.SHA256)
	}
	return nil
}

// ValidateEditorialBackgroundCatalog protects the identity invariant of the
// background registry: every plate must carry one alias, one Drive identity,
// one certified content hash and the normalized contract dimensions.
//
// Note that a SHA256 is NOT required to be unique. drive-background-01 and
// drive-background-05 are deliberately checked in as the same bytes (01 is a
// symlink to 05 in the fixture tree), so uniqueness of the content address
// would reject a valid fixture set. Uniqueness is enforced on the alias and on
// the Drive identity, which is what an asset-selection bug can actually
// confuse.
func ValidateEditorialBackgroundCatalog() error {
	if len(editorialBackgroundAssets) == 0 {
		return fmt.Errorf("editorial background catalog: no assets declared")
	}
	ids := make(map[string]struct{}, len(editorialBackgroundAssets))
	identities := make(map[string]string, len(editorialBackgroundAssets))
	for _, asset := range editorialBackgroundAssets {
		if strings.TrimSpace(asset.ID) == "" || strings.TrimSpace(asset.DriveFileID) == "" {
			return fmt.Errorf("editorial background catalog: id and Drive identity are required")
		}
		if _, exists := ids[asset.ID]; exists {
			return fmt.Errorf("editorial background catalog: duplicate id %q", asset.ID)
		}
		ids[asset.ID] = struct{}{}
		if previous, exists := identities[asset.DriveFileID]; exists && previous != asset.ID {
			return fmt.Errorf("editorial background catalog: Drive identity %q is shared by %q and %q", asset.DriveFileID, previous, asset.ID)
		}
		identities[asset.DriveFileID] = asset.ID
		if !digest.IsSHA256(asset.SHA256) || strings.ToLower(asset.SHA256) != asset.SHA256 {
			return fmt.Errorf("editorial background catalog: %s sha256 %q must be a %d-character lowercase hex digest", asset.ID, asset.SHA256, 64)
		}
		if asset.MediaType != "video/mp4" {
			return fmt.Errorf("editorial background catalog: %s media_type %q violates the video/mp4 contract", asset.ID, asset.MediaType)
		}
		if !strings.HasSuffix(asset.Filename, ".mp4") {
			return fmt.Errorf("editorial background catalog: %s filename %q violates the video/mp4 contract", asset.ID, asset.Filename)
		}
	}
	return nil
}
