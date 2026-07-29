// Package scan — per-check forward-prevention gate that pins
// the migration 157 DEFAULT literal wire to the canonical
// AssetState initial-sentinel constant
// (PR-CATALOG-MULTILINGUA step 7+ GAMMA, July 2026).
//
// scan/percheck_157_asset_state_migration_default_wire.go
// owns the SQL-migration leg of the canonical-14 forward-
// prevention gate. The canonical-14 source/file leg
// (percheck_asset_state_canonical_14) pins the alphabet
// COUNT inside asset_state_values.go; THIS gate pins the COLUMN
// DEFAULT literal inside migrations/sqlite/157_asset_state.sql
// so that the alphabetical initial-sentinel string in
// Go and the runtime column-default literal in SQL stay
// in lockstep across future agent edits.
//
// godlike/06 SSOT invariant: there is exactly one
// runtime companion for each enum value — the typed
// initial-sentinel (string(asset.StateAssetDiscovered))
// in internal/kernel/asset/asset_state_values.go is mirrored by
// the column DEFAULT in
// migrations/sqlite/157_asset_state.sql. Drift between
// the two surfaces (a future agent renames the typed
// initial sentinel without updating the migration
// DEFAULT, OR vice versa) surfaces as a SeverityError
// from this scanner; SQL line comments are accounted as
// residue (WARNed, not violated) per godlike/07
// NO-FAKE-AVAILABILITY.
//
// scanner policy:
//   - opens ONLY migrations/sqlite/157_asset_state.sql
//     (single canonical SQL migration; no walk).
//   - extracts every `DEFAULT 'literal'` occurrence from
//     non-comment lines (SQL comments start with `--`).
//   - asserts the FIRST non-comment `DEFAULT 'literal'`
//     occurrence equals string(asset.StateAssetDiscovered).
//   - if the comparison fails, emits a SeverityError with
//     both the discovered literal AND the expected literal
//     surface so the operator sees the diff inline.
//   - missing migration file → SeverityError (godlike/07
//     fail-closed: the operator MUST investigate the
//     missing path rather than silently passing).
//   - comment-only lines → residue-accounted WARN
//     (mirrors the canonical-14 comment-residue pathway).
//
// matched rule_id: `percheck_157_asset_state_migration_default_wire`.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// migration157AssetStatePath is the SOLE canonical owner of
// the asset_state column DEFAULT literal. Mirrors
// assetStateCanonical14Path's single-file scan policy.
const migration157AssetStatePath = "migrations/sqlite/157_asset_state.sql"

// migration157DefaultLiteralRe matches the SQL DEFAULT 'literal'
// construct (case-sensitive; SQL default is case-sensitive
// for string literals). The capture group is the literal.
// Anchored so the literal must follow `DEFAULT ` with optional
// whitespace between `DEFAULT` and the opening single quote.
//
//	DEFAULT 'literal'
//	DEFAULT   'literal'        (whitespace tolerated)
//
// The regex is intentionally NOT anchored to `ALTER TABLE` so
// any future migration that introduces a second DEFAULT clause
// trips the gate unless it's also canonical-aligned (a separate
// evolution). Migration 157 currently emits exactly one
// DEFAULT in the column-def line.
var migration157DefaultLiteralRe = regexp.MustCompile(`DEFAULT\s+'([^']+)'`)

// migration157DefaultRule is the rule-family id the scanner
// emits. Mirrors percheck_asset_state_canonical_14['s
// matching naming convention.
const migration157DefaultRule = "percheck_157_asset_state_migration_default_wire"

// migration157DefaultNote is the violation Note string for
// DEFAULT literal drift. Reference the canonical SOLE owner
// + the migration path so the operator sees both surfaces
// of the mismatch inline.
const migration157DefaultNote = "migration 157 DEFAULT literal must equal string(asset.StateAssetDiscovered) (PR-CATALOG-MULTILINGUA step 7+ GAMMA, July 2026); godlike/06 SSOT requires the column DEFAULT to stay in lockstep with the typed canonical initial-sentinel in internal/kernel/asset/asset_state_values.go; rename both surfaces together"

