// Package scan — percheck_embedding_constants_ssot.go: forward-prevention
// gate that bans NEW embedding model-id declarations outside the canonical
// EmbeddingContract SSOT (PR-HASH-SEMANTICS item 16, August 2026).
//
// godlike/06 SSOT: the text-embedding identity facts (model id, revision,
// dimension, normalization, distance, prefixes, semantic-document version)
// have exactly ONE owner — internal/kernel/embedding (the EmbeddingContract).
// Historical drift happened precisely because `clipindexer`, the Qdrant
// schema, the search backend and the config each declared their own copy of
// `nomic-embed-text` / `multilingual-e5-base`. This gate fails the build when
// a NEW package declares such a model id as a constant/variable.
//
// Scope (deliberately narrow — forward prevention, not retroactive cleanup):
//   - It matches DECLARATION lines only (`<ident> = "<model-id>"` at line
//     start, optionally preceded by `const`/`var`). Struct-literal fields
//     (`Model: "..."`) and config-tag defaults (`default:"..."`) are NOT
//     matched: those are data flowing through the existing Qdrant schema /
//     config surfaces, which the boot-time embedding-contract handshake
//     (QDRANT_EMBEDDING_CONTRACT_MISMATCH) already validates against the
//     canonical Contract. This gate is the fence against someone adding
//     `const embeddingModel = "nomic-embed-text"` in a new package.
//   - It skips internal/kernel/embedding/** (the canonical owner), the scan's
//     own package, and *_test.go files.
//
// matched rule_id: `percheck_embedding_constants_ssot`.
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

// embeddingConstantsSkipDirs is the standard skip-dir set (mirrors the
// sibling percheck scanners).
var embeddingConstantsSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// embeddingConstantsCanonicalOwners is the EXEMPT surface: the canonical
// EmbeddingContract package (internal/kernel/embedding) plus the scanner's
// own package (this file declares the forbidden literals).
var embeddingConstantsCanonicalOwners = []string{
	"internal/kernel/embedding/",
	"cmd/archcheck/scan",
}

// embeddingConstantsScanScope is the prefix the gate applies to.
const embeddingConstantsScanScope = "internal/"

// embeddingConstantsRule is the rule-family id the scanner emits.
const embeddingConstantsRule = "percheck_embedding_constants_ssot"

// embeddingModelIDLiteralRE matches a declaration line that binds an
// identifier to a text-embedding model id. The identifier must be at line
// start (optionally after `const`/`var` and leading whitespace) so field
// assignments (`cfg.Model = "..."`) and struct literals (`Model: "..."`) do
// not false-positive. The set covers the E5 family + the legacy Nomic id.
var embeddingModelIDLiteralRE = regexp.MustCompile(
	`^\s*(?:const|var)?\s*[A-Za-z_][A-Za-z0-9_]*\s*=\s*"(nomic-embed-text|intfloat/multilingual-e5-base|multilingual-e5-base|multilingual-e5-small|multilingual-e5-large)"`,
)

// embeddingConstantsNote is the violation Note for a forbidden embedding
// model-id declaration outside the canonical EmbeddingContract.
const embeddingConstantsNote = "forbidden embedding model-id declaration outside the canonical EmbeddingContract SSOT (PR-HASH-SEMANTICS item 16, August 2026); godlike/06 SSOT requires every text-embedding identity fact (model id, revision, dimension, normalization, distance, prefixes) to be owned ONLY by internal/kernel/embedding. Do NOT declare a new embedding-model constant/variable in another package — reference internal/kernel/embedding.CanonicalText (or its exported constants) instead. Historical drift (nomic-embed-text vs multilingual-e5-base) broke query/document vector coherence; this gate fails closed on any re-introduction."

// ScanEmbeddingConstantsSSOT walks every .go file under internal/** and emits
// a violation for any non-test production file outside the canonical
// EmbeddingContract package that declares an embedding model id as a
// constant/variable.
func ScanEmbeddingConstantsSSOT(root string, pol *policy.Policy, r *report.Report, _ bool) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if embeddingConstantsSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, embeddingConstantsCanonicalOwners) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.HasPrefix(relSlash, embeddingConstantsScanScope) {
			return nil
		}
		if hasAnyPathPrefix(relSlash, embeddingConstantsCanonicalOwners) {
			return nil
		}
		scanEmbeddingConstantsFile(path, relSlash, r)
		return nil
	})
}

// scanEmbeddingConstantsFile opens a single .go file and emits
// percheck_embedding_constants_ssot violations for any line matching the
// embedding model-id declaration pattern.
func scanEmbeddingConstantsFile(path, relPath string, r *report.Report) {
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
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		m := embeddingModelIDLiteralRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     filepath.ToSlash(filepath.Dir(relPath)),
			File:        relPath,
			Line:        lineNo,
			Rule:        embeddingConstantsRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "non_canonical_embedding_constant",
			Note:        embeddingConstantsNote + " | model: " + m[1] + " | snippet: " + truncateEmbeddingConstants(line),
		})
	}
}

// truncateEmbeddingConstants bounds the snippet surface at 120 chars.
func truncateEmbeddingConstants(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
