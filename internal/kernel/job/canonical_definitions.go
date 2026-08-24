// Package job — canonical_definitions.go (P0 Commit 3, July 2026).
//
// Exported canonical JobDefinition literals for the 5 workflow
// job families. These live OUTSIDE job_definition_test.go so the
// composition root (internal/app/registry.go::WireRegistry) can
// reference them at startup wiring time — Go's `_test.go` files
// are not importable, so promoting the canonical literals to a
// non-test source file is the canonical way to share them.
//
// ── Increment table ─────────────────────────────────────────────────
//
//	CanonicalAssetsResolve     assets.resolve  / creator_allowed / pure-data (zero Artifacts)
//	CanonicalClipRegister      media.clip      / creator_allowed / pure-data (zero Artifacts)
//	CanonicalDocumentGenerate  document.generate / creator_allowed / single-DOCX artifact
//	CanonicalImagesGenerate    images.generate / creator_allowed / multi-image artifacts
//	CanonicalScriptGenerate    script.generate / creator_allowed / heavy artifacts
//
// ── Update discipline ───────────────────────────────────────────────
//
// A 6th canonical family must:
//  1. Append the literal here (this file).
//  2. Append the entry to CanonicalJobDefinitions (this file).
//  3. Append the corresponding typed payload/result structs in
//     internal/application/jobs/codec.go (C2 surface).
//  4. Append the type string constant in internal/domain/job/job.go.
//  5. Append the registry.go re-export alias in internal/application/jobs/registry_types.go.
//  6. Append the per-family round-trip test in internal/application/jobs/registry_codec_completeness_test.go.
//  7. Append the job type to workflowRefs in c3ValidateRuntimeGraph (internal/app/registry.go).
//
// Handler binding is the responsibility of the composition root
// (c3ValidateRuntimeGraph) — JobDefinition declares the operational
// parameters only. HandlerKey was removed in PR-AUDIT-7 (July 2026)
// because it was never consumed: c3ValidateRuntimeGraph binds via
// def.Type, not a separate indirection key.
//
// The compile-time assertions in registry_test.go + startup_validator_test.go
// (next-package-internal) will fail immediately if any of (1)–(7) is removed
// while keeping the canonical literal in this file.
//
// ── Layering ─────────────────────────────────────────────────────────
//
// Standard-library imports only. No application/infrastructure imports,
// per godlike/06 §Database rules.
package job

import (
	"time"
)

// Canonical type string literals. These mirror the capability-owned
// constants in internal/domain/<capability>/job_types.go. Keeping the
// literal values here (rather than importing the domain packages)
// preserves the kernel's stdlib-only import discipline while still
// giving the composition root a stable set of canonical JobDefinitions.
const (
	canonicalTypeScriptGenerate   = "script.generate"
	canonicalTypeImagesGenerate   = "images.generate"
	canonicalTypeDocumentGenerate = "document.generate"
	canonicalTypeAssetsResolve    = "assets.resolve"
	canonicalTypeClipRegister     = "media.clip"

	TypeVoiceoverGenerate     = "voiceover.generate"
	TypeVoiceoverBatch        = "voiceover.batch"
	TypeVoiceoverGenerateItem = "voiceover.generate_item"
	TypeVoiceoverPromo        = "voiceover.promo"

	// Re-exported canonical job type constants previously in
	// internal/domain/job (deleted July 2026). Callers that
	// imported `job "internal/domain/job"` can now import
	// `job "internal/kernel/job"` unmodified.
	TypeYouTubeClipExtract   = "youtube.clip.extract"
	TypeScriptGenerate       = "script.generate"
	TypeScriptGenerateItem   = "script.generate.item"
	TypeAssetTextMaterialize = "asset.text.materialize"
	TypeBooksProcess         = "books.process"
	TypeLessonsProcess       = "lessons.process"
	TypeSubtitleGenerate     = "subtitle.generate"
	TypeCatalogSync          = "catalog.sync"
	TypeSystemCleanup        = "system.cleanup"
	TypeDriveFolderSync      = "drive.folder_sync"

	// TypeClipRender is the canonical job type for the clip.render
	// capability (canonical VeloxEditing-compatible clip
	// post-processing). The literal lives here (kernel owns shared
	// job-type identities) and is re-exported by the owning capability
	// (internal/capabilities/cliprender) + the application registry.
	TypeClipRender = "clip.render"
)

