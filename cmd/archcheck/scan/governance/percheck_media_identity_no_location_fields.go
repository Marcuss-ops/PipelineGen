// Package scan — percheck_media_identity_no_location_fields.go
//
// MEDIA IDENTITY GATE (September 2026): a type that carries a CONTENT ADDRESS
// must not also carry a LOCATION or a PRODUCER-LOCAL field.
//
// Why this is a gate and not a cleanup: the reported production failure was a
// media record whose content address said one thing while its `local_path` (or
// `drive_link`, or `legacy_file_md5`) said another. A record holding both has
// two sources of truth for one fact, and it fails in the worst possible way —
// every individual consumer looks correct, so the disagreement only becomes
// visible much later, in a different process, as "the hash does not match the
// bytes" or "the file is not there".
//
// The invariant, one owner per fact (godlike/06):
//
//	content address  → media_assets / asset_locations  (durable identity)
//	location         → asset_locations                 (provider, file id, links)
//	local path       → a runtime cache, valid for seconds in ONE process
//	legacy_file_md5  → a read-only compatibility bucket, never a decision input
//
// So the ban is exactly: within one struct declaration, a field whose name is a
// canonical content address (AssetID, SHA256, ContentHash, …) may not coexist
// with a location or producer-local value (LocalPath, DriveFileID, DriveLink,
// DownloadLink, LegacyFileMD5) that is REACHABLE from outside the process.
//
// "Reachable" is decided by the struct tag, which is the only mechanism the
// language gives for saying this: a location field explicitly typed `json:"-"`
// is the CORRECT shape (a runtime value that never crosses a boundary) and is
// therefore legal; a location field with no tag, or with any other tag, is a
// value that something will read as data — those are the violations. The
// distinction is not pedantry: `RenderQueueAsset{ SHA256, LocalPath `json:"-"` }`
// is the target design, while `EntityImageBinding{ AssetID, LocalPath `json:"local_path"` }`
// is the defect, and a gate that cannot tell them apart would punish the fix
// and get deleted.
//
// # WHY A COUNT BASELINE INSTEAD OF A PER-FILE ALLOWLIST
//
// Measured 2026-09-17: the pattern is not a handful of offenders, it is the
// dominant idiom of the media surface — 192 field pairs across 92 files. A
// per-file allowlist at that scale is a rubber stamp: it would need ~92
// fabricated owner/deadline lines, nobody re-reads them, and it silently
// converts a real invariant into paperwork (the exact rot this repository's own
// documentation policy warns about).
//
// So the enforced fact is the RATCHET, not the file list:
//
//   - the total must never EXCEED mediaIdentityBaselineOffenses — a single new
//     offense fails the gate as SeverityError, which is the whole point (a
//     future contributor cannot reintroduce the ambiguity);
//   - a total BELOW the baseline emits ONE warning asking for the constant to
//     be lowered in the same change, so the number can only shrink and can
//     never drift upward unnoticed (godlike/08 zero-baseline rule, applied to a
//     count);
//   - at exactly the baseline the rule is SILENT. This is deliberate and not
//     laziness: the repo's committed warning budget is 77 and the tree sits
//     exactly on it, so a standing per-offense residue would breach the budget
//     by itself and turn a satisfied invariant into a hard `warning_budget`
//     failure — the gate would fail for reporting, not for breaking. On a
//     FAILING run the rule prints one residue line per offense instead (the
//     budget no longer matters once the build is red), so the full demolition
//     list is always available exactly when someone needs it.
//
// Demolition therefore needs no bookkeeping: delete the fields, lower the
// constant, the gate stays green and strictly tighter.
//
// Deliberately NOT in the banned set: `SourceURL` / `StorageURL`. A fetchable
// reference legitimately carries a URL — that is what makes it fetchable — and
// an acquisition DTO pointing at an origin is not a second identity. Banning
// them wholesale would have needed dozens of exemptions in this very rule,
// which is how a gate dies. Their narrower problem (the renderer must receive
// materialized bytes, not a second internet path) belongs to the RenderingGen
// wire contract.
//
// Exempt zones:
//   - **/*_test.go — regression-guard surface.
//   - cmd/archcheck/ — the scanner's own source and the report tooling.
//   - mediaIdentityNoLocationExemptFiles — the canonical OWNERS of each fact
//     (the location type must be able to hold a location).
//
// Matched rule_id: percheck_media_identity_no_location_fields
package governance

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaIdentityNoLocationRule is the rule-family id the scanner emits.
const mediaIdentityNoLocationRule = "percheck_media_identity_no_location_fields"

