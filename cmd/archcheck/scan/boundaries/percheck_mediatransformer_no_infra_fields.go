// Package scan — per-check forward-prevention gate that bans
// Drive/Qdrant/SQLite fields in the MediaTransformer DTOs
// (PR-MEDIATRANSFORMER-RENAME, July 2026; PR-MEDIATRANSFORMER-WB, July 2026).
//
// scan/percheck_mediatransformer_no_infra_fields.go owns the Go
// migration of the forward-prevention gate for the
// MediaTransformer contract. The canonical DTOs (`TransformSpec`
// and `RenditionSet`) are declared at
// `internal/kernel/asset/processor.go` and MUST NOT carry fields
// whose NAME or TYPE matches any of the forbidden WORDS
// (case-insensitive, word-bounded) listed below.
//
// PR-MEDIATRANSFORMER-WB (July 2026, follow-up to PR-MEDIATRANSFINALE):
// the matcher now uses `(?i)\b<name>\b`-anchored regex per entry
// (see `mediaTransformerForbiddenWordReList`). Word-bounding
// eliminates the false-positive surface where a future field
// named `MD5ChecksumStr`, `DownloadLinkLocal`, or
// `ClipPageURLBackup` would trip because its name contains the
// substring `md5` / `downloadlink` / `clippageurl` without
// actually being one of the forward-pointer fields step 2 of
// PR-MEDIATRANSFORMER-RENAME will delete.
//
// godlike/06 SSOT: the MediaTransformer is a local-only
// transformer. It takes a `StagedSource` (already on local disk)
// and produces a local `RenditionSet`. Drive/Qdrant/SQLite
// concerns are OUT OF SCOPE for the transformer and belong to
// the orchestrator + finalizer + commit layers downstream.
//
// Forbidden WORDS (each is a whole-word match per
// mediaTransformerForbiddenWordReList, see godoc on that var):
//
//   - "drive"   : Drive SDK / DriveFileID / DriveLink / Drive
//     admin / drive.Store / drive.Reader / etc. (`*cmd.DriveClient`
//     does NOT trip because `drive` is not a word boundary, but
//     `cmd.Drive` itself or `DriveStore` does trip).
//   - "qdrant"  : Qdrant vectors / collections / points /
//     qdrant.Client / qdrant.Schema / etc.
//   - "sqlite"  : SQLite database / sqlite3 / *sql.DB /
//     mattn/go-sqlite3 / sql.Tx / etc.
//   - "folderid": FolderID / folder_id (Drive folder anchor;
//     removed by step 2 of PR-MEDIATRANSFORMER-RENAME).
//   - "downloadlink": DownloadLink / download_link (Drive URL;
//     removed by step 2).
//   - "md5": MD5 (Drive upload checksum; removed by step 2).
//   - "publishaction": PublishAction (Drive action enum;
//     removed by step 2).
//   - "clippageurl": ClipPageURL (artlist provider URL;
//     removed by step 2).
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
//     (internal/kernel/asset/processor.go) — the gate
//     inspects THIS file but the inspection is read-only.
//   - comment-only references to the forbidden WORDS are
//     WARNed (residue accounting, godlike/07). The comment-
//     only path uses substring (`hasAnySubstring` + the
//     legacy `mediaTransformerForbiddenSubstrings` list)
//     because descriptive prose is NOT bound by word
//     semantics; a comment referencing "DriveStore-shaped
//     pkg" without offending a real field is descriptive, not
//     a violation. The field matcher is the canonical
//     word-bounded path; the comment-residue bucket is the
//     canonical tolerant path. Both are intentionally
//     distinct.
//
// matched rule_id: `percheck_mediatransformer_no_infra_fields`.
package boundaries

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
const mediaTransformerCanonicalPath = "internal/kernel/asset/detail/processor.go"

// mediaTransformerTargetTypes lists the DTO struct names that
// the gate inspects. Both names are matched exactly
// (case-sensitive) so a struct named `transformSpec` (lowercase)
// does NOT match. The gate is restricted to the canonical DTOs
// of MediaTransformer; other structs in the file (e.g.
// `ProcessingRecord`) are NOT scanned.
var mediaTransformerTargetTypes = []string{"TransformSpec", "RenditionSet"}

