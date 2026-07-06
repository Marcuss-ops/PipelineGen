// Package main — archcheck standard check functions.
//
// checks.go owns the standard ratchet/focused check functions that
// runner.go orchestrates. These are the "Wave 14-19" checks — API
// infrastructure imports, database/sql gate, migration YAML validation,
// ownership YAML validation, Python legacy writer gate, application→
// infrastructure edges, and cross-capability imports.
//
// The check functions are consumed by runner.go (runFocusedChecks /
// runRatchetChecks). Phase 0 rules live in phase0_checks.go.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

func checkAPIInfrastructureImports() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"allowed":    0,
		"stale":      0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-l",
		`github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/`,
		"internal/api",
		"--glob", "*.go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			actual := []string{}
			allowlist, allowErr := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
			if allowErr != nil {
				return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", allowErr)}
			}
			staleAllowlist := bl.SubtractSet(allowlist, actual)
			stats["allowed"] = len(allowlist)
			stats["stale"] = len(staleAllowlist)
			stats["violations"] = len(staleAllowlist)
			var violations []string
			for _, stale := range staleAllowlist {
				violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
			}
			return stats, violations
		}
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: rg failed: %v", err)}
	}
	actual := bl.NormalizePaths(splitNonEmpty(string(out)))

	allowlist, err := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", err)}
	}

	staleAllowlist := bl.SubtractSet(allowlist, actual)
	violations := bl.SubtractSet(actual, allowlist)
	for _, stale := range staleAllowlist {
		violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
	}
	sort.Strings(violations)
	stats["actual"] = len(actual)
	stats["allowed"] = len(allowlist)
	stats["stale"] = len(staleAllowlist)
	stats["violations"] = len(violations)
	return stats, violations
}

// checkApplicationToInfrastructure is the Wave 19 (June 2026) Portland
// import-edge counter for the operator-pasted "Dependency Rules" spec.
//
// Vocabulary mapping (PR1 does NOT rename directories — would invalidate
// the active Wave 14-18 ratchet):
//   - capabilities = internal/application/<cap>
//   - platform     = internal/infrastructure/<sub>
//   - kernel       = internal/domain/<x>
//   - app          = internal/app
//
// Target after Wave 15-18 hardening: 0. For the FIRST PR the function
// reports COUNTS in the `Checks` map and emits NO violations, so the
// CI stays GREEN while operators see the surface area before any
// hard-gate promotion (PR2+: violations fail ratchet unless the edge
// is allowlisted in docs/migrations/application-infrastructure-imports-allowlist.txt).
//
// The reverse direction (infrastructure -> application) is allowed
// (composition-only bridge — Wave 15 PR4d). The `cmd -> app ->
// capabilities -> kernel` direction is enforced by the existing
// rules (pkg_to_internal, domain_to_application, domain_to_infrastructure,
// application_to_api, application_to_database_sql).
func checkApplicationToInfrastructure() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-l",
		`"github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/`,
		"internal/application",
		"--glob", "*.go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkApplicationToInfrastructure: rg failed: %v", err)}
	}
	actual := bl.NormalizePaths(splitNonEmpty(string(out)))
	stats["actual"] = len(actual)
	// PR1 observation-only: no violations emitted (would break active
	// Wave 14-18 ratchet). The edge-counter stays zero-promotion until
	// PR3 promotes the rule to hard-gate via bl.SubtractSet(actual, allowlist)
	// (see Wave 19 description in architecture/current.yaml).
	return stats, nil
}

