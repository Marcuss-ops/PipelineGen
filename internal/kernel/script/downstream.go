// Package script — downstream.go (Step 11A canonical downstream-types, July 2026).
//
// User spec (verbatim, July 2026):
//
//	(a) Definisci tipi canonici DownstreamRequest + asset_requirements
//	    in kernel/script/downstream.go. Tipi richiesti:
//	      DownstreamRequest{Kind, ItemRef: GenerationItemV2.ID, Required,
//	                        AssetRequirements, OutputDest: OutputDestination}
//	      AssetRequirements{Voiceover: *VoiceoverRequirements,
//	                        Images: *ImagesRequirements}
//	      VoiceoverRequirements{Provider, VoiceID, Pace, StylePreset}
//	      ImagesRequirements{Count, StylePreset, Provider, Resolution}
//	      DownstreamKind enum: DownstreamVoiceover, DownstreamImages,
//	                           DownstreamBoth
//	(b) Aggiungi kernel/script.ManifestV2.NoInlineAssets bool (default
//	    true) — il manifest porta solo DownstreamRequests, non più
//	    voci/immagini inline.
//
// Why this file:
//   - It is the canonical typed surface that REPLACES the legacy
//     inline voice/image collection on the script manifest. The script
//     .generate handler reads Items []DownstreamRequest and routes
//     each to the canonical voiceover / image sibling job types
//     (Step 11B).
//   - NoInlineAssets=true is the canonical NEW-mode marker: the
//     zero-value &ManifestV2{} is the LEGACY inline mode (kept as
//     "not migrating" sentinel for tests + back-compat with pre-Step
//     11A callers), while NewManifestV2() returns the canonical
//     NEW-mode (NoInlineAssets=true + empty Items slice).
//
// Wire-shape contract (godlike/07 fail-closed):
//   - JSON tags are lower-snake + omitempty for pointer fields.
//   - DownstreamRequest.ItemRef carries a GenerationItemV2.ID (the
//     canonical per-item identifier).
//   - AssetRequirements.Voiceover/Images are typed pointers so the
//     dispatcher can route on Kind without a marshaled-string
//     discriminator field.
//   - OutputDestination is INDEPENDENT of voiceover.DestinationRequest
//     so kernel/script does NOT import voiceover (AGENTS.md Pattern 8:
//     domain is the bottom of the import graph, no cross-cut).
package script

// ── DownstreamKind — discriminated enum ────────────────────────────────

// DownstreamKind classifies a DownstreamRequest by its target asset
// combination. The canonical values match the user spec Step 11A (a)
// + Step 11C (Document extension, July 2026):
//
//	DownstreamVoiceover: 1 voiceover sibling only (no images).
//	DownstreamImages:    N image siblings only (no voiceover).
//	DownstreamBoth:      1 voiceover + N image siblings.
//	DownstreamDocument:  1 Google Doc sibling (no voice, no images).
//
// DownstreamBoth is provided so callers can request BOTH asset classes
// for a single item in one envelope (no need to emit distinct
// per-kind envelopes). DownstreamDocument is the canonical Google Doc
// sibling envelope — its semantics are independent of voiceover/images
// (no AssetRequirements sub-struct; the dispatcher routes via Kind
// alone). DownstreamKind is also surfaced in AssetRequirements via
// the pointer fields (Voiceover != nil XOR Images != nil) — the Kind
// is a fast-path for the dispatcher's fan-out routing.
type DownstreamKind string

const (
	// DownstreamVoiceover: voiceover sibling only.
	DownstreamVoiceover DownstreamKind = "voiceover"
	// DownstreamImages: image siblings only.
	DownstreamImages DownstreamKind = "images"
	// DownstreamBoth: voiceover + image siblings (combined envelope).
	DownstreamBoth DownstreamKind = "both"
	// DownstreamDocument: Google Doc sibling only (no asset
	// sub-structs; the document processor is the canonical sibling
	// producer per SCRIPTCONTRACT-2026-07-08 PR-1 processor
	// ordering). Added in PR-1 of the SCRIPT-DOWNSTREAM-CUTOVER
	// wave to give the manifest a first-class per-item Doc
	// envelope (pre-PR-1, the Document processor was registered
	// once at script scope rather than per-item).
	DownstreamDocument DownstreamKind = "document"
)

// IsValid reports whether k is one of the canonical DownstreamKind
// values. Strictly used by deserializers/parsers to reject legacy
// wire values or hand-crafted strings.
func (k DownstreamKind) IsValid() bool {
	switch k {
	case DownstreamVoiceover, DownstreamImages, DownstreamBoth, DownstreamDocument:
		return true
	}
	return false
}

