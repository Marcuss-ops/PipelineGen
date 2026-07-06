// Package main — archcheck anti-pattern detection rules.
//
// checks_patterns.go owns the 3 anti-pattern check functions that
// detect configuration drift, metadata drift, and legacy Python
// writer behavior. These are "shape" checks (governing the shape of
// YAML files + Python patterns) rather than "graph" checks (which
// govern import edges and live in checks_imports.go and
// checks_coupling.go).
//
//   - checkMigrationYAML + subwavePattern + scanYAML +
//     topLevelWaveBlocks: validates that every `status: done` wave
//     in architecture/current.yaml carries the canonical
//     `exit_signal: true` (or the deprecated `verified_zero: true`
//     alias). The Wave 14-PR-2 transition renamed
//     `verified_zero:` → `exit_signal:` (canonical truth key) per
//     action P0-5 slice 3/4. Pre-slice-4 markers may emit a stderr
//     WARNING for the deprecated alias; from slice 4/4 onward the
//     gate HARD-FAILs on the alias.
//
//   - checkOwnershipYAML + checkOwnershipRef +
//     ownershipPathPattern: validates that every path reference in
//     architecture/ownership.generated.yaml (the aggregated view
//     rebuilt by cmd/architecture-aggregate) actually exists on disk.
//     The check is intentionally narrow — it does NOT verify the
//     contents of the referenced path, just that the filesystem
//     surface is intact.
//
//   - checkPythonLegacyWriterGate + pythonLegacyRule struct:
//     detects prohibited patterns in legacy Python writers that the
//     North Star godlike program no longer permits (sqlite3 imports
//     in embedding servers, qdrant_client/googleapiclient in
//     shell-equivalent scripts, etc.). The 4 rules below are the
//     legacy writer surface that must remain frozen until PR-D
//     retires them physically (forward-pointer).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// checkMigrationYAML validates that every `status: done` wave in
// architecture/current.yaml carries `verified_zero: true` (the
// canonical key after the Wave 14-PR-2 rename to `exit_signal:`).
//
// architecture/current.yaml is a YAML 1.2 multi-document stream
// (June 2026 PR —YAML-SEP): doc #1 = wave sequence
// (- id: 0, 14, 15, 16, 17, 18), doc #2 = post_cascade_followups,
// doc #3 = legacy_fallback_cleanup. Documents are separated by
// `---` markers at column 0. The text scanner below intentionally
// ignores `---` (topLevelWaveBlocks only matches lines that start
// with `- id:`, which only doc #1 carries; doc #2/#3 are mappings
// without sequence-item markers). Future YAML-library consumers must
// use yaml.NewDecoder(...).Decode() in a loop to read all 3 docs —
// yaml.Unmarshal only returns doc #1 (PyYAML safe_load_all returns
// all 3).
func checkMigrationYAML() (verifiedOK int, total int, violations []string) {
	const migPath = "architecture/current.yaml"
	text, err := os.ReadFile(migPath)
	if err != nil {
		return -1, 0, []string{fmt.Sprintf("checkMigrationYAML: read %s: %v", migPath, err)}
	}
	total, violations = scanYAML(string(text))
	verifiedOK = total - len(violations)
	return verifiedOK, total, violations
}

// subwavePattern matches the leading `- id: <name>` line that begins
// each top-level wave block in architecture/current.yaml. Used by
// scanYAML to detect block boundaries via subwave line match.
var subwavePattern = regexp.MustCompile(`^\s*-\s+id:\s+\S+`)

