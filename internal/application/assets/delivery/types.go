// Package delivery defines the canonical Drive publish contract.
//
// Every endpoint and every job that uploads a file to Google Drive MUST
// go through the Publisher interface (defined in a follow-up file in this
// package). The types below describe WHAT a caller wants to publish —
// never WHERE on Drive it should land. The DestinationRegistry (also in
// this package) maps a DestinationKey to a root folder and path-builder;
// the concrete Publisher in internal/infrastructure/drive/publisher.go
// resolves the full Drive folder hierarchy and performs the upload.
//
// Architecture rule (June 2026):
//
//	An endpoint or job never chooses a folder ID and never builds a
//	Drive folder hierarchy. It declares only a DestinationKey and the
//	asset's logical metadata. The DestinationRegistry is the sole
//	authority for root and structure; the Publisher is the sole
//	authority for folder creation and file upload.
//
// P0.6 (July 2026): IdempotencyKey replaces folderID+filename as the
// authoritative identity for Drive file conflict detection. The key is
// derived from SHA-256(destination:artifactID:contentHash:sourceVersion)
// and stored as a Drive appProperty for cross-session recovery.
// Filename is retained as a display property only.
package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// DestinationKey identifies a canonical Drive destination. Each key maps
// to exactly one DestinationPolicy in the DestinationRegistry. Adding a
// new capability means adding one constant here and one policy entry —
// no endpoint-level Drive logic is permitted.
type DestinationKey string

const (
	DestinationYouTubeClip DestinationKey = "youtube_clip"
	// DestinationYouTubeAsset (PR-CLIPINGEST-PIPELINE step 9, July 2026):
	// canonical destination for the new YouTube asset layout per user
	// spec — `PipelineGen Assets/youtube/{channel_id}/{video_id}/clips/
	// {asset_id}/{asset_id}__master.mp4 + __preview.mp4 + __manifest.json`.
	// Distinct from DestinationYouTubeClip (the legacy
	// `clips/{group}/{video_id}/` scheme) so the cutover is additive:
	// new YouTube asset writes route through this key; legacy
	// YouTubeClip writes continue unchanged. godlike/06 SSOT: the
	// two destinations share the same root folder (cfg.Drive.ClipsFolder)
	// but emit different PathBuilder segments. The folder-namespace
	// wrap (`youtube/`) is added by the registry via maybeWrapNamespace
	// so the new scheme lives under `<ClipsRootFolder>/youtube/...`
	// rather than spilling into the legacy `clips/...` root.
	DestinationYouTubeAsset DestinationKey = "youtube_asset"
	DestinationArtlist      DestinationKey = "artlist"
	DestinationStock        DestinationKey = "stock"
	DestinationImage        DestinationKey = "image"
	DestinationVoiceover    DestinationKey = "voiceover"
	DestinationBook         DestinationKey = "book"
	DestinationScript       DestinationKey = "script"
	DestinationSoundEffect  DestinationKey = "sound_effect"
	// DestinationSoundEffectSidecar (PR-P12-SOUND-EFFECT-SIDECAR, July 2026):
	// canonical destination for sound-effect metadata.json sidecars
	// (tags, search_text, semantic metadata) co-located with the
	// audio file in the same <root>/<name>/ folder. Distinct from
	// DestinationSoundEffect (audio) so the sidecar can carry its
	// own conflict policy (ConflictOverwrite — the latest
	// metadata.json wins) without conflating with the audio's
	// immutable ConflictSkip semantics. godlike/06 SSOT: one
	// canonical owner per fact; the sidecar's overwrite policy is
	// a separate concern from the audio's skip policy.
	DestinationSoundEffectSidecar DestinationKey = "sound_effect_sidecar"
	DestinationDocument           DestinationKey = "document"
	// DestinationClipMetadata (P0-#1 atomic-RMW cutover, July 2026):
	// canonical destination for the per-folder metadata.json sidecar
	// that backs UpdateCumulativeMetadataJSON. The sidecar lives in
	// the clip's already-resolved folder (no path-builder nesting —
	// the caller threads folderID via DestinationFolderID), and the
	// ConflictPolicy is ConflictOverwrite because the sidecar is a
	// regenerable ledger (the latest merged entries win, and a
	// pre-F2.9 P0-#1 bug was trashing the old sidecar before the new
	// one was published — this destination plus the new
	// read-modify-write flow via delivery.Publisher.Publish closes
	// that hole). godlike/06 SSOT: one canonical owner per fact
	// (the sidecar's overwrite policy is a separate concern from
	// the clip's immutable skip policy).
	DestinationClipMetadata DestinationKey = "clip_metadata"
	// DestinationRenderedClip (P0 pub-outbox, August 2026): canonical
	// destination for clip.render output (the final rendered MP4).
	// Distinct from DestinationClipMetadata (which is for per-folder
	// metadata.json sidecars): the rendered clip is an immutable asset
	// with ConflictSkip, while the metadata sidecar uses ConflictOverwrite.
	DestinationRenderedClip DestinationKey = "rendered_clip"
	DestinationAdmin        DestinationKey = "admin"
)