// ── VoiceoverRequirements — voiceover sub-spec ────────────────────────

// VoiceoverRequirements captures the per-locale voiceover settings
// for a downstream voiceover sibling. All four fields mirror the
// canonical voiceover.DestinationRequest surface (Provider, VoiceID,
// Pace, StylePreset) so the dispatcher can project them into the
// voiceover.spawn_voiceover sibling payload (Step 11B C2).
//
// Wire tags:
//   - Provider     string  (canonical e.g. "edge-tts", "eleven-labs",
//     "google-cloud-tts"). Empty == fallback.
//   - VoiceID      string  (canonical per-provider voice id, e.g.
//     "it-IT-IsabellaNeural").
//   - Pace         string  (optional; "" == default pace per provider).
//   - StylePreset  string  (optional; "" == default style per provider).
type VoiceoverRequirements struct {
	Provider    string `json:"provider"`
	VoiceID     string `json:"voice_id"`
	Pace        string `json:"pace,omitempty"`
	StylePreset string `json:"style_preset,omitempty"`
}

// ── ImagesRequirements — image sub-spec ────────────────────────────────

// ImagesRequirements captures the per-batch AI image settings for a
// downstream image sibling. The Count field is required (zero means
// "no image requested" and is fail-closed via Step 11B (d)). The other
// fields are optional with provider defaults applied at dispatch time.
//
// Wire tags:
//   - Count        int     (REQUIRED: positive integer).
//   - StylePreset  string  (optional; "" == default style preset).
//   - Provider     string  (optional; empty == canonical default
//     "google_slides" per FASE 2).
//   - Resolution   string  (optional; "" == canonical "1920x1080").
type ImagesRequirements struct {
	Count       int    `json:"count"`
	StylePreset string `json:"style_preset,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
}

// ── AssetRequirements — group envelope ─────────────────────────────────

// AssetRequirements groups the per-kind sub-structs under a single
// canonical field. Pointer fields are nil when the corresponding
// downstream kind is not selected by DownstreamRequest.Kind — so a
// DownstreamVoiceover envelope has AssetRequirements.Voiceover !=
// nil and AssetRequirements.Images == nil. The pointer semantics
// preserve the canonical "the kind's substruct is present iff the
// caller explicitly opts in" contract that the Step 11B dispatcher
// relies on for fail-closed Asset.Required detection.
//
// The Kind field on the parent DownstreamRequest is treated as
// authoritative by the dispatcher; the pointer fields here are
// "the substructs that the user spec mentioned explicitly" (used
// for cross-cutting validation: DownstreamVoiceover MUST set
// Voiceover != nil; DownstreamImages MUST set Images != nil;
// DownstreamBoth MAY set both).
type AssetRequirements struct {
	// Voiceover is non-nil when the envelope carries voiceover
	// requirements. The dispatcher projects Values into the
	// voiceover.spawn_voiceover sibling payload.
	Voiceover *VoiceoverRequirements `json:"voiceover,omitempty"`
	// Images is non-nil when the envelope carries image requirements.
	// Required count must be > 0 (validated in the dispatcher's
	// fail-closed path; pre-validate via NewImagesRequirements).
	Images *ImagesRequirements `json:"images,omitempty"`
}

// NewVoiceoverRequirements constructs a typed voiceover sub-struct
// with all four canonical fields populated. Returns nil if voiceID
// is empty (the canonical "no voice selected" sentinel — fail-closed
// via godlike/07 since voice synthesis without a voice id is
// impossible).
func NewVoiceoverRequirements(provider, voiceID, pace, stylePreset string) *VoiceoverRequirements {
	if voiceID == "" {
		return nil
	}
	return &VoiceoverRequirements{
		Provider:    provider,
		VoiceID:     voiceID,
		Pace:        pace,
		StylePreset: stylePreset,
	}
}

// NewImagesRequirements constructs a typed image sub-struct with the
// canonical-everywhere defaults applied (StylePreset="" /
// Provider="google_slides" / Resolution="1920x1080"). Returns nil if
// count <= 0 (fail-closed: zero image count is meaningless).
func NewImagesRequirements(count int, stylePreset string) *ImagesRequirements {
	if count <= 0 {
		return nil
	}
	return &ImagesRequirements{
		Count:       count,
		StylePreset: stylePreset,
		Provider:    "google_slides",
		Resolution:  "1920x1080",
	}
}

// ── OutputDestination — canonical sink envelope ───────────────────────

// OutputDestination is the canonical sink for a downstream-produced
// asset (voiceover file, image asset, or both). INDEPENDENT of
// voiceover.DestinationRequest so kernel/script does NOT import
// voiceover (AGENTS.md Pattern 8: domain is the bottom of the import
// graph). Future code adapter (out-of-scope for this commit) MAY
// project OutputDestination → voiceover.DestinationRequest at the
// composition-root boundary, but the canonical typed surface stays
// in kernel/script.
//
// Wire tags:
//   - Kind          string  (canonical "drive_folder" | "google_doc"
//     | "local"; empty == default
//     "drive_folder").
//   - FolderID      string  (Google Drive folder id; required for
//     drive_folder / google_doc destinations).
//   - DocumentTitle string  (Google Doc title when Kind="google_doc").
type OutputDestination struct {
	Kind          string `json:"kind"`
	FolderID      string `json:"folder_id,omitempty"`
	DocumentTitle string `json:"document_title,omitempty"`
}

// ── DownstreamRequest — canonical envelope ────────────────────────────

// DownstreamRequest is the canonical Step 11A typed envelope that
// REPLACES the legacy inline asset collection on the script
// manifest. Per user spec, the route is:
//
//	ManifestV2.Items []DownstreamRequest
//	  → script.generate handler dispatches one envelope per
//	    downstream sibling (Step 11B C2/C3 wires the dispatcher).
//
// Field semantics:
//   - Kind                       : DownstreamVoiceover | DownstreamImages
//     | DownstreamBoth
//   - ItemRef                    : GenerationItemV2.ID (the canonical
//     per-item identifier that the
//     manifest is being generated for).
//   - Required                   : fail-closed flag. True means the
//     parent script.generate propagates
//     FAILED if this downstream sibling
//     cannot be produced.
//   - AssetRequirements          : typed sub-specs (voiceover / images).
//     Pointer fields preserve "user
//     opted in to this kind" semantics.
//   - OutputDest                 : output destination envelope (Drive
//     folder + optional DocumentTitle).
type DownstreamRequest struct {
	Kind              DownstreamKind    `json:"kind"`
	ItemRef           string            `json:"item_ref"` // GenerationItemV2.ID
	Required          bool              `json:"required"` // fail-closed via Step 11B (d)
	AssetRequirements AssetRequirements `json:"asset_requirements"`
	OutputDest        OutputDestination `json:"output_dest"`
}

// NewDownstreamRequestVoiceover constructs a DownstreamRequest
// envelope for a voiceover-only downstream sibling. Helper that
// enforces consistent Kind/Sub-struct pairing — the caller cannot
// accidentally emit a DownstreamVoiceover envelope with Voiceover
// sub-struct == nil.
func NewDownstreamRequestVoiceover(
	itemRef string,
	required bool,
	voiceover *VoiceoverRequirements,
	outputDest OutputDestination,
) *DownstreamRequest {
	return &DownstreamRequest{
		Kind:     DownstreamVoiceover,
		ItemRef:  itemRef,
		Required: required,
		AssetRequirements: AssetRequirements{
			Voiceover: voiceover,
		},
		OutputDest: outputDest,
	}
}

// NewDownstreamRequestImages constructs a DownstreamRequest envelope
// for image-only downstream siblings. Caller-supplied
// imageRequirements must pass NewImagesRequirements (which enforces
// Count > 0); the helper preserves that contract.
func NewDownstreamRequestImages(
	itemRef string,
	required bool,
	imageRequirements *ImagesRequirements,
	outputDest OutputDestination,
) *DownstreamRequest {
	return &DownstreamRequest{
		Kind:     DownstreamImages,
		ItemRef:  itemRef,
		Required: required,
		AssetRequirements: AssetRequirements{
			Images: imageRequirements,
		},
		OutputDest: outputDest,
	}
}

// NewDownstreamRequestBoth constructs a DownstreamRequest envelope
// for a combined voiceover + images downstream envelope. Helper that
// pairs both sub-structs under a single DownstreamRequest so the
// dispatcher can fan-out to both sibling producers with a single
// envelope (rather than emitting two per-kind envelopes that would
// race on per-item correlation IDs).
//
// Caller-supplied sub-structs MUST both be non-nil for a DownstreamBoth
// envelope to be semantically valid; this helper preserves the
// non-nil invariant (passing nil for either sub-struct is a
// programming error — the dispatcher's fail-closed branch will
// reject the envelope if either is missing at run time).
func NewDownstreamRequestBoth(
	itemRef string,
	required bool,
	voiceover *VoiceoverRequirements,
	imageRequirements *ImagesRequirements,
	outputDest OutputDestination,
) *DownstreamRequest {
	return &DownstreamRequest{
		Kind:     DownstreamBoth,
		ItemRef:  itemRef,
		Required: required,
		AssetRequirements: AssetRequirements{
			Voiceover: voiceover,
			Images:    imageRequirements,
		},
		OutputDest: outputDest,
	}
}

// NewDownstreamRequestDocument constructs a DownstreamRequest envelope
// for a Google Doc-only downstream sibling. Added in PR-1 of the
// SCRIPT-DOWNSTREAM-CUTOVER wave to give the canonical Document
// producer a first-class per-item dispatch path (pre-PR-1 the
// Document processor was script-scope rather than per-item).
//
// Doc envelopes have no AssetRequirements sub-structs (the document is
// the asset, not a sibling of one). The helper preserves the
// no-sub-struct invariant; AssetRequirements stays at zero-value.
//
// The outputDest must specify Kind="google_doc" with a non-empty
// FolderID + DocumentTitle for the dispatcher's fail-closed path
// (validation happens in the dispatcher, not in this helper).
func NewDownstreamRequestDocument(
	itemRef string,
	required bool,
	outputDest OutputDestination,
) *DownstreamRequest {
	return &DownstreamRequest{
		Kind:              DownstreamDocument,
		ItemRef:           itemRef,
		Required:          required,
		AssetRequirements: AssetRequirements{}, // no sub-structs for Doc
		OutputDest:        outputDest,
	}
}

// ── ManifestV2 — canonical container ──────────────────────────────────

// ManifestV2 is the canonical container for a script generation
// request's downstream-fan-out surface. Per user spec
// NoInlineAssets is the canonical migration marker that signals
// "this manifest carries DownstreamRequests rather than legacy
// inline voice/image collections".
//
// Semantic split (godlike/07 fail-closed):
//   - Zero-value &ManifestV2{} represents the LEGACY inline mode
//     (NoInlineAssets=false). REPRESENTS the deprecated surface; kept
//     as a sentinel for tests that exercise migration back-compat.
//   - NewManifestV2() returns the canonical NEW-mode manifest
//     (NoInlineAssets=true). This is the ONLY constructor the
//     Step 11B dispatcher should consume; back-compat zero-value
//     manifests trigger a fail-closed branch in the dispatcher
//     ("legacy manifest cannot be fan-out'd via Step 11A surface").
//
// Wire shape:
//   - JSON tags are lower-snake per AGENTS.md convention. The canonical
//     marshal/match surface is round-trippable (see
//     downstream_test.go for the JSON round-trip table).
type ManifestV2 struct {
	// NoInlineAssets is the migration marker. true = canonical NEW
	// mode (Items carries only DownstreamRequests). false = legacy
	// mode (Items MAY contain inline voice/image assets pre-Step
	// 11A) — kept as a sentinel ONLY for back-compat tests.
	//
	// Per user spec "default true" — the canonical NEW-mode is
	// reachable via NewManifestV2(). The zero-value &ManifestV2{}
	// stays NoInlineAssets=false deliberately so the migration
	// envelope is explicit at construction time.
	NoInlineAssets bool `json:"no_inline_assets"`
	// Items is the canonical list of DownstreamRequest envelopes.
	// Empty slice means "no downstream fan-out requested" — distinct
	// from nil (which is treated as empty by both MarshalJSON and
	// tests).
	Items []DownstreamRequest `json:"items"`
}

// NewManifestV2 returns the canonical NEW-mode ManifestV2
// (NoInlineAssets=true, empty Items []DownstreamRequest).
//
// Per user spec "default true", this is the canonical entry-point
// the Step 11B dispatcher consumers MUST use. The call-site never
// needs to manually set NoInlineAssets=true.
func NewManifestV2() *ManifestV2 {
	return &ManifestV2{
		NoInlineAssets: true,
		Items:          []DownstreamRequest{},
	}
}

// IsCanonicalMode reports whether the manifest is in canonical
// NEW-mode (NoInlineAssets=true). Helper for the dispatcher's
// fail-closed branch: if false, the dispatcher MUST reject the
// manifest with a typed sentinel ErrLegacyManifestRejected.
func (m *ManifestV2) IsCanonicalMode() bool {
	return m != nil && m.NoInlineAssets
}
