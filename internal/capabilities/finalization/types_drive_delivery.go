// types_drive_delivery.go — the durable Drive-delivery intent for finalize
// artifacts whose publication is deferred off the completion critical path
// (L10). One canonical type per godlike/06 SSOT.
//
// Contract family, mirroring clip.render.drive_delivery.requested.v1: the
// producer commits the media asset WITHOUT a Drive location and emits this
// event in the same completion transaction; the outbox consumer performs the
// slow external upload later and reconciles the Drive identity against the
// canonical asset. The artifact bytes live in the local content-addressed
// object store (H1) under StorageKey, so the intent stays deliverable long
// after the per-job workspace has been deleted.
package finalization

import (
	"fmt"
	"strings"
)

const (
	// EventFinalizeDriveDeliveryRequested is the outbox event type drained by
	// the finalize Drive-delivery consumer.
	EventFinalizeDriveDeliveryRequested = "finalize.drive_delivery.requested.v1"

	// FinalizeDriveDeliverySchemaVersion is the payload schema version.
	FinalizeDriveDeliverySchemaVersion = "finalize.drive_delivery.v1"

	// DrivePublicationMetadataKey is the optional producer override on the
	// artifact metadata map: "sync" forces the synchronous publication path,
	// "deferred" forces the asynchronous one. Anything else (or the key's
	// absence) keeps the broker's own scope decision.
	DrivePublicationMetadataKey = "drive_publication"

	// DrivePublicationSync / DrivePublicationDeferred are the two accepted
	// values of DrivePublicationMetadataKey.
	DrivePublicationSync     = "sync"
	DrivePublicationDeferred = "deferred"
)

// FinalizeDriveDeliveryRequest is the durable outbox payload for one deferred
// finalize artifact publication. It carries no credentials and is idempotent
// by ArtifactID + ContentHash + StorageKey: a retried delivery re-publishes
// through the artifact's own IdempotencyKey and never creates a second Drive
// object.
type FinalizeDriveDeliveryRequest struct {
	SchemaVersion string `json:"schema_version"`
	// JobID is the job whose completion deferred this publication.
	JobID string `json:"job_id"`
	// AssetID is the canonical media identity committed at completion. The
	// consumer reconciles the Drive locator against it.
	AssetID string `json:"asset_id"`
	// ArtifactID, Kind, Filename, MIMEType, SHA256, SizeBytes, IdempotencyKey
	// and Requirement rebuild the canonical VerifiedArtifact envelope the
	// synchronous path publishes with (byte-identical publication semantics —
	// only the timing changes).
	ArtifactID     string              `json:"artifact_id"`
	Kind           ArtifactKind        `json:"kind"`
	Filename       string              `json:"filename"`
	MIMEType       string              `json:"mime_type"`
	SHA256         string              `json:"sha256"`
	SizeBytes      int64               `json:"size_bytes"`
	IdempotencyKey string              `json:"idempotency_key"`
	Requirement    ArtifactRequirement `json:"requirement"`
	// StorageKey is the certified content-addressed locator (CAS SHA-256) of
	// the artifact bytes. The consumer resolves it to a local object path and
	// the publisher streams from there — the same bytes the completion already
	// hashed and certified.
	StorageKey string `json:"storage_key"`
	// The optional routing/identity fields, preserved verbatim so the deferred
	// publication resolves the same Drive tree as the synchronous one.
	Description        string         `json:"description,omitempty"`
	Source             string         `json:"source,omitempty"`
	ProjectID          string         `json:"project_id,omitempty"`
	Language           string         `json:"language,omitempty"`
	DriveSubpath       []string       `json:"drive_subpath,omitempty"`
	ArtifactMetadata   map[string]any `json:"artifact_metadata,omitempty"`
	ResolvedFolderID   string         `json:"resolved_folder_id,omitempty"`
	RootFolderResolved bool           `json:"root_folder_resolved,omitempty"`
}

// Validate fail-closes an incomplete delivery intent before any upload runs.
func (r FinalizeDriveDeliveryRequest) Validate() error {
	if strings.TrimSpace(r.SchemaVersion) != FinalizeDriveDeliverySchemaVersion {
		return fmt.Errorf("finalize drive delivery: schema_version %q is not %q", r.SchemaVersion, FinalizeDriveDeliverySchemaVersion)
	}
	if strings.TrimSpace(r.JobID) == "" || strings.TrimSpace(r.AssetID) == "" {
		return fmt.Errorf("finalize drive delivery: job_id and asset_id are required")
	}
	if strings.TrimSpace(r.ArtifactID) == "" || strings.TrimSpace(r.Filename) == "" {
		return fmt.Errorf("finalize drive delivery: artifact_id and filename are required")
	}
	if r.Kind == "" {
		return fmt.Errorf("finalize drive delivery: artifact %s has no kind", r.ArtifactID)
	}
	if strings.TrimSpace(r.StorageKey) == "" {
		return fmt.Errorf("finalize drive delivery: artifact %s has no storage_key", r.ArtifactID)
	}
	if strings.TrimSpace(r.SHA256) == "" || r.SizeBytes <= 0 {
		return fmt.Errorf("finalize drive delivery: artifact %s has no certified digest/size", r.ArtifactID)
	}
	// The content-addressed locator IS the certified digest: the object store
	// keys bytes by their SHA-256 and the completion re-verified the staged
	// object against the manifest digest before emitting this intent. An intent
	// whose locator and digest disagree can never describe the certified bytes
	// and fails closed before any upload.
	if !strings.EqualFold(strings.TrimSpace(r.StorageKey), strings.TrimSpace(r.SHA256)) {
		return fmt.Errorf("finalize drive delivery: artifact %s storage_key %q does not match the certified digest %q", r.ArtifactID, r.StorageKey, r.SHA256)
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return fmt.Errorf("finalize drive delivery: artifact %s has no idempotency key", r.ArtifactID)
	}
	if !r.Requirement.Valid() {
		return fmt.Errorf("finalize drive delivery: artifact %s has Requirement=%s", r.ArtifactID, r.Requirement)
	}
	return nil
}

// ToVerifiedArtifact rebuilds the canonical envelope the synchronous
// publication path consumes. LocalPath is empty here: the consumer resolves
// StorageKey to the certified object-store copy before calling the publisher,
// so the two paths publish exactly the same bytes.
func (r FinalizeDriveDeliveryRequest) ToVerifiedArtifact() VerifiedArtifact {
	return VerifiedArtifact{
		ArtifactID:         r.ArtifactID,
		Kind:               r.Kind,
		Filename:           r.Filename,
		MIMEType:           r.MIMEType,
		SHA256:             r.SHA256,
		SizeBytes:          r.SizeBytes,
		SourceVersion:      1,
		Requirement:        r.Requirement,
		IdempotencyKey:     r.IdempotencyKey,
		Description:        r.Description,
		Source:             r.Source,
		ProjectID:          r.ProjectID,
		Language:           r.Language,
		DriveSubpath:       r.DriveSubpath,
		ArtifactMetadata:   r.ArtifactMetadata,
		ResolvedFolderID:   r.ResolvedFolderID,
		RootFolderResolved: r.RootFolderResolved,
	}
}

// NewFinalizeDriveDeliveryRequest stamps the schema version on a request.
func NewFinalizeDriveDeliveryRequest(req FinalizeDriveDeliveryRequest) FinalizeDriveDeliveryRequest {
	req.SchemaVersion = FinalizeDriveDeliverySchemaVersion
	return req
}
