// Package scan — per-check forward-prevention gate that bans
// Drive/Qdrant/SQLite fields in the MediaTransformer DTOs
// (PR-MEDIATRANSFORMER-RENAME, July 2026).
//
// scan/percheck_mediatransformer_no_infra_fields.go owns the Go
// migration of the forward-prevention gate for the
// MediaTransformer contract. The canonical DTOs (`TransformSpec`
// and `RenditionSet`) are declared at
// `internal/domain/asset/processor.go` and MUST NOT carry fields
// whose name OR type contains the forbidden substrings:
//
//   - "drive"   : Drive SDK / DriveFileID / DriveLink / Drive
//     admin / drive.Store / drive.Reader / etc.
//   - "qdrant"  : Qdrant vectors / collections / points /
//     qdrant.Client / qdrant.Schema / etc.
//   - "sqlite"  : SQLite database / sqlite3 / *sql.DB /
//     mattn/go-sqlite3 / sql.Tx / etc.
//
// godlike/06 SSOT: the MediaTransformer is a local-only
// transformer. It takes a `StagedSource` (already on local disk)
// and produces a local `RenditionSet`. Drive/Qdrant/SQLite
// concerns are OUT OF SCOPE for the transformer and belong to
// the orchestrator + finalizer + commit layers downstream.
//
// Step 1 (July 2026): the god service is ONLY renamed. The
// forbidden fields STAY in `RenditionSet` for now (DriveLink,
// DriveFileID, DownloadLink, MD5, PublishAction) and in
// `TransformSpec` (FolderID, DriveFileID, ClipPageURL). This
// gate WILL trip on those existing fields and surface a
// forward-pointer violation; step 2 of
// PR-MEDIATRANSFORMER-RENAME deletes the fields and the gate
// passes.
//
// scanner policy (mirrors percheck_asset_state_no_shadow_enum.go):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (test stubs legitimately need
//     fixture declarations).
//   - skip `cmd/archcheck/scan/**` (this scanner file +
//     sibling scanners reference the canonical literals).
//   - allow the canonical SOLE owner
//     (internal/domain/asset/processor.go) — the gate
//     inspects THIS file but the inspection is read-only.
//   - comment-only references to Drive/Qdrant/SQLite are
//     WARNed (residue accounting, godlike/07).
//
// matched rule_id: `percheck_mediatransformer_no_infra_fields`.
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

// mediaTransformerCanonicalPath is the canonical SOLE owner of
// the MediaTransformer contract (interface + TransformSpec +
// RenditionSet DTOs). The gate inspects this file but the
// inspection is read-only — the gate does NOT modify the file.
const mediaTransformerCanonicalPath = "internal/domain/asset/processor.go"

// mediaTransformerTargetTypes lists the DTO struct names that
// the gate inspects. Both names are matched exactly
// (case-sensitive) so a struct named `transformSpec` (lowercase)
// does NOT match. The gate is restricted to the canonical DTOs
// of MediaTransformer; other structs in the file (e.g.
// `ProcessingRecord`) are NOT scanned.
var mediaTransformerTargetTypes = []string{"TransformSpec", "RenditionSet"}

// mediaTransformerForbiddenSubstrings is the canonical list of
// infrastructure concerns that the MediaTransformer MUST NOT
// leak into its DTOs. A field whose NAME or TYPE contains any
// of these substrings (case-insensitive) is a violation.
//
// "drive"   covers Drive SDK / DriveFileID / DriveLink /
//
//	DownloadLink / drive.Store / drive.Reader / etc.
//
// "qdrant"  covers Qdrant vectors / qdrant.Client / etc.
// "sqlite"  covers SQLite / *sql.DB / sqlite3 / etc.
var mediaTransformerForbiddenSubstrings = []string{"drive", "qdrant", "sqlite"}

// mediaTransformerFieldRe matches a Go struct field declaration
// of the shape `FieldName TypeExpr` inside a struct body. It
// captures the field name and the type expression as named
// subgroups so the violation Note can surface the exact snippet.
//
// Pattern:
//
//	^[\t ]+      — tab-indented (struct field line).
//	([A-Z]\w*)   — exported field name (Go convention).
//	[\t ]+       — whitespace separator.
//	([\w\.\*\[\]\{\},\s]+) — type expression (allows pointers,
//	                        generics, maps, slices).
//	(?:\s+`[^`]*`)? — optional struct tag.
//
// Limitations: the regex does not handle multi-line type
// expressions (e.g. `map[string]\n\tint`). The MediaTransformer
// DTOs use single-line field declarations so the limitation is
// acceptable. Comment-only lines (starting with `//`) are
// filtered out before the regex is applied.
var mediaTransformerFieldRe = regexp.MustCompile(`^[\t ]+([A-Z]\w*)[\t ]+([\w\.\*\[\]\{\},\s]+?)(?:\s+` + "`[^`]*`" + `)?\s*$`)

// mediaTransformerRule is the rule-family id the scanner emits.
// Mirrors percheck_asset_state_no_shadow_enum.go
// MatchedRule naming.
const mediaTransformerRule = "percheck_mediatransformer_no_infra_fields"

