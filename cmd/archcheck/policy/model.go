// Package policy — archcheck policy model.
//
// policy/model.go owns the typed shape of `architecture/policy.yaml` plus
// three forward-looking types (Constraint, OwnerRef, Rule) that the
// Phase 1+ rule-family dispatch will use. The package is `policy`
// (separate from `package main` of cmd/archcheck) so the policy
// subdirectory holds a focused concern: "what does the on-disk policy
// look like in Go?" — independent of the scan functions that consume
// it.
//
// Phase 0 usage: only `Policy` is instantiated by `Load()`; the three
// forward-looking types (Constraint, OwnerRef, Rule) are declared so
// the policy package is forward-compatible with the PR4 runner
// refactor. They carry no behavior today.
//
// Cross-references:
//   - architecture/policy.yaml: the on-disk format Load() parses
//   - cmd/archcheck/main.go: the caller that loads the policy and
//     passes it to the scan* functions
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md: the
//     canonical rule-family definitions the Constraint/Rule types
//     will eventually encode
package policy

// Policy is the parsed subset of architecture/policy.yaml used by the
// scan. Unknown keys are ignored (forward-compat). Lists are parsed from
// comma-separated values; multi-line YAML lists (e.g. known_grandfathered,
// stale_prose_paths) are doc-only and not consumed for enforcement in
// Phase 0.
//
// `LegacyInternalRoots` is the current (Phase 0) layout — `internal/{api,
// app, application, domain, infrastructure}`. `TargetInternalRoots` is
// the migration target — `internal/{app, kernel, capabilities,
// platform}`. The scan reports any first-level `internal/<x>` not in
// either list as an unknown-root warning, so the migration progress is
// visible in the JSON report over time.
//
// `Capabilities` and `PlatformSubzones` are targets for Phase 1+
// enforcement of "expected zones exist" rules. For Phase 0 they are
// declared in the policy so the report snapshot is forward-compatible,
// but no enforcement logic runs against them yet.
type Policy struct {
	MaxFilesPerPackage int
	MaxLinesPerFile    int
	CmdMainMaxLines    int
	// MaxLinesPerFileStrict is the soft-warning cap on per-file LOC
	// (forward-prevention gate, godlike/08 §"Mandatory checks").
	// Distinct from MaxLinesPerFile=1000: this is a "pre-flight"
	// check that signals overload before crossing the 1000-LOC
	// hard line, giving operators a chance to split proactively.
	// Files in pol.MaxLinesStrictAllowlist are exempt (per godlike/07
	// §"Temporary deprecation record" — each allowlist entry must
	// carry owner + deadline + removal_trigger per godlike/08
	// zero-baseline rule). Setting this to 0 opts the rule family out.
	MaxLinesPerFileStrict int
	// MaxLinesStrictAllowlist is the policy path (relative to root)
	// of the plain-text allowlist for MaxLinesPerFileStrict. Format:
	// one repo-root-relative forward-slashed path per line; `#` comments
	// and blank lines are ignored. Empty string opts out (the rule
	// is unenforced for the un-allowlisted population). Forward-
	// compatible with the existing permit-only model used by
	// admin-sql-allowlist.txt and duplicate-types-allowlist.txt.
	MaxLinesStrictAllowlist string
	MaxConstructorDeps      int
	MaxStructDeps           int

	// MaxClipIngestPipelineFields is the per-struct exception to the global
	// MaxStructDeps=8 cap, lifted to 9 for the canonical 9-component
	// ClipIngestPipeline surface (PR-CLIPINGEST-PIPELINE step 8, July 2026).
	// Mirrors the existing image-asset pattern (well-established per-struct
	// knob escape hatch). The 9-component shape is the SOLE canonical owner at
	// internal/application/assets/ingest/clip_ingest_pipeline.go (godlike/06
	// SSOT); the percheck_clip_ingest_pipeline_canonical_1 forward-prevention
	// gate enforces this at the type level. Operators who add a new
	// canonical surface with > 8 fields declare a matching `_per_struct_field`
	// knob here, NOT a global cap raise.
	MaxClipIngestPipelineFields int
	ForbiddenTopLevelDirs       []string
	KernelSubzones              []string
	Capabilities                []string
	PlatformSubzones            []string
	LegacyInternalRoots         []string
	TargetInternalRoots         []string
	// DataOwnershipDoc is the path (relative to root) of the canonical
	// data/config ownership document whose authority the rule family
	// scanOwnershipDoc enforces. Empty string opts out (Phase 0 only;
	// Phase 1+ may treat absence as a violation). See docs/architecture/
	// godlike/06_DATA_AND_CONFIG_OWNERSHIP.md for the contract.
	DataOwnershipDoc string
	// LegacyPolicyDoc, CIGatesDoc, AgentPlaybookDoc, RemovalDoc mirror
	// the DataOwnershipDoc field for the four canonical-promoted Phase-1
	// docs (07, 08, 11, 13 of the godlike/ program). Each is enforced by
	// the corresponding scan<X>Doc() function. Empty string opts out
	// individually (Phase 0 only).
	LegacyPolicyDoc  string
	CIGatesDoc       string
	AgentPlaybookDoc string
	RemovalDoc       string
	// KnownGrandfathered is exposed in the report header for traceability.
	KnownGrandfathered []string
	// StaleProseStems is the list of pre-Wave-16 path stems that must
	// not appear as bare prose references in *.go source files (a
	// "bare" reference is one whose stem is NOT followed by a literal
	// '.', e.g. `module_jobs_test.go` or `compose_images bundle` —
	// distinct from `compose_images.go` which is already covered by
	// the user-regex gate). Enforced by scanStaleProsePaths. Empty
	// list opts out (Phase 0 only). See architecture/policy.yaml::
	// stale_prose_paths for the comment block + severity ladder.
	StaleProseStems []string
	// HardGates is the canonical Wave-22 Phase-N hard-gate list
	// (godlike/08 evolution PR2). DefaultChecks emit Rule IDs
	// (string-equality matched against report.Violation.Rule);
	// any violation whose Rule is in this list ALWAYS returns
	// ExitViolations from cmd/archcheck/runner.go, regardless of
	// --strict. SSOT for the Wave-22 gate promotion. Bypassing
	// requires an SSOT-marker explicit demotion via
	// architecture/current.yaml (no in-tree override).
	// Empty list opts out (Phase 0 default behaviour).
	HardGates []string
}

