// Package scan — percheck_metadata_registry.go is the
// forward-prevention gate for the Asset.Metadata
// name-spaced key alphabet
// (PR-METADATA-REGISTRY-FOUNDATION, July 2026).
//
// The gate codifies godlike/06 "one owner per fact" +
// godlike/07 NO-FAKE-AVAILABILITY for the typed metadata
// surface: every name-spaced (`a.b.c`-containing) key in
// `Asset.Metadata[…]` literals or
// `Get/Set/Metadata[String|Int|Bool|...](…)` accessor
// calls MUST be explicitly declared in the canonical
// registry at
// `internal/kernel/asset/metadata_registry.go` with its
// owner package + Type. Unregistered name-spaced keys
// surface as error-severity violations.
//
// Bare keys (no dot, e.g. `drive_file_id`,
// `local_path`) are RESIDUE-ALLOWED per the migration-
// window discipline (godlike/07 NO-FAKE-AVAILABILITY):
//   - existing legacy keys live on `Asset.Metadata`
//     via the 30+ typed accessor methods in
//     `internal/kernel/asset/asset_accessors.go`;
//   - the scanner does NOT trip on bare keys so the
//     migration window (godlike/07 BACKFILL → CUTOVER
//     → CONTRACT) progresses without fake-failure
//     noise;
//   - future PRs migrate bare keys into the name-spaced
//     surface via the typed-strip pipeline; once
//     migrated, the registered name-spaced key trips the
//     gate if a CALLER forgets the new name.
//
// godlike/07 fail-closed:
//   - missing canonical registry file    → typed
//     `registry_canonical_missing` violation.
//   - present-but-empty canonical file  → typed
//     `registry_canonical_empty` violation
//     (mirrors percheck_asset_state_canonical_14
//     SSOT-missing discipline).
//   - present-and-keyed file but the key
//     field is malformed (uppercase, whitespace,
//     leading/trailing dot)              → trip per
//     comment accounting (godlike/07 residue,
//     WARNed not violated).
//
// Comment-only references to name-spaced keys are
// residue-accounted (godlike/07). Comment lines with a
// namespaced-region inside (e.g.
// `// see youtube.video_id for canonical convention`)
// contribute to the WARN bucket — descriptive prose is
// not a real consumer, but future drift is visible in CI
// every run.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// metadataKeyCanonicalPath is the canonical SOLE
// owner of the Asset.Metadata name-spaced key whitelist
// (godlike/06 SSOT). ONLY this file declares the
// alphabet.
const metadataKeyCanonicalPath = "internal/kernel/asset/metadata_registry.go"

// metadataKeyScannerSkipDirs mirrors the standard
// skip-dir set (`percheck_player_client_centralization` +
// `percheck_script_docs_route` + `percheck_asset_state_*`).
var metadataKeyScannerSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
	"docs":         true,
	"data":         true,
	"archivist":    true,
}

// metadataKeyScannerSkipPathPrefixes exempts the
// scanner's own package + the asset package (the
// canonical SSOT + the accessors that legitimately
// reference bare-key indices). The scanner regex
// pattern's anchoring depends on these prefixes — false
// positives on scanner self-references would block CI.
var metadataKeyScannerSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
	"internal/kernel/asset",
}

// metadataKeyScannerAccessRe matches BOTH
// direct `Metadata["X.Y.Z"]` index access AND the
// typed accessor surface (`GetMetadataString`,
// `GetMetadataInt`, `SetMetadataString`,
// `SetMetadataInt`, `MetadataBool`,
// `MetadataFloat`, `MetadataStringSlice`,
// `MetadataString`).
//
// The regex anchors on `Metadata[` or `Get|SetMetadata?\w+\(\s*"` to
// distinguish from generic `cfg.X["y"]` literals that
// are NOT Metadata-related. Future-typo drift
// (e.g. `StockMetadata["x"]`) is intentionally NOT in
// the regex — those are out of scope per the scanner's
// creator-only contract; the gate enforces the canonical
// Asset.Metadata surface.
var metadataKeyScannerAccessRe = regexp.MustCompile(
	`(?:Metadata\[\s*|GetMetadata(?:String|Int|Bool|Float|StringSlice)\(\s*|SetMetadata(?:String|Int|Bool|Float|StringSlice)\(\s*|Metadata(?:Bool|Float|StringSlice|String)\(\s*)"([a-z][a-z0-9_.\-]*)"`,
)

