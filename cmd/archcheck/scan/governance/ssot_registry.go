// Package scan — ssot_registry.go: the registry of godlike/06 forward-
// prevention facts, plus the thin entry points that checks.go calls.
//
// Each row below used to be its own scanner with its own walk, skip set,
// comment policy, snippet truncation and `pkgFromXxxRel` helper. The engine
// (ssot.go) now owns all of that; a row owns only what is actually specific
// to the fact: its scope, its exemptions, its owner package, and either the
// predicate that recognises the drift shape or a stateful per-file scanner.
// The fact-specific vocabulary (rule ids, notes, regexes, canonical paths and
// the stateful scanners) lives in ssot_facts.go.
//
// Invariant (pinned by ssot_registry_test.go): a rule's id is unique, the rule
// has a detector (Detect OR ScanFile), and every exported ScanXxx delegates to
// a registered rule — so a fact cannot be silently dropped from the engine.
package governance

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ssotRules is the ordered registry. Rule ids must be unique.
var ssotRules = []ssotRule{
	{
		Name:        embeddingConstantsRule,
		MatchedRule: "non_canonical_embedding_constant",
		Scope:       []string{"internal/"},
		SkipDirs:    policy.SkipDirs(),
		Owners: []string{
			"internal/kernel/models/",
			"internal/kernel/embedding/",
			ssotScannerSourcePrefix,
		},
		Detect: func(_ any, _ string, line string) []string {
			m := embeddingModelIDLiteralRE.FindStringSubmatch(line)
			if m == nil {
				return nil
			}
			return []string{embeddingConstantsNote + " | model: " + m[1] + " | snippet: " + truncateSSOTSnippet(line)}
		},
	},
	{
		Name:             durationProbeSSOTRule,
		MatchedRule:      "duration_probe_ssot_gate",
		Owners:           []string{"internal/platform/media"},
		SkipPathPrefixes: []string{ssotScannerSourcePrefix},
		Detect:           ssotMatchLine(durationProbeSSOTNote, durationProbeSSOTMatch),
	},
	{
		Name:        observabilityOperationSSOTRule,
		MatchedRule: "observability_operation_single_writer",
		// No scope, no dir skips and //-only comments: this rule deliberately
		// walks the whole tree exactly as the original scanner did.
		SkipDirs:        ssotNoDirSkips,
		CommentPrefixes: []string{"//"},
		Detect: func(_ any, relPath, line string) []string {
			var notes []string
			if strings.Contains(line, "MeasuredOperationRecorder") {
				notes = append(notes, observabilityOperationRetiredRecorderNote)
			}
			if !hasAnyPathPrefix(relPath, []string{observabilityOperationCanonicalStore}) &&
				strings.Contains(strings.ToLower(line), "insert into performance_operations") {
				notes = append(notes, observabilityOperationSecondWriterNote)
			}
			return notes
		},
	},
	{
		Name:             speechTimingSSOTRule,
		MatchedRule:      "speech_timing_ssot_gate",
		Owners:           []string{"internal/capabilities/audio"},
		SkipPathPrefixes: []string{ssotScannerSourcePrefix},
		Detect:           ssotMatchLine(speechTimingSSOTNote, func(line string) bool { return strings.Contains(line, speechTimingSSOTLiteral) }),
	},
	{
		Name:             projectDerivationSSOTRule,
		MatchedRule:      "project_derivation_ssot_gate",
		SkipPathPrefixes: []string{ssotScannerSourcePrefix},
		Detect:           ssotMatchLine(projectDerivationSSOTNote, projectDerivationSSOTRe.MatchString),
	},
	{
		Name:             evidencePrecedenceSSOTRule,
		MatchedRule:      "evidence_precedence_ssot_gate",
		Owners:           []string{"internal/kernel/asset"},
		SkipPathPrefixes: []string{ssotScannerSourcePrefix},
		Detect: ssotMatchLine(evidencePrecedenceSSOTNote, func(line string) bool {
			return evidencePrecedenceSSOTHelperRe.MatchString(line) &&
				evidencePrecedenceSSOTTranscriptRe.MatchString(line) &&
				evidencePrecedenceSSOTOtherTierRe.MatchString(line)
		}),
	},
	{
		Name:        stopwordMapRule,
		MatchedRule: "stopword_maps_ssot_gate",
		// Scope = every target root that can hold production code. The
		// historical scope (internal/application/, internal/infrastructure/)
		// pointed at two roots deleted in August 2026, so this gate walked
		// nothing and could never fire.
		Scope:            []string{"internal/"},
		SkipDirs:         policy.SkipDirs(),
		SkipPathPrefixes: []string{ssotScannerSourcePrefix},
		// The canonical linguistic-data owner is exempt by construction:
		// stop-word sets are DATA of the LexiconRegistry, not code.
		Owners:   []string{"internal/capabilities/linguistics/"},
		ScanFile: scanStopwordMapRuleFile,
	},
	{
		Name:             metadataKeyScannerRule,
		MatchedRule:      "unregistered_namespaced_key",
		SkipDirs:         policy.SkipDirs("scripts"),
		SkipPathPrefixes: []string{"internal/kernel/asset/"},
		Prepare: func(root string, r *report.Report, _ *ssotRule) (any, bool) {
			keys := parseMetadataKeys(root, r)
			// A misconfigured canonical registry is a typed fail-closed
			// violation; skip the walk so the report does not flood with
			// config-drift noise on top of the real cause.
			if metadataKeyHasConfigViolation(r) {
				return nil, false
			}
			whitelist := make(map[string]bool, len(keys))
			for _, k := range keys {
				whitelist[k] = true
			}
			return whitelist, true
		},
		ScanFile: scanMetadataKeysRuleFile,
	},
	{
		Name:             indexedStateWriterSSOTRule,
		MatchedRule:      "indexed_state_writer_ssot",
		Scope:            []string{"internal/"},
		SkipDirs:         policy.SkipDirs(),
		SkipPathPrefixes: policy.Prefixes([]string{policy.ScannerSourcePrefix}, policy.TestOnlySupportPrefixes),
		Owners:           indexedStateWriterSSOTCanonicalPaths,
		ScanFile:         scanIndexedStateWriterRuleFile,
	},
	{
		Name:             assetCommitterEventSSOTRule,
		MatchedRule:      "non_canonical_index_event_emission",
		Scope:            []string{"internal/", "tests/", "cmd/"},
		SkipDirs:         policy.SkipDirs(),
		SkipPathPrefixes: []string{policy.ScannerSourcePrefix},
		Owners:           assetCommitterEventSSOTOwnerPaths,
		ScanFile:         scanAssetCommitterEventRuleFile,
	},
}

