// Package job — canonical_definitions.go (P0 Commit 3, July 2026).
//
// Exported canonical JobDefinition literals for the 4 workflow
// job families. These live OUTSIDE job_definition_test.go so the
// composition root (internal/app/registry.go::WireRegistry) can
// reference them at startup wiring time — Go's `_test.go` files
// are not importable, so promoting the canonical literals to a
// non-test source file is the canonical way to share them.
//
// ── Increment table ─────────────────────────────────────────────────
//
//	CanonicalScriptGenerate    script.generate / creator_allowed / heavy artifacts
//	CanonicalImagesGenerate    images.generate / creator_allowed / multi-image artifacts
//	CanonicalDocumentGenerate  document.generate / creator_allowed / single-DOCX artifact
//	CanonicalAssetsResolve     assets.resolve  / creator_allowed / pure-data (zero Artifacts)
//
// ── Update discipline ───────────────────────────────────────────────
//
// A 5th canonical family must:
//  1. Append the literal here (this file).
//  2. Append the entry to CanonicalJobDefinitions (this file).
//  3. Append the corresponding typed payload/result structs in
//     internal/application/jobs/codec.go (C2 surface).
//  4. Append the type string constant in internal/domain/job/job.go.
//  5. Append the registry.go re-export alias in internal/application/jobs/registry.go.
//  6. Append the per-family round-trip test in internal/application/jobs/registry_codec_completeness_test.go.
//
// The compile-time assertions in registry_test.go + startup_validator_test.go
// (next-package-internal) will fail immediately if any of (1)–(6) is removed
// while keeping the canonical literal in this file.
//
// ── Layering ─────────────────────────────────────────────────────────
//
// Standard-library imports only. No application/infrastructure imports,
// per godlike/06 §Database rules.
package job

import "time"

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
	HandlerKey: "script.generate.handler",
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
	HandlerKey: "images.generate.handler",
}

// CanonicalDocumentGenerate is the canonical JobDefinition for
// document.generate — default queue, single-DOCX artifact.
var CanonicalDocumentGenerate = JobDefinition{
	Type:           TypeDocumentGenerate,
	ExecutionClass: ExecutionCreatorAllowed,
	Queue:          "default",
	Timeout:        15 * time.Minute,
	RetryPolicyKey: "max_retries_2",
	ConcurrencyKey: "single_global",
	RequiredCapabilities: []Capability{
		"doc.create",
		"drive.write",
	},
	PayloadCodec: NewCodecDescriptorMarker("pipelinegen.payload.document.generate.v1", TypeDocumentGenerate),
	ResultCodec:  NewCodecDescriptorMarker("pipelinegen.result.document.generate.v1", TypeDocumentGenerate),
	ArtifactPolicy: ArtifactPolicy{
		ProducesArtifacts: true,
		RequireManifest:   true,
		MaxArtifacts:      8,
		MaxTotalBytes:     64 * 1024 * 1024,
	},
	HandlerKey: "document.generate.handler",
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
	HandlerKey: "assets.resolve.handler",
}

// CanonicalJobDefinitions is the slice used by composition-root
// startup wiring (internal/app/registry.go::WireRegistry) to
// register all 4 canonical families into the C3 MutableJobRegistry
// in a single loop. Order is deterministic (alphabetical-by-Type)
// because CreatorCapabilities() derives from this slice via
// sorted-union across RequiredCapabilities.
var CanonicalJobDefinitions = []JobDefinition{
	CanonicalAssetsResolve,    // a
	CanonicalDocumentGenerate, // d
	CanonicalImagesGenerate,   // i
	CanonicalScriptGenerate,   // s
}
