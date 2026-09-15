package adapters

import (
	"context"
	"encoding/json"

	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ClipRenderPublisher is the composition-root adapter for the final render
// boundary. Drive publication goes through delivery.Publisher; the derived
// asset and its location are committed through the canonical AssetCommitter.
//
// Every boundary step (hash, upload video, upload sidecar, taxonomy resolve,
// PostgreSQL commit) is logged with structured zap entries and timed so the
// pipelinegen call chain can be reconstructed from logs alone.
type ClipRenderPublisher struct {
	drive             delivery.Publisher
	committer         persistence.AssetCommitter
	log               *zap.Logger
	asyncStagingRoot  string
	subtitleArtifacts detail.SubtitleArtifactRepository
}

// SetSubtitleArtifactRepository attaches the canonical ASS artifact registry.
func (p *ClipRenderPublisher) SetSubtitleArtifactRepository(repo detail.SubtitleArtifactRepository) {
	if p != nil {
		p.subtitleArtifacts = repo
	}
}

// SetAsyncDriveStagingRoot configures the durable root used to detach an
// asynchronous clip artifact from the per-job workspace. The workspace is
// cleaned as soon as the job finishes; this root must therefore be outside it.
func (p *ClipRenderPublisher) SetAsyncDriveStagingRoot(root string) {
	if p != nil {
		p.asyncStagingRoot = strings.TrimSpace(root)
	}
}

// NewClipRenderPublisher builds the publisher; log is required so each
// publication phase is observable.
func NewClipRenderPublisher(drive delivery.Publisher, committer persistence.AssetCommitter, log *zap.Logger) (*ClipRenderPublisher, error) {
	if drive == nil || committer == nil {
		return nil, errors.New("clip.render publisher: Drive publisher and asset committer are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipRenderPublisher{drive: drive, committer: committer, log: log}, nil
}

// publishPhase is a per-boundary-step diagnostic trace (hash, video upload,
// sidecar upload, taxonomy, commit) emitted at Debug. The canonical operator
// events for a published clip are the single `clip.render.publish.completed`
// line below plus the worker's `clip.render.job.completed`; at Info this
// helper cost ~6 lines of log per clip for facts the publication metrics and
// RunReport already own.
func (p *ClipRenderPublisher) publishPhase(phase, runID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "clip_render_publish"),
		zap.String("phase", phase),
		zap.String("run_id", runID),
	}, fields...)
	p.log.Debug("clip.render.publish.phase", all...)
}

// publishMetrics tracks the wall-clock duration of each publish phase.
type publishMetrics struct {
	HashMS               int64 `json:"hash_ms"`
	VideoUploadMS        int64 `json:"video_upload_ms"`
	SidecarUploadMS      int64 `json:"sidecar_upload_ms"`
	TaxonomyResolveMS    int64 `json:"taxonomy_resolve_ms"`
	AssetCommitMS        int64 `json:"asset_commit_ms"`
	TotalMS              int64 `json:"total_ms"`
	VideoUploadSucceeded bool  `json:"video_upload_succeeded"`
	SidecarUploaded      bool  `json:"sidecar_upload_succeeded"`
}