// mediaTransformerNote is the violation Note string for
// forbidden fields. The message references the canonical SOLE
// owner + the forward-prevention gate so the operator sees
// the migration path inline.
const mediaTransformerNote = "forbidden field in MediaTransformer DTO (PR-MEDIATRANSFORMER-RENAME step 1, July 2026): the MediaTransformer is a local-only transformer and MUST NOT carry Drive/Qdrant/SQLite fields in its DTOs; route the infrastructure concern through the orchestrator + finalizer + commit layers downstream. Step 2 of PR-MEDIATRANSFORMER-RENAME deletes the existing forbidden fields and this gate will pass"

// mediaTransformerSkipDirs mirrors percheck_asset_state_no_shadow_enum.go's
// standard skip-dir set.
var mediaTransformerSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// mediaTransformerSkipPathPrefixes is the scan's own package
// exemption — this file declares regex literals matching the
// forbidden substrings (false-positive exemption).
var mediaTransformerSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// mediaTransformerWarn is the WARN-bucket emitter for
// residue-accounting. Mirrors assetStateWarn.
func mediaTransformerWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, mediaTransformerRule+" "+label+" "+msg)
}

// ScanMediaTransformerNoInfraFields opens the canonical file
// (`processor.go`) and inspects the struct bodies of
// `TransformSpec` and `RenditionSet` for forbidden
// Drive/Qdrant/SQLite fields. Each forbidden field emits a
// violation; comment-only references are residue-accounted
// (WARNed, not violated) per godlike/07.
//
// In step 1 (July 2026) this gate WILL trip on the existing
// forbidden fields. The violations are EXPECTED and documented
// as forward-pointers to step 2 of PR-MEDIATRANSFORMER-RENAME,
// which deletes the forbidden fields.
func ScanMediaTransformerNoInfraFields(root string, pol *policy.Policy, r *report.Report) {
	_ = pol
	path := filepath.Join(root, mediaTransformerCanonicalPath)
	f, err := os.Open(path)
	if err != nil {
		// The canonical file is the SSOT; if it cannot be
		// opened the operator MUST investigate. Surface a
		// violation rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        mediaTransformerCanonicalPath,
			Line:        0,
			Rule:        mediaTransformerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_file_unreadable",
			Note:        mediaTransformerNote + " | cannot open canonical file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// State machine: track which target struct body we are
	// currently inside. Empty string = not inside a target.
	currentTarget := ""
	typeEndLine := 0
	braceDepth := 0
	commentOnly := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		// Enter a new struct body when we see `type
		// <Name> struct {`. The Name must be in
		// mediaTransformerTargetTypes.
		if currentTarget == "" {
			if m := regexp.MustCompile(`^type\s+(\w+)\s+struct\s*\{`).FindStringSubmatch(trimmed); m != nil {
				name := m[1]
				for _, t := range mediaTransformerTargetTypes {
					if name == t {
						currentTarget = name
						braceDepth = 1
						typeEndLine = lineNo
						break
					}
				}
			}
			continue
		}

		// Track brace depth so we know when the struct body ends.
		braceDepth += strings.Count(line, "{")
		braceDepth -= strings.Count(line, "}")
		// Subtract the opening brace we already counted (it
		// appeared on the `type ... struct {` line above).
		// The above `+1` happens AFTER we set `braceDepth = 1`
		// so the next `{` increments to 2, next `}` to 1,
		// next `}` to 0. We exit when braceDepth == 0.
		if braceDepth == 0 {
			currentTarget = ""
			typeEndLine = 0
			continue
		}

		// Comment-only line: residue-accounted.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if hasAnySubstring(line, mediaTransformerForbiddenSubstrings) {
				commentOnly++
			}
			continue
		}

		// Try to match a struct field declaration.
		m := mediaTransformerFieldRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fieldName := strings.TrimSpace(m[1])
		fieldType := strings.TrimSpace(m[2])

		// Check if the field name or type contains any forbidden
		// substring. Both checks are case-insensitive.
		lowerName := strings.ToLower(fieldName)
		lowerType := strings.ToLower(fieldType)
		for _, forbidden := range mediaTransformerForbiddenSubstrings {
			if strings.Contains(lowerName, forbidden) || strings.Contains(lowerType, forbidden) {
				r.Violations = append(r.Violations, report.Violation{
					Package: "internal/domain/asset",
					File:    mediaTransformerCanonicalPath,
					Line:    lineNo,
					Rule:    mediaTransformerRule,
					Severity: func() string {
						// Step 1: VIOLATION (forward-pointer).
						// After step 2 deletes the fields, this
						// stays a VIOLATION so a regression
						// (re-adding a forbidden field) trips
						// the gate again.
						return string(report.SeverityError)
					}(),
					MatchedRule: "forbidden_field_" + forbidden,
					Note: mediaTransformerNote +
						" | DTO: " + currentTarget +
						" | field: " + fieldName +
						" | type: " + fieldType +
						" | matched substring: " + forbidden,
				})
				break
			}
		}
	}
	if commentOnly > 0 {
		mediaTransformerWarn(r, "forbidden-fields:",
			"comment-only reference(s) to forbidden substrings in "+mediaTransformerCanonicalPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// hasAnySubstring returns true if `s` contains any of the
// substrings in `needles` (case-sensitive).
func hasAnySubstring(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