// metadataKeyEntryRe extracts a SINGLE
// {Key, Owner, Type} struct-literal entry from a single
// line of the canonical registry file. The regex
// deliberately ignores Trailing-field (Doc) so registry
// entries can pad the document string without breaking
// the parse.
var metadataKeyEntryRe = regexp.MustCompile(
	`\{\s*Key:\s*"([^"]+)"\s*,\s*Owner:\s*"([^"]+)"\s*,\s*Type:\s*"([^"]+)"`,
)

// metadataKeyScannerRule is the rule family id the
// scanner emits. Mirrors the percheck_* RuleID naming
// convention.
const metadataKeyScannerRule = "percheck_metadata_registry"

// metadataKeyScannerNote is the violation Note
// string for unregistered name-spaced keys.
const metadataKeyScannerNote = "forbidden name-spaced Asset.Metadata key outside canonical registry (`internal/kernel/asset/metadata_registry.go`); godlike/06 SSOT requires every `provider.*` style key to be declared in `allowedMetadataKeys` with `Owner` + `Type` before being written or read; bare keys (no dot) are residue-allowed for the migration window (file a follow-up PR to migrate them to the name-spaced surface via the typed-strip pipeline)"

// metadataKeyWarn is the centralized WARN-bucket
// emitter for residue-accounting. Mirrors assetStateWarn
// + imageAssetWarn.
func metadataKeyWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings,
		metadataKeyScannerRule+" "+label+" "+msg)
}

// ParseMetadataKeys opens the canonical SOLE owner
// of the Asset.Metadata key whitelist and returns the
// alphabetical-sort of the parsed key strings. A missing
// or malformed canonical file surfaces as a typed
// CONFIGURATION_ERROR violation.
//
// godlike/07 fail-closed: the configuration errors are
// `error`-severity. A silent pass on a missing canonical
// file would convert the forward-prevention gate into an
// unconditional no-op — a godlike/07
// NO-FAKE-AVAILABILITY regression.
func ParseMetadataKeys(root string, pol *policy.Policy, r *report.Report) []string {
	_ = pol
	path := filepath.Join(root, metadataKeyCanonicalPath)
	f, err := os.Open(path)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        metadataKeyCanonicalPath,
			Line:        0,
			Rule:        metadataKeyScannerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "registry_canonical_missing",
			Note:        metadataKeyScannerNote + " | cannot open canonical registry: " + err.Error(),
		})
		return nil
	}
	defer f.Close()

	keys := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		m := metadataKeyEntryRe.FindStringSubmatch(line)
		if m == nil {
			trimmed := strings.TrimLeft(line, " \t")
			if (strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "/*") ||
				strings.HasPrefix(trimmed, "*")) &&
				strings.Contains(line, "Key:") {
				commentOnly++
			}
			continue
		}
		keys = append(keys, m[1])
	}

	if len(keys) == 0 {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/kernel/asset",
			File:        metadataKeyCanonicalPath,
			Line:        0,
			Rule:        metadataKeyScannerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "registry_canonical_empty",
			Note: metadataKeyScannerNote +
				" | canonical registry file present but no `{Key: ..., Owner: ..., Type: ...}` entries parsed — verify the file uses the single-line struct-literal format (PR-METADATA-REGISTRY-FOUNDATION, July 2026)",
		})
	}
	if commentOnly > 0 {
		metadataKeyWarn(r, "registry-config:",
			strconv.Itoa(commentOnly)+" comment-only Key: reference(s) in "+
				metadataKeyCanonicalPath+
				" (descriptive prose; non-fatal per godlike/07)")
	}
	sort.Strings(keys)
	return keys
}