// scanYAML walks every top-level wave block in `raw` (architecture/
// current.yaml text) extracted by topLevelWaveBlocks, parses the
// (id, status, exit_signal-or-verified_zero) tuple, and emits a
// violation for any block where:
//
//   - status == "done" AND
//   - the canonical exit_signal key is absent, OR present but not
//     "true" (legacy: verified_zero not "true")
//
// Canonical truth key (action P0-5 slice 3/4) is `exit_signal:`;
// the deprecated alias `verified_zero:` is still accepted in PR1
// (June 2026) but emits a stderr WARNING. slice 4/4 will HARD-fail
// on the alias.
//
// The function is intentionally text-scanner based (no yaml.Decode
// dependency) so it works in CI before any Go YAML library bring-up
// and so it can be re-used from the shell-equivalent
// `architecture/actions/*.yaml` files that are evaluated in
// Wave 14 cleanup waves.
func scanYAML(raw string) (int, []string) {
	var (
		doneTotal  int
		violations []string
	)
	for _, b := range topLevelWaveBlocks(raw) {
		var idv, status, signal string
		for _, line := range strings.Split(b, "\n") {
			if idv != "" && subwavePattern.MatchString(line) {
				break
			}
			tabSplit := strings.SplitN(strings.TrimRight(line, "\r"), ":", 2)
			if len(tabSplit) != 2 {
				continue
			}
			key := strings.TrimSpace(tabSplit[0])
			val := strings.TrimSpace(tabSplit[1])
			switch key {
			case "id":
				if idv == "" {
					idv = val
				}
			case "status":
				if status == "" {
					status = val
				}
			case "exit_signal":
				// Canonical truth key (action P0-5 slice 3/4). Replaces the
				// deprecated `verified_zero:` alias; markers should always
				// emit `exit_signal: true|false|missing`.
				if signal == "" {
					signal = val
				}
			case "verified_zero":
				// DEPRECATED alias (action P0-5 slice 3/4). Forward-compat:
				// accept the value as `signal` ONLY if the canonical
				// `exit_signal:` key was not already seen in this wave
				// block. Emit a stderr WARNING so operators notice the
				// drift before slice 4/4 promotes the alias to hard-FAIL
				// (slice 4/4 will keep the WARNING for backward-compat but
				// also emit a violation entry, so deprecated aliases
				// never silently pass).
				if signal == "" {
					signal = val
					if idv != "" {
						fmt.Fprintf(os.Stderr, "WARNING: wave id=%s uses deprecated 'verified_zero:' field; rename to 'exit_signal:' (slice 4/4 will HARD-fail on this alias)\n", idv)
					}
				}
			}
		}
		if status != "done" {
			continue
		}
		doneTotal++
		if signal != "true" {
			signalStr := signal
			if signalStr == "" {
				signalStr = "missing"
			}
			violations = append(violations, fmt.Sprintf("wave id=%s has status=done but exit_signal=%s", idv, signalStr))
		}
	}
	sort.Strings(violations)
	return doneTotal, violations
}

// topLevelWaveBlocks slices the raw yaml text into one string per
// top-level wave block (delimited by lines starting with `- id:`).
// The function is intentionally string-based (no yaml.Decode) because
// architecture/current.yaml uses YAML 1.2 multi-document + block
// scalar idioms that break naive yaml.Unmarshal.
// See checkMigrationYAML godoc for the multi-document rationale.
func topLevelWaveBlocks(raw string) []string {
	var blocks []string
	var current strings.Builder
	inBlock := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "- id:") {
			if inBlock {
				blocks = append(blocks, current.String())
				current.Reset()
			}
			inBlock = true
		}
		if inBlock {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if inBlock {
		blocks = append(blocks, current.String())
	}
	return blocks
}

// ownershipPathPattern matches the indented `owner: ...` or
// `location: ...` lines in architecture/ownership.generated.yaml.
// The capture group excludes trailing comments (everything after
// `#`) so the post-processing ref-strip in checkOwnershipYAML can
// operate on the canonical path string.
var ownershipPathPattern = regexp.MustCompile(`(?m)^\s+(?:owner|location):\s+([^#\n]+)`)

// checkOwnershipYAML walks architecture/ownership.generated.yaml and
// emits a violation for every `owner:` / `location:` reference whose
// canonical-path form does not exist on disk. Returns the count of
// missing paths + a sorted violation slice for operator reporting.
//
// The check intentionally does NOT validate file CONTENTS (that is
// the role of compile-time `var _ Owner = (*Adapter)(nil)` pins
// elsewhere in the codebase). It ONLY validates that the path
// strings in the aggregated ownership view survive filesystem churn:
// a renamed or git-rm'd package surface MUST be reflected in the
// ownership.yaml SSOT, or the operator sees an actionable error
// here at PR-A / CI-archcheck-hard-fail time.
func checkOwnershipYAML() (int, []string) {
	const path = "architecture/ownership.generated.yaml"
	text, err := os.ReadFile(path)
	if err != nil {
		return -1, []string{fmt.Sprintf("checkOwnershipYAML: read %s: %v", path, err)}
	}
	var violations []string
	for _, match := range ownershipPathPattern.FindAllStringSubmatch(string(text), -1) {
		ref := strings.TrimSpace(match[1])
		ref = strings.Trim(ref, `"'`)
		if ref == "" || strings.HasPrefix(ref, "/") {
			continue
		}
		for _, part := range strings.Split(ref, " + ") {
			checkOwnershipRef(strings.TrimSpace(part), &violations)
		}
	}
	sort.Strings(violations)
	return len(violations), violations
}

