// Package postprocess owns the postprocessor registry: every
// per-stage processing pipeline (clip bindings, document creation,
// entity parsing, image generation, metadata fingerprinting,
// persistence propagation, voiceover cascade) plus the central
// PostprocessorRegistry that wires them into a per-job handler
// chain.
//
// PR-G.1 EXPAND (June 2026) — PR-G mega-package split for
// internal/application/scripts (53 production + 20 test files
// above policy.yaml::max_files_per_package=40). The split mirrors
// the existing internal/application/youtube subpackage layout
// (ports/ metadata/ types/ search/ segments/ tagutil/ jobs/
// extraction/) so future PR-G.2 BACKFILL can `git mv` files
// without further restructuring.
//
// Files scheduled for migration into this subpackage in PR-G.2:
//
//	postprocessor_registry.go     (scripts/)  →  (postprocess/)
//	processor_clip_bindings.go    (scripts/)  →  (postprocess/)
//	processor_document.go         (scripts/)  →  (postprocess/)
//	processor_entities.go         (scripts/)  →  (postprocess/)
//	processor_images.go           (scripts/)  →  (postprocess/)
//	processor_metadata.go         (scripts/)  →  (postprocess/)
//	processor_persistence.go      (scripts/)  →  (postprocess/)
//	processor_voiceover.go        (scripts/)  →  (postprocess/)
//
// Companion test files scheduled for migration:
// postprocessor_registry_test.go, processor_persistence_test.go,
// processor_images_voiceover_test.go. Their `package scripts`
// declarations become `package postprocess`.
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet.
//   - Zero new dependency edge: this subpackage imports nothing.
//
// CONTRACT phase (PR-G.4): the `type X = postprocess.X` aliases
// left in scripts/ during PR-G.2 are removed once every consumer
// has migrated to direct `postprocess.X` references.
//
// Note: processor_clip_bindings.go is currently UNTRACKED (working
// tree, June 2026, adds clip-spec-scene document renderer as
// post-Wave-21 PR10 CUTOVER extension). It lands as the first
// file `git mv`-ed into postprocess/ in PR-G.2 BACKFILL so the
// untracked content stabilises before the package-import epoch.
//
// Cross-reference: architecture/current.yaml#id-21 PR-G.1 EXPAND
// (June 2026) and architecture/ownership.yaml::application_scripts
// ::target_subdirs.
package postprocess