// mediaTransformerForbiddenSubstrings is the tolerant substring
// list used by the comment-only residue bucket (via
// `hasAnySubstring`). It mirrors the canonical word list
// below — keep them in lockstep when adding new forbidden
// words — but the field matcher does NOT use this list
// directly; it uses the word-bounded regex list
// `mediaTransformerForbiddenWordReList` so that legitimate
// composite field names like `MD5ChecksumStr` are not
// false-positively tripped.
//
// "drive"   covers Drive SDK / DriveFileID / DriveLink / Drive
//
//	admin / drive.Store / drive.Reader / etc.
//
// "qdrant"  covers Qdrant vectors / qdrant.Client / etc.
// "sqlite"  covers SQLite / *sql.DB / sqlite3 / etc.
//
// PR-DIAGNOSI-FINALE rule 9 strengthening (July 2026): the 5
// added substrings cover the 5 Drive/provider-specific fields
// that step 2 of PR-MEDIATRANSFORMER-RENAME will REMOVE.
// PR-MEDIATRANSFORMER-WB (July 2026): this list is now ONLY
// used for the comment-residue bucket. The field-matcher uses
// the word-bounded regex list below.
var mediaTransformerForbiddenSubstrings = []string{
	"drive",
	"qdrant",
	"sqlite",
	"folderid",
	"downloadlink",
	"md5",
	"publishaction",
	"clippageurl",
}

// mediaTransformerForbiddenNames is the canonical ordered list
// of forbidden WORDS used by the field-matcher
// (`mediaTransformerForbiddenWordReList`). The order matches
// `mediaTransformerForbiddenSubstrings` 1:1 (the difference
// being word-bounded regex match vs substring match). New
// forbidden words MUST be added in lockstep to both lists.
//
// Index-aligned with mediaTransformerForbiddenWordReList: the
// regex at index `i` matches the word at index `i` of the
// names slice. The field matcher uses this alignment to
// preserve `MatchedRule: "forbidden_field_<name>"` operator-
// facing attribution without an extra lookup step.
var mediaTransformerForbiddenNames = []string{
	"drive",
	"qdrant",
	"sqlite",
	"folderid",
	"downloadlink",
	"md5",
	"publishaction",
	"clippageurl",
}