// migration157DefaultWarn is the centralized WARN-bucket
// emitter for residue-accounting. Mirrors assetStateWarn.
func migration157DefaultWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, migration157DefaultRule+" "+label+" "+msg)
}

// ScanAssetStateMigration157DefaultWire opens the single
// canonical migration file (migration157AssetStatePath)
// and asserts the column DEFAULT literal equals
// string(asset.StateAssetDiscovered). Comment-only lines
// (SQL `--`) are residue-accounted per godlike/07.
//
// The scanner ignores any ALTER/CREATE INDEX/other
// constructs and only matches `DEFAULT 'literal'` clauses.
// Migration 157 has exactly one such clause on the column
// ALTER TABLE line; a future migration that introduces
// additional DEFAULTs would be picked up by the same regex
// (the first match wins).
func ScanAssetStateMigration157DefaultWire(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.
	path := filepath.Join(root, migration157AssetStatePath)
	f, err := os.Open(path)
	if err != nil {
		// The canonical migration is the SOLE column-default
		// owner; if it cannot be opened the operator MUST
		// investigate. Surface a violation rather than
		// silently passing (godlike/07 fail-closed).
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        migration157AssetStatePath,
			Line:        0,
			Rule:        migration157DefaultRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "migration_157_default_literal_drift",
			Note:        migration157DefaultNote + " | cannot open migration 157 file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	wantLiteral := string(asset.StateAssetDiscovered)
	// Track the FIRST non-comment `DEFAULT 'literal'` match
	// across the file: literal value + line number + the
	// raw line content (used as the snippet surface on
	// violation notes). Inline-tracking avoids a second
	// file scan to rebuild the snippet.
	gotLiteral := ""
	gotLineNo := 0
	gotLineContent := ""
	commentOnly := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// SQL line comments start with `--`. Ignore for the
		// regex match; surface as residue accounting so a
		// future agent who writes descriptive prose in the
		// migration's godoc doesn't silently trip the gate
		// (godlike/07 NO-FAKE-AVAILABILITY residue path).
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			commentOnly++
			continue
		}
		m := migration157DefaultLiteralRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// First non-comment match wins (migration 157 has
		// exactly one; future migrations may have more
		// and the first DEFAULT that appears on a column
		// ALTER defines the asset_state column-default
		// semantic per SQL parser order).
		if gotLiteral == "" {
			gotLiteral = m[1]
			gotLineNo = lineNo
			gotLineContent = line
		}
	}
	if gotLiteral == "" {
		// File opened but no `DEFAULT 'literal'` clause was
		// found. This is a different failure mode than
		// "file missing" — the migration is structurally
		// incomplete (the wire shape is undefined).
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        migration157AssetStatePath,
			Line:        0,
			Rule:        migration157DefaultRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "migration_157_default_literal_drift",
			Note:        migration157DefaultNote + " | migration 157 has no `DEFAULT 'literal'` clause; column default is undefined (godlike/07 fail-closed)",
		})
	} else if gotLiteral != wantLiteral {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        migration157AssetStatePath,
			Line:        gotLineNo,
			Rule:        migration157DefaultRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "migration_157_default_literal_drift",
			Note: migration157DefaultNote +
				" | actual DEFAULT: '" + gotLiteral + "'" +
				" | want: '" + wantLiteral + "'" +
				" | snippet: " + truncateMigration157Snippet(gotLineContent),
		})
	}
	if commentOnly > 0 {
		migration157DefaultWarn(r, "migration-157-default:",
			strconv.Itoa(commentOnly)+" comment-only line(s) in "+migration157AssetStatePath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// truncateMigration157Snippet bounds the snippet surface so
// the report JSON stays stable across migration growth.
// Mirrors truncateForReport (120-char cap) on the asset
// state scanners.
func truncateMigration157Snippet(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