// ConflictPolicy controls what happens when a file with the same name
// already exists in the target Drive folder.
//
// P1.1 (July 2026) — type contract: ConflictPolicyUnset is the iota
// zero value, intentionally distinct from any "real" policy. This
// lets the publisher distinguish "caller did not pick a policy" from
// "caller explicitly chose ConflictOverwrite" — pre-P1.1 the two were
// indistinguishable (zero was silently mapped to ConflictOverwrite)
// which produced the unsafe silent-overwrite footgun the registry-
// driven default is meant to eliminate.
//
// A typed value of ConflictPolicyUnset MUST be treated by the
// publisher as "look up the registry default" and never forwarded
// to the uploader without resolution. Callers that explicitly want
// ConflictOverwrite must pass the named constant; the struct's zero
// value is "unset", not "overwrite".
type ConflictPolicy int

const (
	// ConflictPolicyUnset is the iota-zero sentinel. The publisher
	// MUST treat this as "caller didn't pick — consult the registry"
	// and resolve to DestinationPolicy.ConflictPolicy before
	// forwarding. Forwarding Unset verbatim to the uploader would
	// silently fall back to legacy overwriting behaviour.
	ConflictPolicyUnset ConflictPolicy = iota

	// ConflictOverwrite updates the existing file in place. Distinct
	// from ConflictPolicyUnset: an explicit choice, not a hidden
	// zero-default. (P1.1, July 2026.)
	ConflictOverwrite

	// ConflictSkip returns the existing file's metadata without uploading.
	ConflictSkip

	// ConflictRename appends a timestamp or suffix to avoid collision.
	ConflictRename
)