func (p *ClipRenderPublisher) Publish(ctx context.Context, in cliprender.RenderPublishInput) (*cliprender.RenderPublishResult, error) {
	if p == nil || p.drive == nil || p.committer == nil {
		return nil, fmt.Errorf("clip.render publisher: Drive publisher and asset committer are required")
	}
	if in.Outcome == nil || in.DriveFolderID == "" {
		return nil, fmt.Errorf("clip.render publisher: outcome and destination folder are required")
	}
	// Locator-first: the artifact is either a local path (a consumer
	// materialized it) or a certified object-store locator. At least one must
	// be present; the Drive outbox streams from the locator when there is no
	// local copy.
	if strings.TrimSpace(in.OutputPath) == "" && strings.TrimSpace(in.ArtifactURL) == "" {
		return nil, fmt.Errorf("clip.render publisher: output path or artifact locator is required")
	}
	metrics := publishMetrics{}
	started := time.Now()
	runID := in.RunID

	p.publishPhase("start", runID,
		zap.String("output_path", in.OutputPath),
		zap.String("drive_folder_id", in.DriveFolderID),
		zap.String("source_asset_id", in.SourceAssetID),
		zap.Int64("outcome_size_bytes", in.Outcome.SizeBytes),
		zap.Bool("has_subtitles", in.Subtitles != nil),
	)

	// ── Phase 1: adopt the caller-certified artifact digest ───────────
	// The render boundary already certified these exact bytes: the
	// RenderingGen download computed SHA-256 while streaming the artifact to
	// disk (and verified it against the queue's expected digest), and the
	// overlay compositor digested its own encode. Re-reading the file here was
	// a third full pass over the same bytes on the render critical path.
	// Fail-closed: an artifact published without a certified digest is a typed
	// error — the publisher never silently re-hashes what the caller claimed.
	contentHash := strings.ToLower(strings.TrimSpace(in.CertifiedSHA256))
	size := in.CertifiedSizeBytes
	if contentHash == "" {
		p.publishPhase("certified_digest_missing", runID, zap.String("output_path", in.OutputPath))
		return nil, fmt.Errorf("publish rendered clip: certified SHA-256 missing for %s (the producing boundary must certify the artifact)", in.OutputPath)
	}
	if size <= 0 {
		p.publishPhase("certified_size_missing", runID,
			zap.String("output_path", in.OutputPath), zap.String("sha256", contentHash))
		return nil, fmt.Errorf("publish rendered clip: certified size missing for %s", in.OutputPath)
	}
	// No hash pass ran, so the phase carries no measured work. HashMS stays 0
	// (a real zero, not NOT_INSTRUMENTED): the certified digest replaced the
	// read instead of hiding it.
	metrics.HashMS = 0
	assetID := "cliprender_" + contentHash[:24]

	ext := artifactExtension(in.OutputPath, in.ArtifactContentType, in.ArtifactURL)
	base := assetID
	if strings.TrimSpace(in.SourceTitle) != "" {
		safeTitle := textutil.SanitizeFilename(in.SourceTitle)
		if safeTitle != "" && safeTitle != "unnamed" {
			base = safeTitle
		}
	}
	// Multilingual fan-out discriminator. The SAME source clip is rendered once
	// per language and every variant shares the source title, so without a
	// language suffix all variants resolve to ONE Drive filename and each
	// upload overwrites the previous one — only the last language survived the
	// publication. The suffix mirrors the canonical voiceover
	// "{slug}_{lang}" filename convention and keeps one distinct artifact per
	// (clip, language). A non-localized render carries no language and keeps its
	// historical filename verbatim.
	if tag := languageFilenameTag(in.Transcript); tag != "" {
		base += "_" + tag
	}
	driveFilename := base + ext

	// Subtitle sidecars are uploaded ONLY when explicitly in sidecar mode.
	// Burned subtitles are already baked into video frames and must never be uploaded as .ass files to Drive.
	hasSubtitleSidecar := in.Subtitles != nil && in.Subtitles.Mode == cliprender.SubtitlesModeSidecar
	p.publishPhase("hash_done", runID,
		zap.String("content_hash", contentHash),
		zap.Int64("size_bytes", size),
		zap.String("asset_id", assetID),
		zap.String("drive_filename", driveFilename),
		zap.Bool("has_subtitle_sidecar", hasSubtitleSidecar),
		zap.Int64("duration_ms", metrics.HashMS),
	)
	// Clip publication is UNCONDITIONALLY asynchronous: the rendered asset and
	// a durable Drive-delivery intent are committed together, and the outbox
	// consumer owns the later upload. The synchronous Drive branch (and the
	// ClipAsyncDriveEnabled flag that selected it) was DELETED in the
	// 2026-09-13 audit — a Google Drive round-trip must never sit on the render
	// critical path, and there is deliberately no synchronous fallback left to
	// select by accident.
	return p.publishAsyncDrive(ctx, in, started, metrics.HashMS, contentHash, size, assetID, driveFilename, hasSubtitleSidecar)
}

// languageFilenameTag returns the filename discriminator for a render's
// language track. It returns "" when the render carries no language (or an
// undetermined one), so a plain single-language render keeps the filename it
// has always published.
func languageFilenameTag(transcript *cliprender.TranscriptResult) string {
	if transcript == nil {
		return ""
	}
	tag := textutil.SanitizeFilename(strings.TrimSpace(transcript.Language))
	if tag == "" || tag == "unnamed" || tag == "und" {
		return ""
	}
	return tag
}

// clipSearchText composes the lexical/semantic search_text for a derived clip
// render. It is the value the media SSOT stores and the index worker embeds, so
// an empty result is a fail-closed dead-letter rather than a silent miss. The
// clip's own spoken content is the strongest searchable signal, so the human
// source title, the language and the (translated) transcript text are all
// included, in that order.
func clipSearchText(sourceTitle string, transcript *cliprender.TranscriptResult) string {
	lang, body := "", ""
	if transcript != nil {
		lang = strings.TrimSpace(transcript.Language)
		body = transcript.Text
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{sourceTitle, lang, body} {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			parts = append(parts, p)
		}
	}
	text := strings.Join(parts, " ")
	// search_text is an index input, not a document store: bound it so a long
	// transcript cannot bloat every media_assets row.
	const maxSearchTextRunes = 2000
	if utf8.RuneCountInString(text) > maxSearchTextRunes {
		text = string([]rune(text)[:maxSearchTextRunes])
	}
	return text
}

