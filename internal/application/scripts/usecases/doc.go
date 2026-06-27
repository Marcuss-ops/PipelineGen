// Package usecases owns the entry-point use-case orchestrators of
// the script-generation capability: every public /api/script/* and
// pipeline-facing public method expressed as a single
// composable function rather than a method on a shared god-struct.
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
//	cache_eviction_usecase.go     (scripts/)  →  (usecases/)
//	documents_usecase.go          (scripts/)  →  (usecases/)
//	generate_many_usecase.go      (scripts/)  →  (usecases/)
//	generate_one_usecase.go       (scripts/)  →  (usecases/)
//	postgen_usecase.go            (scripts/)  →  (usecases/)
//	prewarm_usecase.go            (scripts/)  →  (usecases/)
//	scene_builder_usecase.go      (scripts/)  →  (usecases/)
//	section_regen.go              (scripts/)  →  (usecases/)
//	semaphore_usecase.go          (scripts/)  →  (usecases/)
//
// Companion test files scheduled for migration:
// documents_usecase_test.go, generate_many_usecase_test.go,
// prewarm_usecase_test.go, scriptflow_usecase_test.go,
// semaphore_usecase_test.go. Their `package scripts` declarations
// become `package usecases`.
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet.
//   - Zero new dependency edge: this subpackage imports nothing.
//
// CONTRACT phase (PR-G.4): the `type X = usecases.X` back-compat
// aliases left in scripts/ during PR-G.2 are removed once every
// consumer has migrated to direct `usecases.X` references.
//
// Goal: each `*_usecase.go` file becomes the SOLE entry point
// of its capability slice (no shared struct with 40+ positional
// args), restoring policy.yaml::max_constructor_deps=8 compliance
// for the scripts capability slice.
//
// Cross-reference: architecture/current.yaml#id-21 PR-G.1 EXPAND
// (June 2026) and architecture/ownership.yaml::application_scripts
// ::target_subdirs.
package usecases