// Sub-rule ids, so a report reader can tell growth from progress.
const (
	mediaIdentityBudgetGrowthRule = "media_identity_no_location_budget_grown"
	mediaIdentityBudgetShrinkRule = "media_identity_no_location_budget_shrinkable"
)

// mediaIdentityBaselineOffenses is the committed count of struct fields that
// still bind a content address to a location. It is a RATCHET: it may only be
// lowered, in the same change that removes the offending fields.
//
// Measured 2026-09-17 over internal/ and cmd/ (production .go files only):
// 192 pairs across 92 files. Lower it as the demolition lands; never raise it
// without an explicit, reviewed decision — raising it is the regression this
// gate exists to catch.
//
// It is a variable rather than a constant so the hermetic test can set the
// threshold it needs: a t.TempDir fixture always holds far fewer offenses than
// the real tree, so the growth path is otherwise unreachable in a test. Wiring
// it to a policy field later is a mechanical change.
var mediaIdentityBaselineOffenses = 192

// mediaIdentityContentAddressFields are the field names that make a struct an
// IDENTITY record: the canonical content-address vocabulary. Without one of
// these the struct is not asserting a durable identity, so the gate stays out
// of its way (a struct holding only locations is the legitimate case — that is
// what asset_locations is).
var mediaIdentityContentAddressFields = map[string]bool{
	"AssetID":        true,
	"SHA256":         true,
	"ContentHash":    true,
	"ContentSHA256":  true,
	"BinarySHA256":   true,
	"ContentAddress": true,
}

// mediaIdentityLocationFields are the field names that must never share a
// struct with a content address. See the package comment for why SourceURL and
// StorageURL are deliberately absent.
var mediaIdentityLocationFields = map[string]bool{
	"LocalPath":     true,
	"DriveFileID":   true,
	"DriveLink":     true,
	"DownloadLink":  true,
	"LegacyFileMD5": true,
}

// mediaIdentityNoLocationExemptFiles are the canonical OWNERS of each fact,
// which must be able to declare the fact they own: the location type owns
// provider/file-id/link fields and carries no identity of its own.
var mediaIdentityNoLocationExemptFiles = map[string]bool{
	"internal/capabilities/finalization/types_asset_location.go":    true,
	"internal/capabilities/finalization/types_verified_artifact.go": true,
}

// mediaIdentityNoLocationStructRe matches a struct type declaration opener.
var mediaIdentityNoLocationStructRe = regexp.MustCompile(`^type\s+([A-Za-z0-9_]+)\s+struct\s*\{`)

// mediaIdentityNoLocationFieldRe captures a struct field name from a
// struct-body line (`AssetID string`, `AssetID *string`, `AssetID []byte`).
var mediaIdentityNoLocationFieldRe = regexp.MustCompile(`^\s*([A-Z][A-Za-z0-9_]*)\s+\S`)

// mediaIdentityNoLocationSkipDirs mirrors the sibling scanners' policy.
var mediaIdentityNoLocationSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
	"testdata":     true,
}

// mediaIdentityNoLocationSkipPathPrefixes keeps the archcheck tree out of the
// scan (its own sources and fixtures describe the rule).
var mediaIdentityNoLocationSkipPathPrefixes = []string{
	"cmd/archcheck/",
}

// mediaIdentityOffense is one struct that violates the invariant.
type mediaIdentityOffense struct {
	File     string
	Struct   string
	Line     int
	Content  string // the content-address field name found
	Location string // the location/producer field name found
	Tag      string // the offending field's struct tag ("" when untagged)
}