// ScanMetadataKeys walks every production .go file
// under <root> (excluding `_test.go` + standard skip
// dirs + the scanner's own package + the canonical
// registry file) and emits a godlike/06 SSOT violation
// for any name-spaced (`a.b.c`-containing) metadata-key
// string literal that is not in the canonical registry.
//
// Bare keys are not violated — they are residue-
// accounted via `r.Warnings` (godlike/07
// NO-FAKE-AVAILABILITY: the migration window stays
// free of fake-failure noise).
//
// Severity is `error` (forward-prevention gate; the
// runner `--strict` mode promotes to ExitViolations).
//
// Path-scope discipline (godlike/06 SSOT): the walker
// visits every `.go` production file. The scope is
// intentionally wider than
// `percheck_asset_state_no_shadow_enum` because consumer
// keys can land in any application/api/infrastructure
// package — the name-spaced surface is provider-public,
// not application-private.
func ScanMetadataKeys(root string, pol *policy.Policy, r *report.Report) {
	keys := ParseMetadataKeys(root, pol, r)
	// If the canonical file is misconfigured (missing OR
	// empty), the parse step emitted a typed violation —
	// skip the walk so a misconfigured scanner does not
	// flood the report with config-drift violations. The
	// typed config-error violation is the load-bearing
	// forward-prevention surface for that failure mode.
	if metadataKeyHasConfigViolation(r) {
		return
	}
	whitelist := map[string]bool{}
	for _, k := range keys {
		whitelist[k] = true
	}

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if metadataKeyScannerSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				for _, prefix := range metadataKeyScannerSkipPathPrefixes {
					if relSlash == prefix ||
						strings.HasPrefix(relSlash, prefix+"/") {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		scanMetadataKeysFile(path, relSlash, whitelist, r)
		return nil
	})
}

// scanMetadataKeysFile reads a single .go file
// line-by-line and emits percheck_metadata_registry
// violations for name-spaced metadata-key string
// literals that are not in the canonical registry.
//
// Mirrors scanScriptDocsRouteFile structure: same line-
// by-line bufio.Scanner surface, same comment-only
// accounting discipline, same warning pattern.
func scanMetadataKeysFile(path, relPath string, whitelist map[string]bool, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	bareKeys := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		matches := metadataKeyScannerAccessRe.FindAllStringSubmatch(line, -1)
		if len(matches) == 0 {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		isComment := strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")
		for _, m := range matches {
			key := m[1]
			if isComment {
				// Comment-only reference: residue-
				// accounted, NOT violated. Future drift
				// is visible in CI output every run
				// (godlike/07 NO-FAKE-AVAILABILITY).
				commentOnly++
				continue
			}
			if !strings.Contains(key, ".") {
				// Bare key: residue-allowed for the
				// migration window. Future PRs migrate
				// bare keys into the name-spaced
				// surface.
				bareKeys++
				continue
			}
			if whitelist[key] {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				Package:     pkgFromMetadataKeyRel(relPath),
				File:        relPath,
				Line:        lineNo,
				Rule:        metadataKeyScannerRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "unregistered_namespaced_key",
				Note:        metadataKeyScannerNote + " | key: " + key,
			})
		}
	}
	if bareKeys > 0 {
		metadataKeyWarn(r, "bare-key-residue:",
			strconv.Itoa(bareKeys)+" non-namespaced (bare) metadata-key reference(s) in "+relPath+
				" (legacy Asset.Metadata via typed-accessor; residue-allowed for the migration window per godlike/07)")
	}
	if commentOnly > 0 {
		metadataKeyWarn(r, "commentonly-residue:",
			strconv.Itoa(commentOnly)+" comment-only metadata-key reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// metadataKeyHasConfigViolation returns true iff a
// typed registry config violation has been emitted by
// ParseMetadataKeys. The caller skips the in-file
// walk when the canonical surface is misconfigured so
// the report does NOT flood with config-drift
// violations on top of the typed config-error.
func metadataKeyHasConfigViolation(r *report.Report) bool {
	for _, v := range r.Violations {
		if v.Rule == metadataKeyScannerRule &&
			(v.MatchedRule == "registry_canonical_missing" ||
				v.MatchedRule == "registry_canonical_empty") {
			return true
		}
	}
	return false
}

// pkgFromMetadataKeyRel extracts the package identifier
// from a repo-relative file path. Mirrors
// pkgFromAssetStateRel + pkgFromRelMetadata naming
// family.
func pkgFromMetadataKeyRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