// checkCrossCapabilityImport is the Wave 19 (June 2026) edge counter
// for `internal/application/<capA>/` -> `internal/application/<capB>/`.
//
// Per the operator-pasted Dependency Rules spec, Section "Cross-capability
// communication": "Capability A must not import Capability B's
// transport, repository implementation, or internal service concrete."
// The preferred order is typed port > typed event > owned read model;
// direct cross-capability import is reserved for composition adapters.
//
// Target after Wave 19 hardening: 0 — except for a documented per-edge
// allowlist. PR1 reports COUNTS in the `Checks` map; no violations
// are emitted.
//
// Distinguish same-package from cross-package imports: a file at
// `internal/application/<capA>/x.go` importing
// `internal/application/<capA>/y` is a SAME-package reference (legal
// under any layout) and DOES NOT count. Only when the source file's
// capability differs from the imported capability does the edge
// contribute to the counter.
func checkCrossCapabilityImport() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-n",
		`github\.com/Marcuss-ops/PipelineGen/internal/application/`,
		"internal/application",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkCrossCapabilityImport: rg failed: %v", err)}
	}

	capabilities := applicationCapabilities()
	pairCount := 0
	seenPairs := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// rg -n emits `path:linenum:matched_text`. SplitN with limit 3
		// gives us [path, linenum, content] — robust against path
		// strings that could in theory contain additional colons
		// (and a safer shape than strings.IndexByte for any future
		// rg output-format change). The "content" slice contains the
		// matched substring (the import string itself for our pattern).
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		src := parts[0]
		content := parts[2]
		srcCap := capabilityOfFile(src, capabilities)
		importCap := capabilityOfImport(content, capabilities)
		if srcCap == "" || importCap == "" {
			continue
		}
		if srcCap == importCap {
			continue
		}
		pairKey := srcCap + "->" + importCap
		if seenPairs[pairKey] {
			continue
		}
		seenPairs[pairKey] = true
		pairCount++
	}
	stats["actual"] = pairCount
	// PR1 observation-only (no violations). Operators can read the
	// pair count as a summary statistic; the full edge list (file-
	// level) is intentionally deferred to PR2 alongside the ratchet
	// baseline — see Wave 19 exit_gate in architecture/current.yaml.
	return stats, nil
}

// applicationCapabilities returns the set of capability sub-package
// names under internal/application/, derived from the filesystem at
// archcheck startup. Adds/removes of capability directories are picked
// up automatically without list edits (Wave 19 PR2-3 acceptance).
//
// Both focused and ratchet modes call this function and observe the
// same set (no drift between modes) because the underlying fs read is
// deterministic for a given checkout.
//
// Non-capability directories are filtered out: hidden directories
// (leading "."), well-known non-cap sub-paths (testdata, mocks,
// helpers). The filter is intentionally narrow — when in doubt, treat
// a directory as a capability so PR2's ratchet gate catches unintended
// drift at the (srcCap == importCap) filter layer.
//
// Edge cases:
//   - os.ReadDir error: a stderr warning is emitted and an empty set
//     returned. This surfaces the symptom (zero counts) immediately to
//     operators instead of silently degrading to a stale hardcoded map.
//     The ratchet gate stays GREEN because 0 < baseline is the natural
//     state. The next operator step is to fix the filesystem path.
//   - No matches at all (empty internal/application): same as above —
//     operators see a zero count instead of a poisoned 22-entry map.
//
// PR2-3 (June 2026) replaces the pre-PR2 hardcoded 22-entry map. The
// prior hook comment ("do not forget when promoting this rule to hard
// gate") is RESOLVED — the divergence risk (silent under-detect when
// new capability dirs are added) no longer exists because
// applicationCapabilities() re-reads the filesystem on every invocation.
func applicationCapabilities() map[string]bool {
	const appPath = "internal/application"
	out := map[string]bool{}
	entries, err := os.ReadDir(appPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: applicationCapabilities: read %s: %v (returning empty set; check filesystem mount and operator access)\n", appPath, err)
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Filter: hidden directories (e.g. .git) are never capabilities.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Filter: well-known non-capability sub-paths.
		switch name {
		case "testdata", "mocks", "helpers":
			continue
		}
		out[name] = true
	}
	return out
}

// capabilityOfFile returns the capability name for a Go file under
// `internal/application/<cap>/...`. Returns "" for files OUTSIDE that
// layout (e.g. architecture metadata, scripts directory).
func capabilityOfFile(relPath string, caps map[string]bool) string {
	const prefix = "internal/application/"
	if !strings.HasPrefix(relPath, prefix) {
		return ""
	}
	rest := relPath[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // file directly in internal/application/ root
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return "" // unknown sub-package — classification is best-effort
	}
	return candidate
}

// capabilityOfImport returns the capability name for an import line
// shaped like `github.com/Marcuss-ops/PipelineGen/internal/application/<cap>/...`.
// Returns "" for non-matching imports.
func capabilityOfImport(importLine string, caps map[string]bool) string {
	const marker = "github.com/Marcuss-ops/PipelineGen/internal/application/"
	idx := strings.Index(importLine, marker)
	if idx < 0 {
		return ""
	}
	rest := importLine[idx+len(marker):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // import of internal/application/ root (no Go file there, but defensive)
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return ""
	}
	return candidate
}