// Constraint is a forward-looking single-rule threshold description.
// Phase 1+ rule families (file_size, pkg_size, constructor_deps,
// kernel_split_hint, etc.) will dispatch on Constraint values
// declared in policy.yaml (or a side-car file). Phase 0 declares the
// shape so the policy package is forward-compatible; no scan function
// reads a Constraint today.
//
// The fields map directly to the per-rule knobs the scan functions
// already read off the Policy struct — Constraint is the typed
// generalization of those scalar fields + a Severity tag. Promotion
// to a "policy entry per rule" model is a separate PR in the
// Godlike-08 CI-gates evolution track.
type Constraint struct {
	// Name is the human-readable rule id (e.g. "max_lines_per_file",
	// "kernel_split_hint"). Matches the `MatchedRule` field on a
	// report.Violation when the rule fires.
	Name string
	// Severity is the canonical severity the rule emits on violation
	// ("info" / "warn" / "error"). The corresponding
	// report.Severity constants (report.SeverityInfo / Warn / Error)
	// are the typed string values; Constraint.Severity is a string
	// here to avoid a cyclic import between policy/ and report/.
	Severity string
	// Threshold is the numeric limit the rule enforces (e.g. 500
	// for max_lines_per_file). Zero means the rule is opt-out.
	Threshold int
	// Pattern is the optional regex / glob / path the rule matches
	// against (e.g. `module_jobs` for stale_prose_paths, `models`
	// for forbidden_top_level_dirs). Empty when the rule has no
	// per-target pattern.
	Pattern string
}

// OwnerRef is a forward-looking owner-pointer for a path stem or
// domain concept. Phase 1+ ownership-rule-family scans will emit
// "no owner for X" findings by comparing the codebase's `// owns:`
// comments (or filesystem layout) against the OwnerRef.Source
// registry. Phase 0 declares the shape; the scan family ships in
// the same Godlike-08 promotion track as Constraint.
//
// Cross-reference: architecture/ownership.generated.yaml is the
// canonical owner registry — its entries map 1:1 to OwnerRef rows
// when the policy-driven family lands.
type OwnerRef struct {
	// Path is the codebase-relative path the owner is responsible
	// for (e.g. "internal/application/scripts", "pkg/defaults",
	// "cmd/archcheck"). Slash-separated (forward-slash on all OSes
	// per report.Violation.Directory's convention).
	Path string
	// Owner is the canonical owner identifier — typically a Go
	// package path, a YAML section, or a free-form team/workstream
	// label (e.g. "internal/app", "qdrant-hygiene work stream",
	// "scripting"). Owners must be unique across the registry.
	Owner string
	// Source records where the owner declaration came from. Three
	// values: "ownership.yaml" (the canonical architecture/owner-
	// ship.generated.yaml registry), "policy.yaml" (declared as
	// a side-car in policy.yaml under `owners:` — Phase 1+ only),
	// "explicit" (operator-supplied via CLI flag for ad-hoc
	// overrides during incident response).
	Source string
}

// Rule is a forward-looking rule-family definition. The PR4 runner
// (cmd/archcheck/runner.go) will hold a slice of Rule values and
// dispatch them in order. The Check field is the closure that runs
// the rule against the project root + policy; it returns the list
// of report.Violation findings the rule produces (zero or more).
//
// Phase 0 declares the shape so the policy package compiles
// forward-compatibly with PR4. The Check closure is not invoked
// anywhere in Phase 0; the scan* functions in main.go are the
// Phase 0 equivalent and are migrated to Rule.Check values in PR4
// (per FASE 1.C spec).
type Rule struct {
	// ID is the canonical rule family id (e.g. "max_files_per_package",
	// "kernel_split_hint", "data_ownership_doc"). Matches the
	// report.Violation.MatchedRule / Rule field when the rule fires.
	ID string
	// Severity is the rule's default severity on violation. Per-
	// violation severity may still be overridden by the rule's
	// runtime logic (e.g. info for kernel_split_hint vs warn for
	// forbidden_dir).
	Severity string
	// Check runs the rule against the project root and the loaded
	// policy, returning zero or more findings. The closure
	// signature mirrors the scan* family: (root string, pol *Policy)
	// → []report.Violation. Cyclic import avoidance: the Check
	// signature uses an `any` slice today (Phase 0 placeholder);
	// PR4 promotes to `[]report.Violation` once report/ and policy/
	// are both landed and the runner can wire them together.
	//
	// Keeping the field name lowercase (`check`) signals "not part
	// of the JSON / on-disk policy shape" — it's a runtime-only
	// field. Operators reading architecture/policy.yaml will never
	// see `check:` in the YAML.
	Check func(root string, pol *Policy) []any
}
