// Package generation owns the script-generation core: the engine,
// pipeline implementation plan-builder, html document renderer,
// generation identity / validator / normalizer / enqueue / job
// orchestrator plus metadata, preset, language helpers, entity
// parser, progress reporter, and clip-evidence / clip-search /
// clip-source-builder ports.
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
//	generation_enqueue.go          (scripts/)  →  (generation/)
//	generation_html.go            (scripts/)  →  (generation/)
//	generation_identity.go        (scripts/)  →  (generation/)
//	generation_job.go             (scripts/)  →  (generation/)
//	generation_normalizer.go      (scripts/)  →  (generation/)
//	generation_plan_builder.go    (scripts/)  →  (generation/)
//	generation_validator.go       (scripts/)  →  (generation/)
//	engine.go                     (scripts/)  →  (generation/)
//	entity_parser.go              (scripts/)  →  (generation/)
//	language_helpers.go           (scripts/)  →  (generation/)
//	metadata.go                   (scripts/)  →  (generation/)
//	pipeline_impl.go              (scripts/)  →  (generation/)
//	preset_resolver.go            (scripts/)  →  (generation/)
//	progress.go                   (scripts/)  →  (generation/)
//
// Companion test files scheduled for migration are the
// `engine_test.go` + `entity_parser_test.go` siblings; their
// `package scripts` declarations become `package generation`.
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet;
//     identities remain in scripts/ until PR-G.2 BACKFILL lands.
//   - Zero new dependency edge: this subpackage imports nothing.
//   - The subpackage compiles to an empty package on `go build`.
//
// CONTRACT phase guarantee (godlike/07 §migration-sequence):
//   - PR-G.4 CONTRACT will remove the `type X = generation.X`
//     back-compat aliases left in scripts/ by PR-G.2 once every
//     consumer has migrated to direct `generation.X` references.
//
// Cross-reference: architecture/current.yaml#id-21 (PR-G.1 EXPAND
// in_progress, June 2026) and architecture/ownership.yaml
// ::application_scripts.target_subdirs (registration of the new
// subpkg as canonical owner).
package generation
