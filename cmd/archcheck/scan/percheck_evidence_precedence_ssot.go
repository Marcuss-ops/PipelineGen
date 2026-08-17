// Package scan — percheck_evidence_precedence_ssot.go
//
// Forward-prevention gate: production Go code MUST NOT re-implement the
// canonical evidence precedence. The SINGLE owner is
// internal/kernel/asset/evidence.go::ResolveEvidence (order: transcript →
// semantic_summary → visual_summary → summary → description → fail closed).
// Qdrant, script and search each carried their own drift-prone copy before
// this convergence; this gate prevents the transcript-first selection from
// coming back as a local first-non-empty helper.
//
// The drift shape banned here is a first-non-empty selection helper whose
// argument list puts the canonical "transcript" tier ahead of the summary /
// description tiers (the defining transcript-first ordering). Producers are
// expected to build asset.EvidenceInput and call asset.ResolveEvidence — not
// to re-order the tiers themselves.
//
// Out of scope by design: read-only historical-payload reconstruction that
// selects a *description* field (no transcript tier, e.g. the Qdrant
// recovery reader) is a different concern and is NOT a copy of the grounding
// precedence.
//
// Exempt zones:
//   - internal/kernel/asset — the canonical SSOT owner.
//   - **/*_test.go — regression-guard fixtures may pin the legacy shape.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_evidence_precedence_ssot
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const evidencePrecedenceSSOTRule = "percheck_evidence_precedence_ssot"

const evidencePrecedenceSSOTNote = "forbidden evidence-precedence re-implementation. The single owner is internal/kernel/asset/evidence.go::ResolveEvidence (transcript → semantic_summary → visual_summary → summary → description → fail closed). Build asset.EvidenceInput and call ResolveEvidence instead of re-ordering the tiers in a local first-non-empty helper."

// evidencePrecedenceSSOTHelperRe matches the selection-helper call shapes
// that historically drifted into per-consumer evidence precedence copies.
var evidencePrecedenceSSOTHelperRe = regexp.MustCompile(`(?i)\b(firstString|firstNonEmpty|firstNonEmptyString|firstNonEmptyProvider|coalesce|pickFirst|firstEvidence)\s*\(`)

// evidencePrecedenceSSOTTranscriptRe pins the defining first tier: transcript
// is the tier whose presence (in a selection helper) marks an evidence
// precedence rather than an unrelated fallback (URLs, titles, provider ids).
var evidencePrecedenceSSOTTranscriptRe = regexp.MustCompile(`"transcript"`)

// evidencePrecedenceSSOTOtherTierRe matches any non-transcript canonical tier
// that a transcript-first selection may be ordered against.
var evidencePrecedenceSSOTOtherTierRe = regexp.MustCompile(`"semantic_summary"|"visual_summary"|"summary"|"description"`)

var evidencePrecedenceSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var evidencePrecedenceSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var evidencePrecedenceSSOTExemptPathPrefixes = []string{
	"internal/kernel/asset",
}

// ScanEvidencePrecedenceSSOT walks every .go file under the repo root and
// emits a violation for any production file that re-introduces a
// transcript-first evidence-tier selection helper outside the canonical
// resolver.
func ScanEvidencePrecedenceSSOT(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if evidencePrecedenceSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), evidencePrecedenceSSOTSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if hasAnyPathPrefix(relSlash, evidencePrecedenceSSOTExemptPathPrefixes) {
			return nil
		}
		scanEvidencePrecedenceSSOTFile(path, relSlash, r)
		return nil
	})
}

func scanEvidencePrecedenceSSOTFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if !evidencePrecedenceSSOTHelperRe.MatchString(line) {
			continue
		}
		if !evidencePrecedenceSSOTTranscriptRe.MatchString(line) {
			continue
		}
		if !evidencePrecedenceSSOTOtherTierRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromEvidencePrecedenceSSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        evidencePrecedenceSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "evidence_precedence_ssot_gate",
			Note:        evidencePrecedenceSSOTNote + " | snippet: " + truncateEvidencePrecedenceSSOT(line),
		})
	}
}

func truncateEvidencePrecedenceSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

func pkgFromEvidencePrecedenceSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