// ── Entry points (unchanged surface for checks.go + the per-gate tests) ───

// ScanEmbeddingConstantsSSOT is the registry-backed entry point for
// percheck_embedding_constants_ssot. The 4th parameter is retained for
// signature parity with the other checks.go scanners.
func ScanEmbeddingConstantsSSOT(root string, _ *policy.Policy, r *report.Report, _ bool) {
	scanSSOTRule(root, r, ssotRuleByName(embeddingConstantsRule))
}

// ScanDurationProbeSSOT is the registry-backed entry point for
// percheck_duration_probe_ssot.
func ScanDurationProbeSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(durationProbeSSOTRule))
}

// ScanObservabilityOperationSSOT is the registry-backed entry point for
// percheck_observability_operation_ssot.
func ScanObservabilityOperationSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(observabilityOperationSSOTRule))
}

// ScanSpeechTimingSSOT is the registry-backed entry point for
// percheck_speech_timing_ssot.
func ScanSpeechTimingSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(speechTimingSSOTRule))
}

// ScanProjectDerivationSSOT is the registry-backed entry point for
// percheck_project_derivation_ssot.
func ScanProjectDerivationSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(projectDerivationSSOTRule))
}

// ScanEvidencePrecedenceSSOT is the registry-backed entry point for
// percheck_evidence_precedence_ssot.
func ScanEvidencePrecedenceSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(evidencePrecedenceSSOTRule))
}

// ScanStopwordMapsInApp is the registry-backed entry point for
// percheck_stopword_maps_in_app.
func ScanStopwordMapsInApp(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(stopwordMapRule))
}

// ScanMetadataKeys is the registry-backed entry point for
// percheck_metadata_key_registry (emitted rule id percheck_metadata_key_registry).
func ScanMetadataKeys(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(metadataKeyScannerRule))
}

// ScanIndexedStateWriterSSOT is the registry-backed entry point for
// percheck_indexed_state_writer_ssot.
func ScanIndexedStateWriterSSOT(root string, _ *policy.Policy, r *report.Report) {
	scanSSOTRule(root, r, ssotRuleByName(indexedStateWriterSSOTRule))
}

// ScanAssetCommitterEventSSOT is the registry-backed entry point for
// percheck_asset_committer_event_ssot. productionOnly silences the comment-only
// residue bucket so the operator-facing "zero production-code hits" claim
// stays auditable via len(r.Violations) == 0.
func ScanAssetCommitterEventSSOT(root string, _ *policy.Policy, r *report.Report, productionOnly bool) {
	rule := ssotRuleByName(assetCommitterEventSSOTRule)
	rule.SuppressResidueWarnings = productionOnly
	scanSSOTRule(root, r, rule)
}
