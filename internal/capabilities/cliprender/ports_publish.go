package cliprender

import "context"

// PublicationMetrics carries the publisher's OWN measured publication
// sub-phase walls. The publisher is the single chronometer owner for its
// phases; the worker projects these into RenderMetricsV2 (publication_total_ms
// / artifact_publish_ms / drive_upload_ms) and never re-times publication
// when this report is present. HashMS/TaxonomyResolveMS/AssetCommitMS run
// sequentially (their sum is the local artifact work); VideoUploadMS and
// SidecarUploadMS run concurrently (the drive upload wall is their max,
// never their sum).
type PublicationMetrics struct {
	HashMS            int64
	VideoUploadMS     int64
	SidecarUploadMS   int64
	TaxonomyResolveMS int64
	AssetCommitMS     int64
	TotalMS           int64
}

// RenderPublishResult is the typed outcome of publishing + committing the
// derived asset. AssetID is the canonical media_assets.id; DriveLink is the
// canonical Drive web link (never reconstructed by the worker). Publish is
// the publisher-owned measurement report projected into the canonical V2
// execution report.
type RenderPublishResult struct {
	AssetID     string
	DriveFileID string
	DriveLink   string

	// DrivePending means the artifact is committed and the Drive upload was
	// durably handed to the outbox. It is intentionally distinct from an
	// upload failure: the render job may complete while the external delivery
	// continues in the background.
	DrivePending bool

	// DeliveryStatus names the durable delivery state committed with the
	// asset: "pending" for the asynchronous Drive outbox, "not_required"
	// when the certified object-store locator IS the delivery (CR-19).
	DeliveryStatus string
	// ContentURL is the authenticated route that streams the committed bytes
	// (object-store delivery). Empty on the Drive-only path, where the Drive
	// link is the consumer-facing address.
	ContentURL string

	// Reused means this job performed NO publication: the asset identity below
	// belongs to a render that already completed and published, and the
	// deterministic render cache handed back its certified locator.
	//
	// It is deliberately distinct from BOTH publication states. The original
	// render either published synchronously or handed delivery to the outbox, and
	// this job cannot know which, so claiming PUBLISHED would assert a fact it
	// never observed and claiming PENDING (the historical fabrication) would
	// announce a delivery nobody owes. DriveFileID/DriveLink are empty in this
	// case; the result projects publication_status=REUSED.
	Reused        bool
	SizeBytes     int64
	SidecarFileID string
	SidecarLink   string
	Publish       *PublicationMetrics
}

// AssetContentURL is the authenticated route that streams a committed asset's
// bytes. Object-store delivery (CR-19) is the only mode that sets it; a
// Drive-delivered asset keeps drive_link as its consumer-facing address.
func AssetContentURL(assetID string) string {
	return "/api/v1/media/assets/" + assetID + "/content"
}

// EventClipRenderDriveDeliveryRequested is emitted atomically with the
// rendered media asset when clip.render uses asynchronous Drive delivery.
// The outbox consumer uploads the durable local artifact and then reconciles
// the Drive location on the canonical asset row.
const EventClipRenderDriveDeliveryRequested = "clip.render.drive_delivery.requested.v1"

// ClipRenderDrivePolicyVersion is the logical-identity version of the
// clip.render Drive publication. The upload idempotency key is derived from
// (source asset, destination filename, this version) so a RERENDER of the same
// clip replaces the SAME Drive file.
//
// It deliberately does NOT fold in the artifact digest: a valid rerender
// produces different bytes — hence a different content hash and a different
// derived asset id — and folding either into the key made the publisher's
// idempotency-key lookup miss the previously published file, so
// ConflictOverwrite was never reached and every rerender created a same-named
// duplicate in the destination folder. Both the synchronous publisher and the
// asynchronous outbox consumer derive the key from this ONE version literal.
const ClipRenderDrivePolicyVersion = "clip-render-v1"

