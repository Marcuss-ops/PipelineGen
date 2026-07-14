// Package job — kernel alias re-exports (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical definitions for artifact manifests and worker command
// types now live in internal/kernel/job. This file re-exports them as
// transparent type aliases so existing callers that import
// internal/domain/job continue to compile unchanged during the
// Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Artifact manifest types and constants ─────────────────────────────

const (
	SchemaVersionArtifactManifestV1 = kerneljob.SchemaVersionArtifactManifestV1
	ManifestKey                     = kerneljob.ManifestKey

	ArtifactKindScriptJSON       = kerneljob.ArtifactKindScriptJSON
	ArtifactKindScriptText       = kerneljob.ArtifactKindScriptText
	ArtifactKindScenes           = kerneljob.ArtifactKindScenes
	ArtifactKindMetadata         = kerneljob.ArtifactKindMetadata
	ArtifactKindEntities         = kerneljob.ArtifactKindEntities
	ArtifactKindVoiceover        = kerneljob.ArtifactKindVoiceover
	ArtifactKindImage            = kerneljob.ArtifactKindImage
	ArtifactKindClipBindings     = kerneljob.ArtifactKindClipBindings
	ArtifactKindArtifactManifest = kerneljob.ArtifactKindArtifactManifest
	ArtifactKindPDF              = kerneljob.ArtifactKindPDF
	ArtifactKindMarkdown         = kerneljob.ArtifactKindMarkdown

	StatusReady   = kerneljob.StatusReady
	StatusSkipped = kerneljob.StatusSkipped
)

type (
	ArtifactManifest         = kerneljob.ArtifactManifest
	Artifact                 = kerneljob.Artifact
	RemoteAsset              = kerneljob.RemoteAsset
	RemoteAssetIDAdapter     = kerneljob.RemoteAssetIDAdapter
	UploadedManifest         = kerneljob.UploadedManifest
	RemoteArtifactManifest   = kerneljob.RemoteArtifactManifest
	ArtifactRequirement      = kerneljob.ArtifactRequirement
	UploadedArtifact         = kerneljob.UploadedArtifact
	RemoteArtifact           = kerneljob.RemoteArtifact
)

const (
	ArtifactRequirementInvalid  = kerneljob.ArtifactRequirementInvalid
	ArtifactRequirementRequired = kerneljob.ArtifactRequirementRequired
	ArtifactRequirementOptional = kerneljob.ArtifactRequirementOptional
)

var (
	ErrRemoteSchemaVersionUnsupported = kerneljob.ErrRemoteSchemaVersionUnsupported
	Decode                            = kerneljob.Decode
	ComputeSHA256                     = kerneljob.ComputeSHA256
)

// ── Artifact manifest typed-error sentinels ────────────────────────────

var (
	ErrArtifactManifestMissing  = kerneljob.ErrArtifactManifestMissing
	ErrArtifactManifestInvalid  = kerneljob.ErrArtifactManifestInvalid
	ErrRequiredArtifactMissing  = kerneljob.ErrRequiredArtifactMissing
)

// ── Worker command types ──────────────────────────────────────────────

type (
	WorkerSession          = kerneljob.WorkerSession
	WorkerCapabilities     = kerneljob.WorkerCapabilities
	RegisterWorkerCommand  = kerneljob.RegisterWorkerCommand
	ClaimCommand           = kerneljob.ClaimCommand
	HeartbeatCommand       = kerneljob.HeartbeatCommand
	RenewCommand           = kerneljob.RenewCommand
	ProgressCommand        = kerneljob.ProgressCommand
	CompleteCommand        = kerneljob.CompleteCommand
	FailCommand            = kerneljob.FailCommand
	WorkerHardwareStats    = kerneljob.WorkerHardwareStats
)
