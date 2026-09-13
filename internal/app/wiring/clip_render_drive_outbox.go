package wiring

// clip_render_drive_outbox.go owns the composition-root consumer for the
// clip.render Drive projection. The render handler commits the asset and this
// event atomically; this consumer performs the slow external upload and then
// reconciles the canonical Drive location.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

var errClipRenderDrivePayload = errors.New("clip.render drive delivery: invalid payload")

type clipRenderDriveDeliveryHandler struct {
	publisher delivery.Publisher
	mutator   persistence.AssetMutator
	// subtitles is the canonical ASS artifact registry. It is required only
	// when a payload carries the optional sidecar half of the bundle, so it is
	// allowed to be nil for deployments that never render sidecar subtitles.
	subtitles detail.SubtitleArtifactRepository
	log       *zap.Logger
}

func newClipRenderDriveDeliveryHandler(publisher delivery.Publisher, mutator persistence.AssetMutator, subtitles detail.SubtitleArtifactRepository, log *zap.Logger) (*clipRenderDriveDeliveryHandler, error) {
	if publisher == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: publisher is required")
	}
	if mutator == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: asset mutator is required")
	}
	if log == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: logger is required")
	}
	return &clipRenderDriveDeliveryHandler{publisher: publisher, mutator: mutator, subtitles: subtitles, log: log}, nil
}

func (h *clipRenderDriveDeliveryHandler) EventType() string {
	return cliprender.EventClipRenderDriveDeliveryRequested
}

func (h *clipRenderDriveDeliveryHandler) IdempotencyKey() string {
	return cliprender.EventClipRenderDriveDeliveryRequested
}

func (h *clipRenderDriveDeliveryHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return fmt.Errorf("%w: nil outbox claim", errClipRenderDrivePayload)
	}
	evt := claim.Event
	var req cliprender.ClipRenderDriveDeliveryRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("%w: json: %v", errClipRenderDrivePayload, err)
	}
	// Locator-first: the intent is valid with EITHER a locally staged path OR a
	// certified object-store locator. The canonical path carries no local file
	// and the publisher streams object-store → Drive.
	if req.SchemaVersion != "clip.render.drive_delivery.v1" ||
		strings.TrimSpace(req.AssetID) == "" ||
		(strings.TrimSpace(req.LocalPath) == "" && strings.TrimSpace(req.ArtifactURL) == "") ||
		strings.TrimSpace(req.Filename) == "" || strings.TrimSpace(req.FolderID) == "" ||
		strings.TrimSpace(req.ContentHash) == "" || req.SizeBytes <= 0 {
		return fmt.Errorf("%w: missing schema/asset/path-or-url/name/folder/hash/size", errClipRenderDrivePayload)
	}
	if localPath := strings.TrimSpace(req.LocalPath); localPath != "" {
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			return fmt.Errorf("clip.render Drive upload %s: staged artifact unavailable: %w", req.AssetID, statErr)
		}
		if info.Size() != req.SizeBytes {
			return fmt.Errorf("clip.render Drive upload %s: local size=%d want=%d", req.AssetID, info.Size(), req.SizeBytes)
		}
	}
	result, err := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: req.FolderID,
		LocalPath:           req.LocalPath,
		SourceURL:           req.ArtifactURL,
		ContentType:         req.ContentType,
		Filename:            req.Filename,
		AssetID:             req.AssetID,
		ProjectID:           req.RunID,
		SizeBytes:           req.SizeBytes,
		ContentHash:         req.ContentHash,
		SourceVersion:       1,
		// Stable LOGICAL identity, never the artifact digest: a rerender of the
		// same clip must replace this Drive file. Deriving the key from
		// AssetID+ContentHash (both content-addressed, both changing on every
		// rerender) made the publisher's idempotency-key lookup miss the file it
		// had published one render earlier, so ConflictOverwrite was never
		// reached and each rerender created a same-named duplicate in the
		// destination folder. This is the same identity the synchronous
		// publisher derives (SourceAssetID:filename + policy version).
		IdempotencyKey: delivery.DeriveIdempotencyKey(
			delivery.DestinationClipMetadata,
			req.SourceAssetID+":"+req.Filename,
			cliprender.ClipRenderDrivePolicyVersion,
			1,
		),
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	if err != nil {
		return fmt.Errorf("clip.render Drive upload %s: %w", req.AssetID, err)
	}
	if result == nil || result.FileID == "" {
		return fmt.Errorf("%w: Drive returned no file id for %s", errClipRenderDrivePayload, req.AssetID)
	}
	if err := h.mutator.ReconcileDriveLocations(ctx, []persistence.DriveLocationPatch{{
		AssetID: req.AssetID, DriveFileID: result.FileID, DriveLink: result.WebViewLink,
		DownloadURL: result.DownloadLink,
	}}); err != nil {
		return fmt.Errorf("clip.render Drive reconcile %s: %w", req.AssetID, err)
	}
	if err := h.markDeliveryCompleted(ctx, req, result); err != nil {
		return err
	}
	if err := h.deliverSidecar(ctx, req); err != nil {
		return err
	}
	h.removeStagedArtifact(req.AssetID, req.LocalPath)
	if req.Sidecar != nil {
		h.removeStagedArtifact(req.AssetID, req.Sidecar.LocalPath)
	}
	h.log.Info("clip.render Drive delivery completed",
		zap.String("asset_id", req.AssetID), zap.String("drive_file_id", result.FileID),
		zap.String("event_key", evt.EventKey), zap.Int64("size_bytes", req.SizeBytes),
		zap.Bool("sidecar_delivered", req.Sidecar != nil))
	return nil
}

