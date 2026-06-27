// Package persistence owns the script-persistence contract layer:
// the canonical ScriptRepository interface (the single writer
// surface consumed by PersistenceProcessor), its concrete
// repository adapter that wires the SQLite repository into the
// engine, and the flow helpers (cache-aware read shortcuts used
// by the generation pipeline + curation flow).
//
// PR-G.1 EXPAND (June 2026) — PR-G mega-package split for
// internal/application/scripts (53 production + 20 test files
// above policy.yaml::max_files_per_package=40). The split mirrors
// the existing internal/application/youtube subpackage layout
// (ports/ metadata/ types/ search/ segments/ tagutil/ jobs/
// extraction/) so future PR-G.2 BACKFILL can `git mv` files
// without further structuring.
//
// Files scheduled for migration into this subpackage in PR-G.2:
//
//	flow_helpers.go               (scripts/)  →  (persistence/)
//	repository.go                 (scripts/)  →  (persistence/)
//	repository_adapter.go         (scripts/)  →  (persistence/)
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet.
//   - Zero new dependency edge: this subpackage imports nothing.
//
// CONTRACT phase (PR-G.4): the `type X = persistence.X` aliases
// left in scripts/ during PR-G.2 (e.g. `ScriptRepository`,
// `ScriptRecord`, `ScriptSectionRecord`, `ScriptStockMatchRecord`)
// are removed once every consumer has migrated to direct
// `persistence.X` references.
//
// Note: this subpackage's PR-G.2 BACKFILL keeps zero infra
// dependency: the actual SQLite repository lives in
// `internal/infrastructure/database/sqlite/scripts/` and
// satisfies the canonical `*persistence.ScriptRepository`
// interface per godlike/06 §"One owner per fact" — the
// script/persistence package owns the contract surface, the
// infrastructure/sqlite/scripts package owns the concrete
// adapter. The two `package scripts` declarations (one in
// `internal/application/scripts/`, one in
// `internal/infrastructure/database/sqlite/scripts/`) are
// DIFFERENT PACKAGES per Go's package-identity rule
// (directory_path + package_name tuple). Wave 20 CHECK 5
// documents this asymmetry as a known cross-directory
// same-package-NAME false-positive (Wave 20 transitional
// baseline owner: qdrant-hygiene work stream, deadline
// 2026-07-25).
//
// Cross-reference: architecture/current.yaml#id-21 PR-G.1 EXPAND
// (June 2026) and architecture/ownership.yaml::application_scripts
// ::target_subdirs.
package persistence
