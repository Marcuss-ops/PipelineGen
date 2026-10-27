// broker_finalize_deferred.go — the deferred Drive-publication half of the
// finalize spine (L10). Artifacts whose Drive identity is not consumed at
// completion are staged into the local content-addressed object store (H1)
// and their publication intent is emitted as a finalize.drive_delivery
// outbox event INSIDE the completion transaction; the registered consumer
// performs the upload later and reconciles the Drive locator against the
// canonical asset.
//
// Scope is deliberately narrow (godlike/07): only the script.generate
// post_writer deliverables (script + voiceover/audio artifacts) defer — the
// exact surface measured in the 2026-10-26 drive_publish analysis. Every
// other job type and artifact kind keeps the synchronous publication path,
// and a producer can force either direction per artifact through the
// drive_publication metadata override.
package local

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// deferredDriveJobType is the only job type whose finalize artifacts defer
// their Drive publication. Its post_writer_finalize deliverables (script.json
// + final_audio.m4a) have no consumer of their Drive identity at completion:
// final runs consume the runner-published master audio (single publication
// owner, ReferencesPublishedAudio) and the overlay deliverables ride their
// own RequireDriveBeforeReturn gate — neither reads these artifacts.
//
// The wire literal is owned by internal/kernel/job (godlike/06 one owner per
// fact): this is an alias of the canonical constant, never a re-declaration.
const deferredDriveJobType = job.TypeScriptGenerate

// finalizeDrivePublicationDeferrable reports whether this staged artifact may
// publish its Drive copy after completion instead of inside it. The
// per-artifact drive_publication metadata override wins over the scope rule:
// "sync" pins the synchronous path (today's behaviour), "deferred" requests
// the asynchronous one.
func finalizeDrivePublicationDeferrable(jobType string, va finalization.VerifiedArtifact) bool {
	override := ""
	if value, ok := va.ArtifactMetadata[finalization.DrivePublicationMetadataKey].(string); ok {
		override = strings.ToLower(strings.TrimSpace(value))
	}
	switch override {
	case finalization.DrivePublicationSync:
		return false
	case finalization.DrivePublicationDeferred:
		return true
	}
	if strings.TrimSpace(jobType) != deferredDriveJobType {
		return false
	}
	switch va.Kind {
	case finalization.KindScript, finalization.KindVoiceover:
		return true
	default:
		return false
	}
}

// stageDeferredDrivePublication detaches the certified artifact bytes from the
// ephemeral per-job workspace into the content-addressed object store and
// builds the durable delivery intent. The object store write is verified
// against the completion's own certified digest/size: if the file changed
// after staging, the intent fails closed instead of deferring a delivery of
// different bytes than the manifest certified.
func (b *Broker) stageDeferredDrivePublication(ctx context.Context, jobID string, va finalization.VerifiedArtifact) (finalization.FinalizeDriveDeliveryRequest, error) {
	if b.deferredDriveStore == nil {
		return finalization.FinalizeDriveDeliveryRequest{}, fmt.Errorf("broker: deferred Drive publication is not wired")
	}
	file, err := os.Open(va.LocalPath)
	if err != nil {
		return finalization.FinalizeDriveDeliveryRequest{}, fmt.Errorf("broker: open staged artifact %q: %w", va.ArtifactID, err)
	}
	defer func() { _ = file.Close() }()
	object, err := b.deferredDriveStore.Put(ctx, file)
	if err != nil {
		return finalization.FinalizeDriveDeliveryRequest{}, fmt.Errorf("broker: stage artifact %q into the object store: %w", va.ArtifactID, err)
	}
	if object.SHA256 != strings.ToLower(strings.TrimSpace(va.SHA256)) || object.SizeBytes != va.SizeBytes {
		return finalization.FinalizeDriveDeliveryRequest{}, fmt.Errorf(
			"broker: staged artifact %q changed after certification (object %s/%d, manifest %s/%d)",
			va.ArtifactID, object.SHA256, object.SizeBytes, va.SHA256, va.SizeBytes)
	}
	return finalization.NewFinalizeDriveDeliveryRequest(finalization.FinalizeDriveDeliveryRequest{
		JobID:              jobID,
		AssetID:            va.ArtifactID,
		ArtifactID:         va.ArtifactID,
		Kind:               va.Kind,
		Filename:           va.Filename,
		MIMEType:           va.MIMEType,
		SHA256:             va.SHA256,
		SizeBytes:          va.SizeBytes,
		IdempotencyKey:     va.IdempotencyKey,
		Requirement:        va.Requirement,
		StorageKey:         object.SHA256,
		Description:        va.Description,
		Source:             va.Source,
		ProjectID:          va.ProjectID,
		Language:           va.Language,
		DriveSubpath:       va.DriveSubpath,
		ArtifactMetadata:   va.ArtifactMetadata,
		ResolvedFolderID:   va.ResolvedFolderID,
		RootFolderResolved: va.RootFolderResolved,
	}), nil
}

// deferredDriveEventKey is the deterministic outbox dedup key for one delivery
// intent: the same job attempt can never enqueue the same publication twice,
// and a re-run with identical bytes shares the object store copy.
func deferredDriveEventKey(jobID string, va finalization.VerifiedArtifact) string {
	return "finalize-drive:" + strings.TrimSpace(jobID) + ":" + strings.TrimSpace(va.ArtifactID) + ":" + strings.ToLower(strings.TrimSpace(va.SHA256))
}

// pendingFinalizeArtifact projects a deferred artifact into the completion
// request WITHOUT a remote location: the asset is committed as deliberately
// local (the AssetTxFinalizer inserts no asset_locations row for an empty
// FileID) and the outbox consumer reconciles the Drive locator later. The
// metadata mirrors the clip.render delivery-intent contract
// (delivery_status=pending + the deferral marker the legacy normalization
// loop and projections key off).
func pendingFinalizeArtifact(va finalization.VerifiedArtifact) finalization.PublishedArtifact {
	metadata := make(map[string]any, len(va.ArtifactMetadata)+2)
	for key, value := range va.ArtifactMetadata {
		metadata[key] = value
	}
	metadata["delivery_status"] = "pending"
	metadata[finalization.DrivePublicationMetadataKey] = finalization.DrivePublicationDeferred
	return finalization.PublishedArtifact{
		ArtifactID:       va.ArtifactID,
		Kind:             va.Kind,
		Filename:         va.Filename,
		MIMEType:         va.MIMEType,
		SizeBytes:        va.SizeBytes,
		SHA256:           va.SHA256,
		SourceVersion:    1,
		Requirement:      va.Requirement,
		IdempotencyKey:   va.IdempotencyKey,
		Description:      va.Description,
		Source:           va.Source,
		ProjectID:        va.ProjectID,
		Language:         va.Language,
		ArtifactMetadata: metadata,
	}
}