// checkOwnershipRef classifies one `owner:` / `location:` reference
// and emits a violation if the canonical path form does not resolve
// to an existing filesystem entry. Filters:
//
//   - Multi-part refs joined with " + " (e.g. "pkg/foo + pkg/bar")
//     call this function once per part — the caller splits.
//   - Refs that contain " " (without "::") are skipped — they are
//     irregular tokens (comments, prose fragments) not file paths.
//   - Refs containing "(", "{", or "[" are skipped — they are likely
//     syntax fragments (generics, type expressions, slice/array
//     literals), not file paths. This filter is heuristic but
//     empirically tight across the current ownership.generated.yaml.
//   - The "heyavatar/" prefix is mapped to a stable external
//     URL-not-on-disk (DOCS proxy) and is intentionally skipped.
//   - Absolute paths (starting "/") are skipped — owner refs are
//     always repo-relative per the canonical policy.
//
// All other refs are stat()'d. A os.Stat error means the ref is
// stale (package was renamed/removed); a violation is emitted with
// the original ref string verbatim for operator grep.
func checkOwnershipRef(ref string, violations *[]string) {
	ref = strings.TrimSpace(strings.Trim(ref, `"'`))
	if ref == "" {
		return
	}
	if strings.Contains(ref, " ") && !strings.Contains(ref, "::") {
		return
	}
	if strings.Contains(ref, "(") || strings.Contains(ref, "{") || strings.Contains(ref, "[") {
		return
	}
	candidate := strings.SplitN(ref, "::", 2)[0]
	candidate = strings.TrimSuffix(candidate, "/")
	candidate = filepath.FromSlash(candidate)
	if candidate == "" {
		return
	}
	if strings.HasPrefix(filepath.ToSlash(candidate), "heyavatar/") {
		return
	}
	if _, err := os.Stat(candidate); err != nil {
		*violations = append(*violations, fmt.Sprintf("ownership.yaml references missing path: %s", ref))
	}
}

// pythonLegacyRule defines one legacy Python writer check: Path is
// the Python script path (relative to repo root); Patterns is the
// list of substrings that MUST NOT appear in that file. Co-located
// in checks_patterns.go (NOT in its own legacy_python.go file)
// because the canonical SSOT for this type is the rule list below —
// splitting it would split the SOLE owner of the legacy writer
// surface across two files.
type pythonLegacyRule struct {
	Path     string
	Patterns []string
}

// checkPythonLegacyWriterGate scans the 4 Python legacy writer files
// (sync_drive_qdrant.py + 3 embedding_server/{text,visual,audio}.py)
// for the prohibited patterns that the godlike/06 North Star
// invariants no longer permit:
//
//   - "sqlite3" — direct SQLite access from Python (must go through
//     the Go dispatcher via internal/jobs surface).
//   - "SentenceTransformer" — heavyweight embedding model directly
//     imported in a shell script (must live in scripts/services/
//     embedding_server typed entry).
//   - "qdrant_client" — direct Qdrant HTTP/gRPC access from a shell
//     script (must route via internal/media/vectorstore typed port).
//   - "googleapiclient" / "google.oauth2" — direct Google Drive /
//     OAuth imports (must route via internal/infrastructure/drive
//     typed entry).
//   - "/collections/" — Path-prefix matching Qdrant collection URLs
//     would indicate direct HTTP scrape (must go via vectorstore).
//
// Each rule emits ONE violation per pattern-per-file match. The
// returned stat is the total violation count; the violations slice
// is sorted for deterministic CI output.
func checkPythonLegacyWriterGate() (int, []string) {
	rules := []pythonLegacyRule{
		{
			Path: "scripts/tools/sync_drive_qdrant.py",
			Patterns: []string{
				"sqlite3",
				"SentenceTransformer",
				"qdrant_client",
				"googleapiclient",
				"google.oauth2",
				"/collections/",
			},
		},
		{
			Path:     "scripts/services/embedding_server/text.py",
			Patterns: []string{"sqlite3"},
		},
		{
			Path:     "scripts/services/embedding_server/visual.py",
			Patterns: []string{"sqlite3"},
		},
		{
			Path:     "scripts/services/embedding_server/audio.py",
			Patterns: []string{"sqlite3"},
		},
	}

	var violations []string
	for _, rule := range rules {
		raw, err := os.ReadFile(rule.Path)
		if err != nil {
			violations = append(violations, fmt.Sprintf("python legacy writer gate: read %s: %v", rule.Path, err))
			continue
		}
		text := string(raw)
		for _, pattern := range rule.Patterns {
			if strings.Contains(text, pattern) {
				violations = append(violations,
					fmt.Sprintf("python legacy writer gate: %s contains prohibited pattern %q", rule.Path, pattern))
			}
		}
	}

	sort.Strings(violations)
	return len(violations), violations
}
