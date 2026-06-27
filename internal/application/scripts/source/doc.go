// Package source owns the source-resolution registry: every
// per-source pipeline that resolves a script-generation input
// (catalog browsing, clip-aware matching, curate flows, search
// dispatching, text preprocessing) plus the central SourceRegistry
// that owns id-resolution w.r.t. the asset lifecycle pipeline.
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
//	source_registry.go            (scripts/)  →  (source/)
//	source_resolver_catalog.go    (scripts/)  →  (source/)
//	source_resolver_clips.go      (scripts/)  →  (source/)
//	source_resolver_curate.go     (scripts/)  →  (source/)
//	source_resolver_search.go     (scripts/)  →  (source/)
//	source_resolver_shared.go     (scripts/)  →  (source/)
//	source_resolver_text.go       (scripts/)  →  (source/)
//
// Companion test files scheduled for migration:
// source_registry_test.go, source_resolver_curate_test.go.
// Their `package scripts` declarations become `package source`.
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet.
//   - Zero new dependency edge: this subpackage imports nothing.
//
// CONTRACT phase (PR-G.4): the `type X = source.X` aliases
// left in scripts/ during PR-G.2 are removed once every consumer
// has migrated to direct `source.X` references.
//
// Cross-package import rule: this subpackage will own the
// `SourceRegistry` identity in PR-G.2. The composition root
// currently imports `*scripts.SourceRegistry` from
// `internal/app/wire_script.go` and the test fixture in
// `internal/application/scripts/source_registry_test.go::29`
// imports `*scripts.SourceRegistry` directly. Both
// migration paths are additive during BACKFILL (alias
// `scripts.SourceRegistry = source.SourceRegistry`) so the
// existing import stays green; the alias is removed in PR-G.4
// once consumers are migrated.
//
// Cross-reference: architecture/current.yaml#id-21 PR-G.1 EXPAND
// (June 2026) and architecture/ownership.yaml::application_scripts
// ::target_subdirs.
package source