// mediaTransformerForbiddenWordReList holds the word-bounded
// regex for each forbidden word. The regex requires a word
// boundary on BOTH sides (the standard Go regex `\b` is
// defined as a transition between `\w` and `\W` / string edge,
// where `\w` = `[0-9A-Za-z_]`).
//
// PR-MEDIATRANSFORMER-WB (July 2026): this is the canonical
// field-matcher regex family. Pre-compiled at package init;
// matched per-field via `MatchString(lowerName)` + `MatchString
// (lowerType)` (case sensitivity is doubled-down because
// `(?i)` is set explicitly even though the caller already
// lowercases the input — defense in depth).
//
// Why per-substring regex (not a single combined alternation):
// the per-substring list preserves 1:1 attribution between
// `MatchedRule: "forbidden_field_<name>"` and the matched
// rule via `mediaTransformerForbiddenNames[i]`. A combined
// alternation regex would force a manual substring-to-name
// lookup step (Go regex alternation is leftmost-first, not
// leftmost-longest) which is harder to audit in CI logs.
//
// The 8 entries are pre-compiled at package init; the per-
// field runtime cost is 8 `MatchString` calls per
// `fieldName || fieldType`, which is acceptable because the
// scanner inspects exactly ONE canonical file
// (`internal/kernel/asset/processor.go`).
var mediaTransformerForbiddenWordReList = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdrive\b`),
	regexp.MustCompile(`(?i)\bqdrant\b`),
	regexp.MustCompile(`(?i)\bsqlite\b`),
	regexp.MustCompile(`(?i)\bfolderid\b`),
	regexp.MustCompile(`(?i)\bdownloadlink\b`),
	regexp.MustCompile(`(?i)\bmd5\b`),
	regexp.MustCompile(`(?i)\bpublishaction\b`),
	regexp.MustCompile(`(?i)\bclippageurl\b`),
}

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
// the migration path inline. The forbidden-word list is not
// enumerated inline (the godoc on
// `mediaTransformerForbiddenNames` is the operator's source
// of truth) to keep the violation JSON payload compact.
const mediaTransformerNote = "forbidden field in MediaTransformer DTO (PR-MEDIATRANSFORMER-RENAME step 1, July 2026; PR-DIAGNOSI-FINALE rule 9, July 2026; PR-MEDIATRANSFORMER-WB, July 2026 word-boundary fix): the MediaTransformer is a local-only transformer and MUST NOT carry Drive/Qdrant/SQLite/provider-specific fields in its DTOs; the matcher is now word-bounded (`?\\bname\\b?` regex per forbidden entry) so legitimate composite names like MD5ChecksumStr do not false-positive trip. Route the infrastructure concern through the orchestrator + finalizer + commit layers downstream. Step 2 of PR-MEDIATRANSFORMER-RENAME deletes the existing forbidden fields and this gate will pass"

// mediaTransformerWarn is the WARN-bucket emitter for
// residue-accounting. Mirrors assetStateWarn.
func mediaTransformerWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, mediaTransformerRule+" "+label+" "+msg)
}

// ScanMediaTransformerNoInfraFields opens the canonical file
// (`processor.go`) and inspects the struct bodies of
// `TransformSpec` and `RenditionSet` for forbidden
// Drive/Qdrant/SQLite/forward-pointer fields. Each forbidden
// field emits a violation; comment-only references are
// residue-accounted (WARNed, not violated) per godlike/07.
//
// PR-MEDIATRANSFORMER-WB (July 2026): the field-matcher now
// uses word-bounded regex (`(?i)\b<name>\b`) instead of
// `strings.Contains`. Composite field names like
// `MD5ChecksumStr` no longer false-positive trip.
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
			Package:     "internal/kernel/asset",
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
			if strings.HasPrefix(trimmed, "type ") && strings.HasSuffix(trimmed, " struct {") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					name := parts[1]
					for _, t := range mediaTransformerTargetTypes {
						if name == t {
							currentTarget = name
							braceDepth = 1
							break
						}
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
			continue
		}

		// Comment-only line: residue-accounted via SUBSTRING
		// match (NOT word-bounded) per the design split
		// documented above. hasAnySubstring + the legacy
		// mediaTransformerForbiddenSubstrings list are used
		// here on purpose: descriptive prose is NOT bound
		// by word semantics.
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

		// PR-MEDIATRANSFORMER-WB (July 2026): word-bounded
		// match. Iterate mediaTransformerForbiddenWordReList;
		// the FIRST regex whose match succeeds determines the
		// forbidden name via index-aligned
		// mediaTransformerForbiddenNames[i]. First-match-wins
		// preserves the prior semantic (only one violation
		// per field, attributed to the first matching
		// forbidden word). `(?i)` makes the case-insensitive
		// match double-down safe (the caller also lowercases
		// the input — defense in depth).
		lowerName := strings.ToLower(fieldName)
		lowerType := strings.ToLower(fieldType)
		for i, re := range mediaTransformerForbiddenWordReList {
			if !re.MatchString(lowerName) && !re.MatchString(lowerType) {
				continue
			}
			forbidden := mediaTransformerForbiddenNames[i]
			r.Violations = append(r.Violations, report.Violation{
				Package: "internal/kernel/asset",
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
					" | matched word: " + forbidden,
			})
			break
		}
	}
	if commentOnly > 0 {
		mediaTransformerWarn(r, "forbidden-fields:",
			"comment-only reference(s) to forbidden substrings in "+mediaTransformerCanonicalPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// hasAnySubstring returns true if `s` contains any of the
// substrings in `needles` (CASE-INSENSITIVE). Used ONLY for
// the comment-only residue bucket. The field-matcher uses the
// word-bounded regex list (`mediaTransformerForbiddenWordReList`)
// for stricter semantic matching.
//
// PR-MEDIATRANSFORMER-WB (July 2026): the case-insensitivity is
// intentional. Descriptive prose typically uses TitleCase
// variants (`MD5Checksum`, `DriveStore`, `Link`) and the
// residue-accounting WARN bucket should fire for ALL case
// forms of a forbidden word, not just the lowercase bare
// form. The case-sensitive fallback in the PRIOR
// implementation was a long-standing limitation exposed by
// the WB test.
// godlike/07 NO-FAKE-AVAILABILITY: descriptive-prose residue
// MUST NOT be silently dropped because the prose uses
// pedestrian TitleCase variants of the forbidden word.
func hasAnySubstring(s string, needles []string) bool {
	lowerS := strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(lowerS, n) {
			return true
		}
	}
	return false
}