// CanonicalScriptGenerate is the canonical JobDefinition for
// script.generate — the workflow entry-point that fans out to
// images.generate + assets.resolve + document.generate downstream.
var CanonicalScriptGenerate = JobDefinition{
	Type:           canonicalTypeScriptGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        60 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"script.generate",
		"media.script.generate",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.script.generate.v1", canonicalTypeScriptGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.script.generate.v1", canonicalTypeScriptGenerate),
	ArtifactPolicy: ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      16,
		MaxTotalBytes:     256 * 1024 * 1024,
	},
}

// CanonicalImagesGenerate is the canonical JobDefinition for
// images.generate — heavy queue, multi-image artifacts, capacity-2
// concurrency.
var CanonicalImagesGenerate = JobDefinition{
	Type:           canonicalTypeImagesGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "heavy",
	Timeout:        30 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "global_cap_2",
	RequiredCapabilities: []Capability{
		"media.image.generate",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.images.generate.v1", canonicalTypeImagesGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.images.generate.v1", canonicalTypeImagesGenerate),
	ArtifactPolicy: ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      64,
		MaxTotalBytes:     512 * 1024 * 1024,
	},
}

// CanonicalDocumentGenerate is the canonical JobDefinition for
// document.generate — default queue, single-DOCX artifact.
var CanonicalDocumentGenerate = JobDefinition{
	Type:           canonicalTypeDocumentGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        15 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"doc.create",
		"drive.write",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.document.generate.v1", canonicalTypeDocumentGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.document.generate.v1", canonicalTypeDocumentGenerate),
	ArtifactPolicy: ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      8,
		MaxTotalBytes:     64 * 1024 * 1024,
	},
}

// CanonicalAssetsResolve is the canonical JobDefinition for
// assets.resolve — pure-data job. Zero ArtifactPolicy:
// ProducesArtifacts=false + RequireManifest=false (pure-data default).
var CanonicalAssetsResolve = JobDefinition{
	Type:           canonicalTypeAssetsResolve,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        10 * time.Minute,
	RetryPolicyKey: "max_retries_1",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"qdrant.search",
		"asset.reference",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.assets.resolve.v1", canonicalTypeAssetsResolve),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.assets.resolve.v1", canonicalTypeAssetsResolve),
	// Pure-data job: zero ArtifactPolicy left implicit.
}

// CanonicalClipRegister is the canonical JobDefinition for
// media.clip — async clip registration from the batch-register
// endpoint. Each clip becomes an independent job; yt-dlp + cut +
// Drive upload + DB write happen off the request thread.
//
// ProducesArtifacts=false because the registration pipeline persists
// its own media_assets row + outbox events inside a per-clip tx
// (mirror of youtube_clip.extract); the broker's legacy Complete is
// the canonical mark-SUCCEEDED seam.
var CanonicalClipRegister = JobDefinition{
	Type:           canonicalTypeClipRegister,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        30 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"media.clip.extract",
		"drive.write",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.media.clip.v1", canonicalTypeClipRegister),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.media.clip.v1", canonicalTypeClipRegister),
	// ProducesArtifacts=false: per-item tx owns artifact persistence.
}

// CanonicalJobDefinitions is the slice used by composition-root
// startup wiring (internal/app/registry.go::WireRegistry) to
// register all 5 canonical families into the C3 MutableJobRegistry
// in a single loop. Order is deterministic (alphabetical-by-Type)
// because CreatorCapabilities() derives from this slice via
// sorted-union across RequiredCapabilities.
var CanonicalJobDefinitions = []JobDefinition{
	CanonicalAssetsResolve,    // a
	CanonicalClipRegister,     // c (media.clip — async batch-register)
	CanonicalDocumentGenerate, // d
	CanonicalImagesGenerate,   // i
	CanonicalScriptGenerate,   // s
}