// ClipRenderDriveDeliveryRequest is the durable outbox payload for a rendered
// clip's external Drive projection. It contains no credentials and is
// idempotent by AssetID + ContentHash + FolderID.
//
// Sidecar, when non-nil, carries the compiled ASS subtitle artifact that must
// travel with the video: the delivery intent is a BUNDLE (video + optional
// sidecar), so an explicit sidecar request no longer forces the synchronous
// Drive path. The outbox consumer uploads the sidecar, upserts the canonical
// subtitle artifact row and patches the asset metadata in the same delivery
// that uploads the video, so the render job can complete without waiting for
// Drive in every subtitle mode.
type ClipRenderDriveDeliveryRequest struct {
	SchemaVersion string `json:"schema_version"`
	AssetID       string `json:"asset_id"`
	RunID         string `json:"run_id"`
	SourceAssetID string `json:"source_asset_id"`
	// LocalPath is the OPTIONAL locally staged copy. Empty on the canonical
	// locator-first path: the consumer streams from ArtifactURL instead.
	LocalPath string `json:"local_path,omitempty"`
	// StorageKey/ArtifactURL/ContentType are the certified object-store locator
	// the consumer streams from when LocalPath is empty.
	StorageKey  string `json:"storage_key,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Filename    string `json:"filename"`
	FolderID    string `json:"folder_id"`
	ContentHash string `json:"content_hash"`
	SizeBytes   int64  `json:"size_bytes"`
	// Sidecar (when non-nil) is always a local compiled ASS artifact; the
	// bundle contract is unchanged.
	Sidecar *ClipRenderSubtitleDelivery `json:"sidecar,omitempty"`
}

// ClipRenderSubtitleDelivery is the optional ASS sidecar half of an async
// clip.render Drive delivery. LocalPath points at the durable staging copy (the
// per-job workspace is deleted as soon as the job finishes); SourceAssetID is
// implied by the parent payload because subtitles belong to the source clip.
type ClipRenderSubtitleDelivery struct {
	LocalPath    string `json:"local_path"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	LanguageCode string `json:"language_code,omitempty"`
	TextHash     string `json:"text_hash,omitempty"`
	StyleVersion string `json:"style_version,omitempty"`
}

// RenderPublisher publishes the validated output to Drive through the
// canonical delivery publisher and commits it as a derived media asset
// inside ONE SQLite transaction (mirror of the canonical final-audio
// publisher). Fail-closed: an unwired publisher, a missing output, or a
// failed commit is a typed error — the job never reports success without
// the derived asset durably committed.
type RenderPublisher interface {
	Publish(ctx context.Context, in RenderPublishInput) (*RenderPublishResult, error)
}

// DestinationFolderResolveInput is the fully-resolved input for the
// DestinationFolderResolver: the Drive root the caller selected plus the
// optional human subfolder name (typically the script/batch title). The
// resolver never re-derives either — it maps root + name to the leaf folder.
type DestinationFolderResolveInput struct {
	RootFolderID  string
	SubfolderName string
}

// DestinationFolderResolver resolves (create-or-reuse) the Drive leaf folder
// a clip.render batch publishes into: RootFolderID/<SafeName(SubfolderName)>.
// The canonical adapter routes through delivery.Publisher.ResolveFolder — the
// same publisher that performs the final upload — so folder creation stays in
// ONE canonical owner (never a second Drive reach-through inside cliprender).
//
// The worker calls this ONCE per job when the request carries
// destination.subfolder_name. All clips of one script/batch carry the same
// name, so sequential or concurrent jobs converge on a single shared leaf
// folder (EnsureFolder is idempotent + singleflight-deduped in-process). The
// canonical adapter additionally memoizes (root + name) → leaf folder ID for
// a bounded TTL with singleflight-deduped misses, so a whole batch costs one
// create-or-reuse round-trip per TTL window even across sequential jobs
// (errors are never cached; the TTL bounds staleness). The publisher stays
// dumb: it receives the fully-resolved leaf folder ID and never creates
// folders itself. Fail-closed: a resolver returning an empty folder ID is a
// typed error, never a silent root fallback.
type DestinationFolderResolver interface {
	ResolveDestinationFolder(ctx context.Context, in DestinationFolderResolveInput) (string, error)
}

// OverlaySegment is the materialized overlay artifact the final video
// composites over the source. LocalPath is the local, content-addressed
// file; SHA256 is its verified digest. RenderKey is the same content
// key the OverlayRefSpec declares, so the resolved segment can be proven
// to be the exact artifact that render_job_id produced.
type OverlaySegment struct {
	RenderJobID string
	RenderKey   string
	LocalPath   string
	SHA256      string
	SizeBytes   int64
}

// OverlayResolveInput is the fully-resolved input for the overlay segment
// resolution: the lineage the request declares (render_job_id + render_key).
// The resolver never re-derives anything — it maps the declared identity to
// the materialized artifact.
type OverlayResolveInput struct {
	RenderJobID string
	RenderKey   string
}

// OverlaySegmentResolver resolves the overlay.render artifact (the rendered
// overlay segment) from the lineage the clip.render request declares. This
// is the "render_job_id → artifact" hop of the compositing chain. Fail-
// closed: an unresolvable segment (unknown job, missing artifact, hash
// mismatch) is a typed error — the worker never composites a phantom
// segment.
type OverlaySegmentResolver interface {
	Resolve(ctx context.Context, in OverlayResolveInput) (*OverlaySegment, error)
}

// NOTE: the overlay COMPOSITOR port was DEMOLISHED with the single-pass
// overlay cutover. A declared overlay is no longer blended by a post-render
// pass (which encoded the whole clip a second time); the resolved segment is
// sealed into ClipRenderPlanV1.Overlay and composited inside the same Chronon
// render as a timed video layer. OverlaySegmentResolver is the only remaining
// overlay port, and scripts/ci/check_clip_render_cutover.sh fails on any new
// FFmpeg overlay compositor caller.
