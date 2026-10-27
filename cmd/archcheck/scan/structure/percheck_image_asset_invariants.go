// Package scan — percheck_image_asset_invariants.go is the
// REGISTRY + ORCHESTRATOR FACADE for the percheck_image_asset_
// invariants forward-prevention gate (split-image-asset-invariants,
// July 2026).
//
// The gate enforces TWO invariants in lockstep:
//
//	(Rule A) image_asset_literal_ban:   bans direct
//	         `&detail.ImageAsset{...}` (and the
//	         domain-alias `&domainasset.ImageAsset{...}`)
//	         literal instantiation anywhere outside the
//	         canonical definition + canonical builder helper
//	         + test stub files;
//	(Rule B) gemma_dto_leak_ban:        scans struct JSON-tag
//	         definitions + struct-literal sites inside the
//	         Gemma-prompt-construction scope
//	         (internal/application/scripts/usecase/**) for any
//	         key in the canonical deny-list
//	         (clipview.ForbiddenCandidateViewJSONFields),
//	         catching future agents that would marshal
//	         `asset_id`, `drive_link`, `clip_id`, `folder_path`,
//	         `content_hash`, `hash`, `local_path`, `job_id`,
//	         `plan_id`, `slot_ref`, `source_url`, etc. into the
//	         model-facing JSON stream.
//
// Fit-for-purpose split (split-image-asset-invariants, July 2026):
//
//	internal/application/.../cmd/archcheck/scan/percheck_image_asset_invariants.go         // THIS FILE (registry + orchestrator)
//	internal/application/.../cmd/archcheck/scan/percheck_image_asset_invariants_rule_a.go  // image_asset_literal_ban (Rule A)
//	internal/application/.../cmd/archcheck/scan/percheck_image_asset_invariants_rule_b.go  // gemma_dto_leak_ban       (Rule B)
//	internal/application/.../cmd/archcheck/scan/percheck_image_asset_invariants_shared.go  // cross-rule infra
//
// The split divides the gate into a registry pattern without
// changing the binary output: the orchestrator iterates the
// registered rules in registration order (Rule A → Rule B).
// Byte-deterministic violation ordering is preserved because
// (a) each rule still emits to r.Violations / r.Warnings in the
// pre-split order, and (b) the orchestrator iterates the slice
// in declaration order — Rule A produces its series first,
// then Rule B appends its series.
//
// runner.go wiring (mirroring the percheck_voiceover_alias_ban
// precedent — godlike/08 evolution may upgrade this to the
// closure pattern):
//
//	{"percheck_image_asset_invariants", scan.ScanImageAssetInvariants}
//
// productionOnly flag is NOT plumbed via the standard CheckSpec
// closure here (the runner.go wiring uses the named binding
// directly). The v1 scanner always reports BOTH violations AND
// comment-only warnings; future PR can upgrade to the closure
// pattern (mirroring `percheck_voiceover_alias_ban.go`) if
// the residue-warning bucket proves operationally noisy.
// Forward-pointer: PR-IMAGE-ASSET-INVARIANTS-PRODUCTION-ONLY.
//
// policy.MaxImageAssetFields is unused by this scanner per the
// PR design scoping (Rule A is "fidelity to canonical
// metadata_json shape" not "field-count-of-the-literal"; a
// future AST-based scanner can lift this field-aware cap).
package structure

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// imageAssetInvariant is the self-contained sub-rule shape
// registered into the percheck_image_asset_invariants gate.
// Every implementation owns its own matched-string set,
// allowlist, scope filter, and residue-accounting policy.
//
// godlike/06 SSOT: each sub-rule owns its OWN fact set. The
// orchestrator below iterates registered rules in declaration
// order; future sub-rules (Rule C / D / …) must be appended
// to defaultImageAssetInvariants() with an explicit forward-
// pointer comment so the audit trail is preserved.
type imageAssetInvariant interface {
	Scan(root string, r *report.Report)
}
