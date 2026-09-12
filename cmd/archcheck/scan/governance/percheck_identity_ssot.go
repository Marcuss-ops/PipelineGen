// Package scan — percheck_identity_ssot.go (identity SSOT forward prevention).
//
// ONE generic, data-driven gate replaces the per-fact literal greps that used
// to protect event names and job types. The rule is structural, not textual:
//
//	A canonical identity literal (outbox event name, envelope schema version,
//	shared job type) may be DECLARED in exactly one package — its owner.
//
// The vocabulary is not copied into this file. It is read from the owners
// themselves:
//
//	internal/kernel/event  →  event.Canonical()
//	internal/kernel/job    →  job.CanonicalTypeIdentities()
//
// That is the point of the gate: a scanner that hardcodes the literals it
// protects holds a second copy of the fact and can drift away from the code
// it is supposed to police. Before this gate existed, the same event literal
// was declared twice (sqlite outboxevents + postgres media) and EVERY scanner
// exempted both copies, so the duplication was invisible by construction.
//
// Detection is DECLARATION-scoped, matching the SSOT intent: we flag
// `Name = "literal"` declarations, not usages. A comparison
// (`evt.Type == "asset.index.requested"`) and a log message are not
// declarations of the fact; they are consumers of it. Consumers migrate to
// the constant opportunistically; re-declarations are hard failures.
//
// Exempt (never a violation):
//   - the owner package itself (it declares the literal),
//   - *_test.go fixtures (they legitimately pin wire strings),
//   - cmd/archcheck/** (the scanners reference literals as documentation),
//   - the standard skip-dir set (vendor, node_modules, docs, ...).
//
// Matched rule_id: `percheck_identity_ssot`.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const identitySSOTRule = "percheck_identity_ssot"

// identitySSOTOwnerEvent / identitySSOTOwnerJob are the owner package
// directories (repo-relative, slash-separated). A literal declared under its
// owner prefix is legitimate; anywhere else it is a violation.
const (
	identitySSOTOwnerEvent = "internal/kernel/event/"
	identitySSOTOwnerJob   = "internal/kernel/job/"
)

// identityDeclRe matches a Go declaration/assignment of a string literal:
//
//	Name = "value"
//	Name Type = "value"
//	const Name = "value"
//
// The name capture is used only for diagnostics. Lines with a trailing
// comment are trimmed before matching.
var identityDeclRe = regexp.MustCompile(`^\s*(?:const|var)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:[A-Za-z_][A-Za-z0-9_.]*\s+)?=\s*"([^"]*)"\s*$`)

// identitySSOTViolationNote explains the rule and the remediation inline.
const identitySSOTViolationNote = "canonical identity literal re-declared outside its owner package (godlike/06 one owner per fact). The literal is owned by internal/kernel/event (outbox event names + envelope schema versions) or internal/kernel/job (shared job types); domain, capability and platform packages MUST alias the owner constant (e.g. script.TypeGenerate = job.TypeScriptGenerate, outboxevents.EventAssetIndexRequested = event.AssetIndexRequested) instead of re-declaring the string. Two declarations of a wire fact are a silent drift hazard: the value is the SQLite/PG outbox event_type, the jobs.type discriminator and the C3 dispatcher routing key. The forward-prevention gate is percheck_identity_ssot."

// identityOwner is one registry row: the literal and the owner package that
// is allowed to declare it.
type identityOwner struct {
	Const   string
	Literal string
	Owner   string
}

// identityOwners builds the literal → owner map from the owner packages'
// registries. No literal appears in this file.
func identityOwners() map[string]identityOwner {
	out := make(map[string]identityOwner)
	for _, id := range event.Canonical() {
		out[id.Literal] = identityOwner{Const: "event." + id.Const, Literal: id.Literal, Owner: identitySSOTOwnerEvent}
	}
	for _, id := range job.CanonicalTypeIdentities() {
		out[id.Literal] = identityOwner{Const: "job." + id.Const, Literal: id.Literal, Owner: identitySSOTOwnerJob}
	}
	return out
}

// ScanIdentitySSOT walks every non-test .go file under <root>/internal/** and
// <root>/cmd/** and emits a violation for each declaration of a canonical
// identity literal made outside its owner package.
func ScanIdentitySSOT(root string, _ *policy.Policy, r *report.Report) {

	owners := identityOwners()
	skipDirs := policy.SkipDirs()

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
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
		// Exempt: owner packages, tests, and the scanner sources.
		if strings.HasPrefix(relSlash, identitySSOTOwnerEvent) ||
			strings.HasPrefix(relSlash, identitySSOTOwnerJob) ||
			strings.HasPrefix(relSlash, policy.ScannerSourcePrefix) {
			return nil
		}
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if !strings.HasPrefix(relSlash, "internal/") &&
			!strings.HasPrefix(relSlash, "cmd/") {
			return nil
		}
		scanIdentitySSOTFile(path, relSlash, owners, r)
		return nil
	})
}

// scanIdentitySSOTFile emits one violation per re-declaration found in a
// single .go file.
func scanIdentitySSOTFile(path, relPath string, owners map[string]identityOwner, r *report.Report) {
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
		// Strip a trailing line comment so `X = "v" // note` still matches.
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		m := identityDeclRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, literal := m[1], m[2]
		owner, ok := owners[literal]
		if !ok {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromIdentitySSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        identitySSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "identity_literal_redeclared_outside_owner",
			Note: identitySSOTViolationNote +
				" | literal: " + literal + " | owner const: " + owner.Const +
				" | local: " + name,
		})
	}
}

// pkgFromIdentitySSOTRel extracts the package identifier from a
// repo-relative file path.
func pkgFromIdentitySSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
