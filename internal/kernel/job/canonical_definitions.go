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
//	CanonicalImagesGenerate    images.generate / creator_allowed / multi-image artifacts
//	CanonicalScriptGenerate    script.generate / creator_allowed / heavy artifacts
//
// ── Update discipline ───────────────────────────────────────────────
//
// A 6th canonical family must:
//  1. Append the literal here (this file).
//  2. Append the entry to CanonicalJobDefinitions (this file).
//  3. Append the corresponding typed payload/result structs in
//     internal/capabilities/jobs/queue/codec.go (C2 surface).
//  4. Append the type string constant in internal/kernel/job/job.go.
//  5. Append the registry.go re-export alias in internal/capabilities/jobs/queue/registry_types.go.
//  6. Append the per-family round-trip test in internal/capabilities/jobs/queue/registry_codec_completeness_test.go.
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

// Canonical job-type wire strings.
//
// OWNERSHIP (godlike/06 one owner per fact): THIS package is the single
// owner of every SHARED job-type identity. A job type is a wire fact — it
// is the SQLite jobs.type discriminator and the C3 dispatcher routing key —
// so two declarations of the same literal are a silent dispatch hazard.
//
// Domain and capability packages MUST alias these
// (e.g. script.TypeGenerate = job.TypeScriptGenerate,
// images.TypeImagesGenerate = job.TypeImagesGenerate) instead of
// re-declaring the literal. percheck_identity_ssot fails closed on a
// re-declaration outside this package.
//
// The previous revision declared private canonicalTypeXxx copies "to
// preserve the kernel's stdlib-only import discipline". That claim was
// inaccurate (kernel/asset already imports kernel/media + kernel/digest) and
// the copies had already drifted: TypeScriptGenerateItem held
// "script.generate.item" while the value actually REGISTERED with the C3
// registry is "script.generate_item" (kernel/script.TypeGenerateItem), so
// the preparation planner matched a job type no producer ever emits. One
// owner per fact makes that class of drift impossible.
const (
	TypeScriptGenerate       = "script.generate"
	TypeScriptGenerateItem   = "script.generate_item"
	TypeImagesGenerate       = "images.generate"
	TypeImageGenerateGoogle  = "image.generate.google"
	TypeAssetsResolve        = "assets.resolve"
	TypeMediaClip            = "media.clip"
	TypeAssetTextMaterialize = "asset.text.materialize"

	TypeVoiceoverGenerate     = "voiceover.generate"
	TypeVoiceoverBatch        = "voiceover.batch"
	TypeVoiceoverGenerateItem = "voiceover.generate_item"
	TypeVoiceoverPromo        = "voiceover.promo"

	// The youtube clip-extraction wire string is the historical
	// "youtube_clip.extract" (underscore separator), preserved for
	// back-compat with in-flight SQLite jobs.type rows and owned here so the
	// preparation planner and the C3 registry cannot disagree.
	TypeYouTubeClipExtract = "youtube_clip.extract"
	TypeSubtitleGenerate   = "subtitle.generate"
	TypeCatalogSync        = "catalog.sync"
	TypeSystemCleanup      = "system.cleanup"
	TypeDriveFolderSync    = "drive.folder_sync"

	// TypeClipRender is the canonical job type for the clip.render
	// capability (canonical VeloxEditing-compatible clip
	// post-processing).
	TypeClipRender = "clip.render"
)

// CanonicalScriptGenerate is the canonical JobDefinition for
// script.generate — the workflow entry-point that fans out to
// images.generate + assets.resolve + document.generate downstream.
var CanonicalScriptGenerate = JobDefinition{
	Type:           TypeScriptGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        60 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"script.generate",
		"media.script.generate",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.script.generate.v1", TypeScriptGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.script.generate.v1", TypeScriptGenerate),
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
	Type:           TypeImagesGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "heavy",
	Timeout:        30 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "global_cap_2",
	RequiredCapabilities: []Capability{
		"media.image.generate",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.images.generate.v1", TypeImagesGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.images.generate.v1", TypeImagesGenerate),
	ArtifactPolicy: ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      64,
		MaxTotalBytes:     512 * 1024 * 1024,
	},
}

// CanonicalAssetsResolve is the canonical JobDefinition for
// assets.resolve — pure-data job. Zero ArtifactPolicy:
// ProducesArtifacts=false + RequireManifest=false (pure-data default).
var CanonicalAssetsResolve = JobDefinition{
	Type:           TypeAssetsResolve,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        10 * time.Minute,
	RetryPolicyKey: "max_retries_1",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"qdrant.search",
		"asset.reference",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.assets.resolve.v1", TypeAssetsResolve),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.assets.resolve.v1", TypeAssetsResolve),
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
	Type:           TypeMediaClip,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        30 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"media.clip.extract",
		"drive.write",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.media.clip.v1", TypeMediaClip),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.media.clip.v1", TypeMediaClip),
	// ProducesArtifacts=false: per-item tx owns artifact persistence.
}

// CanonicalJobDefinitions is the slice used by composition-root
// startup wiring (internal/app/registry.go::WireRegistry) to
// register all 5 canonical families into the C3 MutableJobRegistry
// in a single loop. Order is deterministic (alphabetical-by-Type)
// because CreatorCapabilities() derives from this slice via
// sorted-union across RequiredCapabilities.
var CanonicalJobDefinitions = []JobDefinition{
	CanonicalAssetsResolve,  // a
	CanonicalClipRegister,   // c (media.clip — async batch-register)
	CanonicalImagesGenerate, // i
	CanonicalScriptGenerate, // s
}
