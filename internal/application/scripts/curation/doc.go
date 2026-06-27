// Package curation owns the script-curation flow: the MediaCurator
// orchestrator, its input types (curation_types.go), the
// InsightBuilder (per-channel insight scoring for story pitching),
// the SpecScene validator, the scene-stub scaffold used for
// pre-write visualisation, and the three clip-aware ports
// (clip_evidence_builder, clip_search_port, clip_source_builder).
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
//	clip_evidence_builder.go      (scripts/)  →  (curation/)
//	clip_search_port.go           (scripts/)  →  (curation/)
//	clip_source_builder.go        (scripts/)  →  (curation/)
//	curation_types.go             (scripts/)  →  (curation/)
//	insight_builder.go            (scripts/)  →  (curation/)
//	job_helpers.go                (scripts/)  →  (curation/)
//	media_curator.go              (scripts/)  →  (curation/)
//	scene_stubs.go                (scripts/)  →  (curation/)
//	specscene_validator.go        (scripts/)  →  (curation/)
//
// Companion test files scheduled for migration:
// media_curator_test.go, specscene_validator_test.go. Their
// `package scripts` declarations become `package curation`.
//
// EXPAND phase guarantees (godlike/07 §zero-legacy-policy):
//   - Zero behavior change: no consumer of scripts.X is modified.
//   - Zero alias introduced: this subpackage exposes no shim yet.
//   - Zero new dependency edge: this subpackage imports nothing.
//
// CONTRACT phase (PR-G.4): the `type X = curation.X` aliases
// left in scripts/ during PR-G.2 are removed once every consumer
// has migrated to direct `curation.X` references. External
// consumers today (`internal/app/wire_script.go:97` reads
// `*scripts.MediaCurator`; `internal/api/script/handler_flow.go:71`
// reads `*scripts.MediaCurator`) will be migrated to `*curation.MediaCurator`
// in PR-G.2 alongside the file move.
//
// Cross-reference: architecture/current.yaml#id-21 PR-G.1 EXPAND
// (June 2026) and architecture/ownership.yaml::application_scripts
// ::target_subdirs.
package curation