func checkDatabaseSQLGate() (map[string]int, []string) {
	stats := map[string]int{
		"actual":      0,
		"baseline":    len(databaseSQLLegacyBaseline),
		"regressions": 0,
	}
	out, err := exec.Command("rg", "-ln", `"database/sql"`,
		"internal/api",
		"internal/application",
		"internal/domain",
		"--type", "go",
	).Output()
	if err != nil && !(execErrIsNoMatch(err)) {
		stats["regressions"] = -1
		return stats, []string{fmt.Sprintf("checkDatabaseSQLGate: rg failed: %v", err)}
	}

	actual := bl.NormalizePaths(splitNonEmpty(string(out)))
	baseSet := bl.NormalizePaths(databaseSQLLegacyBaseline)
	added := bl.SubtractSet(actual, baseSet)
	removed := bl.SubtractSet(baseSet, actual)
	stats["actual"] = len(actual)
	stats["regressions"] = len(added)

	var violations []string
	for _, path := range added {
		violations = append(violations, "new database/sql import in api/application/domain: "+path)
	}
	if len(removed) > 0 {
		stats["baseline"] = len(baseSet) - len(removed)
	}
	return stats, violations
}

func execErrIsNoMatch(err error) bool {
	if err == nil {
		return false
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode() == 1
	}
	return false
}

func loadAllowlist(path string) ([]string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (allowlist file is required; see docs/migrations/api-infrastructure-imports-allowlist.txt)", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return bl.NormalizePaths(out), nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// databaseSQLLegacyBaseline captures the pre-gate db/sql surface that still
// exists in api/application/domain and is being shrunk incrementally.
var databaseSQLLegacyBaseline = []string{
	"internal/api/middleware/middleware_auth_test.go",
	"internal/application/assets/artifacts/clips_adapter.go",
	"internal/application/assets/artifacts/finalizer_test.go",
	"internal/application/assets/artifacts/repository.go",
	"internal/application/assets/artifacts/resolvers/resolvers.go",
	"internal/application/assets/ingest/adapter_clip.go",
	"internal/application/assets/maintenance/deep_cleanup.go",
	"internal/application/assets/maintenance/run_cleanup.go",
	"internal/application/assets/maintenance/service.go",
	"internal/application/assets/monitor/channel_monitor.go",
	"internal/application/assets/providers/artlist/assetrepo_integration_test.go",
	"internal/application/assets/providers/artlist/search_cache.go",
	"internal/application/assets/providers/artlist/service.go",
	"internal/application/assets/providers/artlist/service_test.go",
	"internal/application/books/service.go",
	"internal/application/images/google_generate.go",
	"internal/application/jobs/outbox/delivery.go",
	"internal/application/jobs/outbox/registry.go",
	"internal/application/jobs/service_test.go",
	"internal/application/scripts/batch_persistence_test.go",
	"internal/application/scripts/gemmamemory/adapters.go",
	"internal/application/scripts/gemmamemory/stub_test.go",
	"internal/application/voiceover/groups_resolver_test.go",
	"internal/application/voiceover/service.go",
	"internal/application/youtube/assetrepo_integration_test.go",
	"internal/domain/asset/assets.go",
	"internal/domain/asset/dedup.go",
	"internal/domain/asset/list_clips.go",
	"internal/domain/asset/locations.go",
	"internal/domain/asset/processing.go",
	"internal/domain/asset/scan.go",
	"internal/domain/asset/store_core.go",
	"internal/domain/asset/tags.go",
	"internal/domain/asset/utility.go",
	"internal/domain/asset/versions.go",
}

// checkMigrationYAML validates that every `status: done` wave in
// architecture/current.yaml carries `verified_zero: true`.
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

var subwavePattern = regexp.MustCompile(`^\s*-\s+id:\s+\S+`)

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

var ownershipPathPattern = regexp.MustCompile(`(?m)^\s+(?:owner|location):\s+([^#\n]+)`)

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

type pythonLegacyRule struct {
	Path     string
	Patterns []string
}

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
