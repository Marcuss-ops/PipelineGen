// Package scan — percheck_version_strings_ban.go
//
// Forward-prevention gate that BANS hardcoded pipeline version
// strings of the form "<name>-v<N>" outside the canonical version
// registry.
//
// The canonical version registry lives in
// internal/kernel/media/version.go. Every component that needs to
// expose a version MUST reference a constant from that file instead
// of repeating the literal string.
//
// Exempt zones:
//   - internal/kernel/media/version.go — the canonical SSOT owner.
//   - **/*_test.go — test fixtures may use literal versions for
//     regression checks.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_version_strings_ban
package governance

import (
	"regexp"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/textban"
)

// versionStringsSSOTRe matches the canonical resolution pipeline
// version literals (e.g. "brain-v1", "multilingual-e5-v1"). It is
// deliberately narrow to avoid flagging unrelated schema / model
// versions such as "2026-06-16-v1".
var versionStringsSSOTRe = regexp.MustCompile(`"(brain|intent-registry|multilingual-e5|diversity-policy|slot-sampler|provider-registry|media-ranker|scene-planner|visual-intent)-v[0-9]+"`)

const versionStringsSSOTRule = "percheck_version_strings_ban"

const versionStringsSSOTNote = "forbidden hardcoded pipeline version string outside the canonical version registry (internal/kernel/media/version.go). Components MUST reference a version constant from the registry so that version changes propagate from a single source of truth."

var versionStringsSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var versionStringsSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var versionStringsSSOTExemptPathPrefixes = []string{
	"internal/kernel/media",
}

const versionStringsSSOTScanScope = "internal/"

// ScanVersionStringsBan walks every .go file under <root>/internal/**
// and emits a violation for any production file that contains a
// hardcoded pipeline version string outside the canonical registry.
func ScanVersionStringsBan(root string, _ *policy.Policy, r *report.Report) {
	textban.Scan(root, textban.Spec{
		RuleID:             versionStringsSSOTRule,
		MatchedRule:        "version_strings_ssot_gate",
		Note:               versionStringsSSOTNote,
		ScanPrefixes:       []string{versionStringsSSOTScanScope},
		SkipDirs:           versionStringsSSOTSkipDirs,
		SkipPathPrefixes:   versionStringsSSOTSkipPathPrefixes,
		ExemptPathPrefixes: versionStringsSSOTExemptPathPrefixes,
		Match:              versionStringsSSOTRe.MatchString,
	}, r)
}