// markDeliveryCompleted flips the rendered asset's delivery_status from the
// intent's "pending" to "completed" and records the resolved Drive identity.
// The JSON merge patch preserves every other metadata key.
func (h *clipRenderDriveDeliveryHandler) markDeliveryCompleted(ctx context.Context, req cliprender.ClipRenderDriveDeliveryRequest, result *delivery.PublishResult) error {
	patch, err := json.Marshal(map[string]any{
		"delivery_status": "completed",
		"drive_file_id":   result.FileID,
		"drive_link":      result.WebViewLink,
	})
	if err != nil {
		return fmt.Errorf("clip.render Drive delivery %s: encode metadata patch: %w", req.AssetID, err)
	}
	patchJSON := string(patch)
	if err := h.mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: req.AssetID, MetadataPatchJSON: &patchJSON}); err != nil {
		return fmt.Errorf("clip.render Drive delivery %s: mark delivery complete: %w", req.AssetID, err)
	}
	return nil
}

// deliverSidecar uploads the optional ASS bundle half of the intent, records it
// in the canonical subtitle-artifact registry and reflects its identity on the
// rendered asset. Fail-closed: a payload that declares a sidecar without a
// wired registry is an error, never a silently dropped artifact.
func (h *clipRenderDriveDeliveryHandler) deliverSidecar(ctx context.Context, req cliprender.ClipRenderDriveDeliveryRequest) error {
	sidecar := req.Sidecar
	if sidecar == nil {
		return nil
	}
	if strings.TrimSpace(sidecar.LocalPath) == "" || strings.TrimSpace(sidecar.Filename) == "" ||
		strings.TrimSpace(sidecar.SHA256) == "" || sidecar.SizeBytes <= 0 {
		return fmt.Errorf("%w: incomplete sidecar block for %s", errClipRenderDrivePayload, req.AssetID)
	}
	if h.subtitles == nil {
		return fmt.Errorf("clip.render Drive delivery %s: sidecar declared but the subtitle artifact registry is not wired", req.AssetID)
	}
	info, err := os.Stat(sidecar.LocalPath)
	if err != nil {
		return fmt.Errorf("clip.render Drive sidecar %s: staged artifact unavailable: %w", req.AssetID, err)
	}
	if info.Size() != sidecar.SizeBytes {
		return fmt.Errorf("clip.render Drive sidecar %s: local size=%d want=%d", req.AssetID, info.Size(), sidecar.SizeBytes)
	}
	result, err := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: req.FolderID,
		LocalPath:           sidecar.LocalPath,
		Filename:            sidecar.Filename,
		AssetID:             req.AssetID,
		ProjectID:           req.RunID,
		SizeBytes:           sidecar.SizeBytes,
		ContentHash:         sidecar.SHA256,
		SourceVersion:       1,
		// Same logical-identity rule as the video: the ASS sidecar belongs to
		// (source asset, filename), not to the video bytes it was compiled
		// against.
		IdempotencyKey: delivery.DeriveIdempotencyKey(
			delivery.DestinationClipMetadata,
			req.SourceAssetID+":"+sidecar.Filename,
			cliprender.ClipRenderDrivePolicyVersion,
			1,
		),
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	if err != nil {
		return fmt.Errorf("clip.render Drive sidecar upload %s: %w", req.AssetID, err)
	}
	if result == nil || result.FileID == "" {
		return fmt.Errorf("%w: Drive returned no file id for sidecar %s", errClipRenderDrivePayload, req.AssetID)
	}
	language := strings.TrimSpace(sidecar.LanguageCode)
	if language == "" {
		language = "und"
	}
	if err := h.subtitles.Upsert(ctx, &detail.SubtitleArtifact{
		AssetID:       req.SourceAssetID,
		LanguageCode:  language,
		Format:        detail.SubtitleFormatASS,
		LocalPath:     sidecar.LocalPath,
		DriveFileID:   result.FileID,
		DriveURL:      result.WebViewLink,
		LegacyFileMD5: sidecar.SHA256,
		TextHash:      sidecar.TextHash,
		StyleVersion:  sidecar.StyleVersion,
		Status:        detail.SubtitleStatusReady,
		IsCurrent:     true,
	}); err != nil {
		return fmt.Errorf("clip.render Drive sidecar registry %s: %w", req.AssetID, err)
	}
	patch, err := json.Marshal(map[string]any{
		"subtitle_file_id":    result.FileID,
		"subtitle_drive_link": result.WebViewLink,
		"subtitle_status":     string(detail.SubtitleStatusReady),
	})
	if err != nil {
		return fmt.Errorf("clip.render Drive sidecar %s: encode metadata patch: %w", req.AssetID, err)
	}
	patchJSON := string(patch)
	if err := h.mutator.PatchAsset(ctx, persistence.AssetPatch{AssetID: req.AssetID, MetadataPatchJSON: &patchJSON}); err != nil {
		return fmt.Errorf("clip.render Drive sidecar metadata %s: %w", req.AssetID, err)
	}
	h.log.Info("clip.render Drive sidecar delivered",
		zap.String("asset_id", req.AssetID),
		zap.String("drive_file_id", result.FileID),
		zap.Int64("size_bytes", sidecar.SizeBytes))
	return nil
}

func (h *clipRenderDriveDeliveryHandler) removeStagedArtifact(assetID, path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		h.log.Warn("clip.render Drive delivery completed but local artifact cleanup failed",
			zap.String("asset_id", assetID), zap.String("path", path), zap.Error(err))
	}
}

// imageDriveDeliveryPGHandler adapts the capability-owned image payload
// handler to the PostgreSQL media outbox worker. Image delivery intents are
// emitted by the same Postgres AssetCommitter as clip intents.
type imageDriveDeliveryPGHandler struct {
	handler *imagesapp.ImageDriveDeliveryHandler
}

func (h imageDriveDeliveryPGHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return fmt.Errorf("image Drive delivery: nil outbox claim")
	}
	return h.handler.HandlePayload(ctx, claim.Event.PayloadJSON)
}