// PublishRequest describes WHAT to publish, not WHERE it lands on Drive.
// The caller provides only the destination kind and the asset's logical
// metadata. The Publisher resolves the actual folder path.
type PublishRequest struct {
	// Destination is the canonical Drive destination key.
	Destination DestinationKey `json:"destination"`

	// LocalPath is the absolute path to the file on the local filesystem.
	LocalPath string `json:"local_path"`

	// Filename is the desired name on Drive (e.g. "clip_abc123.mp4").
	Filename string `json:"filename"`

	// Description is an optional description visible in the Drive UI.
	Description string `json:"description,omitempty"`

	// AssetID is the canonical asset identifier (e.g. media_assets.id).
	AssetID string `json:"asset_id,omitempty"`

	// ProjectID groups related assets (e.g. a book processing run).
	ProjectID string `json:"project_id,omitempty"`

	// ChannelID (PR-CLIPINGEST-PIPELINE step 9, July 2026) is the
	// canonical YouTube channel_id for the new YouTube asset layout.
	// Used by YouTubeAssetPath as the second path segment (after the
	// `youtube/` namespace): `youtube/{channel_id}/{video_id}/clips/
	// {asset_id}/...`. Distinct from the legacy Group field (which
	// is the operator-curated human-readable alias — Boxe, HipHop —
	// the YouTubeClipPath builder consumes). The two coexist:
	// existing Group-based YouTubeClip writes are unaffected;
	// new YouTubeAsset writes thread the canonical YouTube
	// channel_id via this field. Empty ChannelID on a
	// DestinationYouTubeAsset request is fail-closed (the mapper
	// returns ErrAssetPublishLocationIncompleteForDestination with
	// "missing channel_id").
	ChannelID string `json:"channel_id,omitempty"`

	// SizeBytes (PR-CLIPINGEST-PIPELINE step 9, July 2026) is the
	// pre-computed local-file size for the post-upload size-match
	// verification gate (Commit 3 of verifier.go). When non-zero,
	// the publisher threads this into VerificationParams.ExpectedSize
	// and the verifier rejects uploads whose Drive-side size does
	// not match. Zero = skip the size check (back-compat for
	// callers that don't pre-compute size).
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// Group is a logical grouping key (e.g. YouTube channel name,
	// artlist search term). Used by PathBuilder to construct the
	// folder hierarchy.
	Group string `json:"group,omitempty"`

	// Subject identifies the subject within the group (e.g. YouTube
	// video ID, artlist asset ID). Used by PathBuilder.
	Subject string `json:"subject,omitempty"`

	// Style is an optional style tag (e.g. image generation style).
	Style string `json:"style,omitempty"`

	// Language is an optional BCP-47 language tag.
	Language string `json:"language,omitempty"`

	// Provider (SEMANTIC-LOCATION-API-2026-07-06 Wave 1) categorises
	// the upstream source (e.g. "pexels", "pixabay", "wikipedia").
	// Optional at PublishRequest level; required by StockPath in Wave 4
	// stock migration. Carries to Qdrant payload in Wave 10.
	Provider string `json:"provider,omitempty"`

	// Category (SEMANTIC-LOCATION-API-2026-07-06 Wave 1) groups assets
	// under a logical taxonomy bucket (e.g. "Boxe", "Personaggi").
	// Optional at PublishRequest level; required by StockPath / Stock
	// surface in Wave 4 stock migration. Carries to Qdrant payload in
	// Wave 10.
	Category string `json:"category,omitempty"`

	// Tags are semantic keywords for the Qdrant payload (DoD #3,
	// July 2026). Carried downstream to asset_published events for
	// hybrid BM25 lexical search. Optional; empty Tags slice means
	// "no keywords assigned yet" — distinct from nil (unset).
	//
	// godlike/06 SSOT: the canonical owner of Tags is the
	// AssetPublishInput.Tags field propagated through BuildPublishRequest.
	// Downstream consumers (Publisher, outbox handler) MUST NOT
	// re-derive Tags from other PublishRequest fields — callers
	// populate Tags once at the publish seam.
	Tags []string `json:"tags,omitempty"`

	// ConflictPolicy controls duplicate-file behaviour for this call.
	//
	// Semantics (P1.1, July 2026):
	//   - ConflictPolicyUnset (zero value of the enum, the iota
	//     sentinel) means "caller didn't pick a policy"; the publisher
	//     consults DestinationRegistry's per-destination default (see
	//     DestinationPolicy.ConflictPolicy). Per-key mapping: immutable
	//     assets (YouTube clip, Artlist, Stock, Image, Voiceover,
	//     SoundEffect) default to ConflictSkip; regenerable outputs
	//     (Book, Script, Document) default to ConflictOverwrite.
	//   - Explicit values (ConflictOverwrite / ConflictSkip /
	//     ConflictRename) are honoured verbatim — the registry
	//     default is bypassed.
	//
	// Pre-P1.1, the iota-first const was ConflictOverwrite => the
	// zero value of uninitialised PublishRequest.ConflictPolicy was
	// indistinguishable from "caller explicitly wants Overwrite",
	// which produced the silent-overwrite footgun for callers that
	// forgot the field. Reordering the iota to put
	// ConflictPolicyUnset first closes that gap: the zero value is
	// now a typed sentinel that triggers registry-default lookup,
	// and an explicit Overwrite must use the named constant.
	ConflictPolicy ConflictPolicy `json:"conflict_policy,omitempty"`

	// DestinationFolderID is the explicit Drive folder ID where the
	// publisher should place the file. It is used by sidecar flows
	// (e.g. DestinationClipMetadata) where the caller already knows
	// the resolved folder and the destination has no fixed root.
	//
	// When non-empty, it takes precedence over the registry's
	// RootFolderID for the destination. When empty, the publisher
	// falls back to ParentFolderID (legacy admin escape hatch)
	// and then to the registry root.
	DestinationFolderID string `json:"destination_folder_id,omitempty"`
	// DestinationSubpath is an optional child path below an already
	// resolved DestinationFolderID. It is used for deterministic sidecar
	// folders such as "Ass Sub"; it never changes the destination root.
	DestinationSubpath []string `json:"destination_subpath,omitempty"`

	// ParentFolderID is the semantic parent folder used when resolving
	// a destination hierarchy. It is not a direct upload target: callers
	// that already resolved the leaf MUST use DestinationFolderID.
	ParentFolderID string `json:"parent_folder_id,omitempty"`

	// IdempotencyKey (P0.6, July 2026) is the deterministic identity
	// key for this publish. Derived from SHA-256(destination:artifactID:
	// contentHash:sourceVersion). When non-empty, the Publisher uses
	// it for conflict detection via Drive appProperties instead of
	// folderID+filename lookup. Empry = fallback to filename-based
	// lookup (backward compat).
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// ContentHash (P0.6) is the hex-encoded SHA-256 digest of the
	// artifact content. Used to derive IdempotencyKey.
	ContentHash string `json:"content_hash,omitempty"`

	// SourceVersion (P0.6) is the logical source version at publish
	// time. Used to derive IdempotencyKey.
	SourceVersion int64 `json:"source_version,omitempty"`
}