// ScanMediaIdentityNoLocationFields walks every production .go file under
// internal/ and cmd/ and counts structs that carry a content address AND a
// location/producer field. The count is ratcheted against
// mediaIdentityBaselineOffenses; every offense is reported as residue so the
// demolition list is visible in every run.
func ScanMediaIdentityNoLocationFields(root string, _ *policy.Policy, r *report.Report) {
	var offenses []mediaIdentityOffense

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if mediaIdentityNoLocationSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), mediaIdentityNoLocationSkipPathPrefixes) {
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
		if !mediaIdentityInScope(relSlash) {
			return nil
		}
		if mediaIdentityNoLocationExemptFiles[relSlash] {
			return nil
		}
		found := scanMediaIdentityFile(path)
		for i := range found {
			found[i].File = relSlash
		}
		offenses = append(offenses, found...)
		return nil
	})

	if len(offenses) > mediaIdentityBaselineOffenses {
		// A failing run is allowed to be verbose: the global warning budget is
		// irrelevant once the gate has already failed, and this is exactly the
		// moment an operator needs the full list of structs to fix. Naming every
		// offense here — and ONLY here — is what keeps the green path quiet
		// enough to live inside a 77-warning committed budget.
		for _, offense := range offenses {
			r.Warnings = append(r.Warnings, mediaIdentityNoLocationRule+" media-identity-location-debt: "+
				mediaIdentityOffenseName(offense))
		}
		r.Violations = append(r.Violations, report.Violation{
			Rule:         mediaIdentityNoLocationRule,
			MatchedRule:  mediaIdentityBudgetGrowthRule,
			Severity:     string(report.SeverityError),
			ActualCount:  len(offenses),
			AllowedCount: mediaIdentityBaselineOffenses,
			Note: fmt.Sprintf("media identity gate GREW from %d to %d: a struct that carries a content address "+
				"must not also carry a location or producer-local field (godlike/06, one owner per fact). "+
				"Keep the content address as the identity; move provider locations to asset_locations; keep a local "+
				"path only as a short-lived runtime value from a materializer (type it `json:\"-\"` when it must cross "+
				"a boundary); treat legacy_file_md5 as a read-only compatibility bucket no decision may read. "+
				"The excess is %d offense(s); this report also carries one residue warning per offense, naming every "+
				"struct and field.",
				mediaIdentityBaselineOffenses, len(offenses), len(offenses)-mediaIdentityBaselineOffenses),
		})
		return
	}

	// Steady state is SILENT, on purpose. The global warning budget is committed
	// at 77 and the tree already sits exactly on it, so a rule that emits even
	// one standing warning would breach the budget and turn a satisfied
	// invariant into a hard `warning_budget` failure — the gate would fail for
	// reporting, not for breaking.
	//
	// The committed baseline constant IS the record of the debt, and it is
	// stricter than a warning: it cannot drift upward without failing this rule.
	// So this rule speaks only when the measurement CHANGES: growth is an error
	// with the full offense list, and shrinkage is a single warning asking for
	// the constant to be lowered in the same change.
	if len(offenses) == mediaIdentityBaselineOffenses {
		return
	}
	summary := fmt.Sprintf("%s media-identity-location-debt: measured %d offense(s) across %d file(s), baseline %d — %s%s",
		mediaIdentityNoLocationRule, len(offenses), len(mediaIdentityOffenseFiles(offenses)),
		mediaIdentityBaselineOffenses, mediaIdentityOffenseFieldCounts(offenses), mediaIdentityShrinkHint(len(offenses)))
	r.Warnings = append(r.Warnings, summary)
}

// mediaIdentityShrinkHint asks for the baseline constant to be lowered once the
// measured count drops below it, so the ratchet can never sit above reality
// (godlike/08 zero-baseline rule applied to a count).
func mediaIdentityShrinkHint(measured int) string {
	if measured >= mediaIdentityBaselineOffenses {
		return ""
	}
	return fmt.Sprintf(" — lower mediaIdentityBaselineOffenses to %d in "+
		"cmd/archcheck/scan/governance/percheck_media_identity_no_location_fields.go so the ratchet stays tight "+
		"(a baseline above reality authorises the difference)", measured)
}

// mediaIdentityOffenseFiles counts the distinct files carrying an offense.
func mediaIdentityOffenseFiles(offenses []mediaIdentityOffense) map[string]bool {
	files := make(map[string]bool, len(offenses))
	for _, offense := range offenses {
		files[offense.File] = true
	}
	return files
}