// publishAsyncDrive completes the local, durable half of publication and
// emits a transactionally-persisted Drive intent. The intent is a BUNDLE: the
// rendered video plus, when the request selected sidecar subtitles, the
// compiled ASS artifact. Both artifacts are detached from the per-job
// workspace before the commit, so the outbox consumer can drain long after the
// workspace has been cleaned.
func (p *ClipRenderPublisher) publishAsyncDrive(
	ctx context.Context,
	in cliprender.RenderPublishInput,
	started time.Time,
	hashMS int64,
	contentHash string,
	size int64,
	assetID, driveFilename string,
	hasSubtitleSidecar bool,
) (*cliprender.RenderPublishResult, error) {
	// Locator-first: only a locally materialized artifact is staged. The
	// canonical path carries no local bytes, so there is nothing to copy and
	// the outbox consumer streams from ArtifactURL.
	stagedPath := ""
	if strings.TrimSpace(in.OutputPath) != "" {
		staged, err := p.stageAsyncArtifact(in.OutputPath, assetID, size)
		if err != nil {
			return nil, fmt.Errorf("stage rendered artifact for asynchronous Drive delivery: %w", err)
		}
		stagedPath = staged
	}
	sidecar, err := p.stageAsyncSubtitle(in, assetID, hasSubtitleSidecar)
	if err != nil {
		return nil, err
	}
	taxonomyStart := time.Now()
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	taxonomyMS := time.Since(taxonomyStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("resolve rendered taxonomy: %w", err)
	}

	payload := cliprender.ClipRenderDriveDeliveryRequest{
		SchemaVersion: "clip.render.drive_delivery.v1",
		AssetID:       assetID, RunID: in.RunID, SourceAssetID: in.SourceAssetID,
		LocalPath: stagedPath, Filename: driveFilename,
		StorageKey: in.ArtifactStorageKey, ArtifactURL: in.ArtifactURL, ContentType: in.ArtifactContentType,
		FolderID: in.DriveFolderID, ContentHash: contentHash, SizeBytes: size,
		Sidecar: sidecar,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Drive delivery intent: %w", err)
	}
	keyDigest := digest.SHA256Bytes([]byte(assetID + "|" + contentHash + "|" + in.DriveFolderID))
	commitRequest := persistence.AssetCommitRequest{
		AssetID: assetID, Source: "clip.render", Name: driveFilename,
		// The derived asset MUST carry a search_text: the index worker reads it
		// back to embed the asset, and an empty value makes the embedder fail
		// closed, so every render dead-lettered asset.index.requested (delivered
		// to Drive but never searchable). See clipSearchText.
		SearchText: clipSearchText(in.SourceTitle, in.Transcript),
		Filename:   driveFilename, MediaType: "video", Category: "clip-render",
		DurationMs: int64(in.Outcome.DurationSec * 1000), ContentHash: contentHash,
		LifecycleState: "ACTIVE", IndexState: "DISCOVERED", LocalPath: stagedPath,
		FolderID: in.DriveFolderID, SourceURL: in.SourceAssetID,
		AssetVersion: contentHash, Rendition: "rendered", Title: driveFilename,
		SourceProvider: "pipelinegen", Taxonomy: taxonomy,
		Metadata: persistence.TypedMetadata{Title: driveFilename, Origin: "clip.render", SourceVersion: contentHash,
			PublishAction: "clip.render", SizeBytes: size, Extra: map[string]any{
				"source_asset_id": in.SourceAssetID, "plan_run_id": in.RunID,
				"delivery_status": "pending", "drive_folder_id": in.DriveFolderID,
			}},
		EmitIndexEvent: true,
		AdditionalOutboxEvents: []persistence.OutboxEvent{{
			EventType:   cliprender.EventClipRenderDriveDeliveryRequested,
			AggregateID: assetID, AggregateType: "media_asset",
			PayloadJSON: string(payloadJSON), EventKey: "clip-render-drive:" + keyDigest,
		}},
	}
	commitStart := time.Now()
	_, err = p.committer.CommitAsset(ctx, commitRequest)
	commitMS := time.Since(commitStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("commit rendered asset with Drive intent: %w", err)
	}

	metrics := &cliprender.PublicationMetrics{
		HashMS: hashMS, TaxonomyResolveMS: taxonomyMS, AssetCommitMS: commitMS,
		TotalMS: time.Since(started).Milliseconds(),
	}
	p.publishPhase("drive_queued", in.RunID,
		zap.String("asset_id", assetID), zap.String("event_type", cliprender.EventClipRenderDriveDeliveryRequested),
		zap.Int64("duration_ms", metrics.TotalMS), zap.Int64("drive_upload_ms", -1),
	)
	return &cliprender.RenderPublishResult{
		AssetID: assetID, DrivePending: true, SizeBytes: size, Publish: metrics,
	}, nil
}