// DeriveIdempotencyKey (P0.6, July 2026) computes the deterministic
// identity key for a publish operation:
//
//	SHA-256(destination:artifactID:contentHash:sourceVersion)
//
// The colon-delimited input string is hashed via SHA-256 and returned
// as a 64-char hex string. Same inputs → same key across retries and
// cross-session recovery.
func DeriveIdempotencyKey(destination DestinationKey, artifactID, contentHash string, sourceVersion int64) string {
	input := string(destination) + ":" + artifactID + ":" + contentHash + ":" + strconv.FormatInt(sourceVersion, 10)
	h := sha256.New()
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// ── P2.3 (July 2026) — PublishAction consolidation: UploadOutcome canonical rename ──
//
// godlike/06 §"one owner per fact" motivation. Pre-P2.3 the cross-package
// mapping between drive.PutAction (4 values, used by the uploader seam) and
// delivery.PublishAction (4 values + zero-marker Unknown) was implicit
// through Publisher.actionFor; the ambiguous "PublishAction" naming
// (publish is a verb, but the type is the outcome enum) created a
// maintenance hazard: every new wire consumer had to learn the legacy
// verb-type semantic before branching.
//
// The canonical rename UploadOutcome (a noun, matching the verb base
// "upload") replaces PublishAction as the SSOT. The legacy identifier
// remains available via Go-level type alias (`type PublishAction =
// UploadOutcome`) + back-compat constant aliases so pre-P2.3 call sites
// compile unchanged and post-P2.3 new code prefers UploadOutcome per
// godlike/06 SSOT. The 4 arms (Created / Updated / Skipped / Renamed) +
// the zero-value sentinel (UploadOutcomeUnknown = "") are preserved
// 1:1; no semantic shift, only a rename + back-compat surface.
//
// godlike/07 honest-limitation: the back-compat surface carries the
// `,back-compat 2026-Q4` deprecation hint in the docstring above the
// alias declaration; future agents reading the file cold see the
// migration intent. The CONTRACT removal of the alias is filed in
// architecture/current.yaml#PR-P2.3-CLOSURE for ForwardPointer at the
// Wave 14 mega-package split gate; current P2.5 closure locks the
// canonical surface + tests + alias.

// UploadOutcome is the canonical typed-string outcome enum for the Drive
// publisher. It mirrors the legacy PublishAction 1:1; the canonical
// surface post-P2.3 is UploadOutcome, and PublishAction is preserved
// as a Go-level type alias for back-compat (see below). It mirrors
// delivery.ConflictPolicy (declared further up in this file) by
// exposing the concrete outcome of the publish so callers can branch
// on it: route a "skipped" asset to the dedupe ledger, convert a
// "renamed" asset into a sibling row, treat "updated" as a no-op for
// downstream events, and surface "created" as the canonical
// fresh-asset path.
//
// Zero value is "" to keep zero-value PublishResult (e.g. stub fakes
// in tests, or legacy sites that still construct the value manually
// before downstream consumers move to the canonical Publisher)
// indistinguishable from an actual unknown action — any consumer that
// branches on Action MUST default the empty branch to a conservative
// no-op rather than treat it as "created".
type UploadOutcome string

const (
	// UploadOutcomeUnknown is the typed zero value. It signals that
	// the publisher could not determine the outcome — usually because
	// a pre-P0-#1 adapter (no PutFile on the FileUploaderPort)
	// produced a PublishResult. Post-P0-#9 callers that branch on
	// Action MUST default the empty branch to a conservative no-op
	// rather than treat it as UploadOutcomeCreated.
	UploadOutcomeUnknown UploadOutcome = ""

	UploadOutcomeCreated UploadOutcome = "created"
	UploadOutcomeUpdated UploadOutcome = "updated"
	UploadOutcomeSkipped UploadOutcome = "skipped"
	UploadOutcomeRenamed UploadOutcome = "renamed"
)

// Back-compat aliases (P2.3, July 2026).
//
// `type PublishAction = UploadOutcome` is a Go-level type alias — the
// two names refer to the same underlying type, so existing pre-P2.3 code
// that says `var x delivery.PublishAction = delivery.PublishActionCreated`
// continues to compile unchanged. The same alias allows
// `delivery.PublishAction` and `delivery.UploadOutcome` to be used
// interchangeably in struct literals, function signatures, and
// comparisons. Future removal (filed in
// architecture/current.yaml#PR-P2.3-CLOSURE forward-pointer) gates on
// the Wave 14 mega-package split per godlike/06 SSOT.
//
// The constant aliases (PublishActionUnknown/= UploadOutcomeUnknown, etc.)
// preserve the legacy constant names while pointing to the canonical
// UploadOutcome values. `errors.Is`, switch arms, and consumers that
// branch on the legacy constants continue to work byte-stable.
type PublishAction = UploadOutcome

const (
	PublishActionUnknown = UploadOutcomeUnknown
	PublishActionCreated = UploadOutcomeCreated
	PublishActionUpdated = UploadOutcomeUpdated
	PublishActionSkipped = UploadOutcomeSkipped
	PublishActionRenamed = UploadOutcomeRenamed
)

// PublishResult is returned after a successful publish.
//
// P0 #9 (June 2026): the publisher used to drop DownloadLink,
// MD5Checksum, Action, and FolderPath from the PutFileResult. Callers
// were forced to reconstruct the download URL via string interpolation
// ("https://drive.google.com/uc?id=...") and to re-issue FindFileByName
// to recover the hash — both fragile (link drift, network race on
// the lookup). The struct now carries all four fields so no caller
// ever has to reconstruct them.
type PublishResult struct {
	// FileID is the Google Drive file ID of the uploaded file.
	FileID string `json:"file_id"`

	// WebViewLink is the human-readable Drive URL.
	WebViewLink string `json:"web_view_link,omitempty"`

	// DownloadLink is the direct download URL surfaced by Drive.
	//
	// This field is the canonical source for the download URL. Any
	// caller that needs the download link MUST read it from here —
	// never reconstruct via "https://drive.google.com/uc?id=" + FileID.
	// Reconstructing risks formatting drift (e.g. the "?export=download"
	// variant used by jobs/assets/service.go) and produces different URLs
	// for the same underlying FileID depending on call site.
	//
	// Empty when the publish was a no-op (e.g. ConflictSkip on a
	// not-yet-existing file) AND Drive did not return a transactional
	// download URL — callers should treat empty as "no link available"
	// and skip Drive-side cleanup or projection in that branch.
	DownloadLink string `json:"download_link,omitempty"`

	// MD5Checksum is the Drive-returned md5Checksum for the uploaded
	// file. Present on every successful PutFile (PutActionCreated,
	// PutActionUpdated, PutActionSkipped, PutActionRenamed) because
	// Drive returns the check-sum on both Create and Update responses
	// AND on the existing-match metadata returned by the skip branch.
	//
	// Empty when Drive did not surface the checksum (rare; the
	// Publisher logs but does not fail when this happens). Callers
	// should treat empty as "checksum not yet known" and either
	// compute the local MD5 separately or skip a checksum-pin step.
	MD5Checksum string `json:"md5_checksum,omitempty"`

	// FolderID is the resolved Drive folder the file was uploaded into.
	FolderID string `json:"folder_id"`

	// FolderPath is the slash-joined human-readable form of
	// PathSegments (e.g. "NBA News/abc123"). Useful for audit logs,
	// display layers, and asset-tree unfurls that need a single string.
	//
	// Empty when PathSegments is empty (the same condition under which
	// ResolveFolder returns the root FolderID unchanged). Coexists
	// with PathSegments: callers that need the structured form use
	// PathSegments; callers that need a single string use FolderPath.
	// PathSegments is authoritative (zero or one ordering; no
	// round-trip normalization); FolderPath is a derived view.
	FolderPath string `json:"folder_path,omitempty"`

	// Destination echoes back the requested DestinationKey.
	Destination DestinationKey `json:"destination"`

	// PathSegments are the resolved folder path segments (e.g.
	// ["clips", "NBA News", "abc123"]). Useful for auditing and
	// asset-tree upsert.
	PathSegments []string `json:"path_segments,omitempty"`

	// Action is what the publisher actually did on Drive. See
	// PublishAction constants for the canonical outcomes.
	//
	// Empty when the publisher was a pre-P0-#1 adapter that could
	// not determine the outcome. New code (post-P0-#9) MUST populate
	// Action via TranslatePutAction so consumers can branch deterministically.
	Action PublishAction `json:"action,omitempty"`
}