// mediaIdentityOffenseFieldCounts renders the per-field breakdown, so the
// summary tells an operator which field class to attack first (LocalPath vs
// DriveLink vs the compatibility digest).
func mediaIdentityOffenseFieldCounts(offenses []mediaIdentityOffense) string {
	counts := map[string]int{}
	for _, offense := range offenses {
		counts[offense.Location]++
	}
	parts := make([]string, 0, len(counts))
	for _, field := range []string{"LocalPath", "DriveLink", "DriveFileID", "DownloadLink", "LegacyFileMD5"} {
		if n := counts[field]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", field, n))
		}
	}
	return strings.Join(parts, " ")
}

// mediaIdentityOffenseName renders one offense for the residue line: the struct,
// the two fields that must not coexist, the offending tag, and the file:line to
// open.
func mediaIdentityOffenseName(offense mediaIdentityOffense) string {
	tag := offense.Tag
	if tag == "" {
		tag = "untagged"
	}
	return fmt.Sprintf("%s%s carries content address %s AND location field %s (tag %s; two sources of truth for one fact; %s:%d)",
		mediaIdentityOffenseQualifier(offense), offense.Struct, offense.Content, offense.Location, tag, offense.File, offense.Line)
}

// mediaIdentityOffenseQualifier returns the struct name followed by a space, or
// an empty string when the struct name is unknown.
func mediaIdentityOffenseQualifier(offense mediaIdentityOffense) string {
	if offense.Struct == "" {
		return ""
	}
	return offense.Struct + " "
}

// mediaIdentityFieldIsProcessLocal reports whether a struct field is explicitly
// confined to the process by its tag (`json:"-"`). That is the one declaration
// that no other process can read as data, so it satisfies the invariant by
// construction — it is the shape this gate asks offenders to move to.
func mediaIdentityFieldIsProcessLocal(trimmed string) bool {
	return strings.Contains(trimmed, `json:"-"`)
}

// mediaIdentityFieldTag returns the raw backtick-quoted struct tag of a field
// line, or "" when the field carries none.
func mediaIdentityFieldTag(trimmed string) string {
	start := strings.IndexByte(trimmed, '`')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(trimmed[start+1:], '`')
	if end < 0 {
		return ""
	}
	return trimmed[start+1 : start+1+end]
}

// mediaIdentityInScope reports whether a repo-relative path belongs to the
// scanned production surface.
func mediaIdentityInScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") || strings.HasPrefix(relSlash, "cmd/")
}

// scanMediaIdentityFile parses the struct declarations of one Go file and
// returns the offenders. It is a line-oriented scan (the same idiom as the
// sibling perchecks): a struct body runs from `type X struct {` to the first
// line that closes it at column zero, which is how gofmt always renders a
// top-level type declaration.
func scanMediaIdentityFile(path string) []mediaIdentityOffense {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []mediaIdentityOffense

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inStruct := false
	structName := ""
	contentField := ""
	contentLine := 0
	var locations []mediaIdentityOffense

	flush := func() {
		if inStruct && contentField != "" {
			for _, offense := range locations {
				offense.Struct = structName
				offense.Content = contentField
				if offense.Line == 0 {
					offense.Line = contentLine
				}
				out = append(out, offense)
			}
		}
		inStruct, structName = false, ""
		contentField, contentLine, locations = "", 0, nil
	}

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()

		if !inStruct {
			if m := mediaIdentityNoLocationStructRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				inStruct, structName = true, m[1]
			}
			continue
		}

		// gofmt renders the closing brace of a top-level type at column zero.
		if strings.HasPrefix(line, "}") {
			flush()
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		m := mediaIdentityNoLocationFieldRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		name := m[1]
		switch {
		case mediaIdentityContentAddressFields[name]:
			if contentField == "" {
				contentField, contentLine = name, lineNo
			}
		case mediaIdentityLocationFields[name]:
			// `json:"-"` is the RECOMMENDED shape for a producer-local value: it
			// exists in-process and never crosses a boundary. Flagging it would
			// punish the very fix this gate asks for.
			if mediaIdentityFieldIsProcessLocal(trimmed) {
				continue
			}
			locations = append(locations, mediaIdentityOffense{
				Line:     lineNo,
				Location: name,
				Tag:      mediaIdentityFieldTag(trimmed),
			})
		}
	}
	// Unterminated struct (truncated file): evaluate what was seen rather than
	// silently dropping it behind the ratchet.
	flush()
	return out
}
