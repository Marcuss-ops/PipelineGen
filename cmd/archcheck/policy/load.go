// Package policy — archcheck policy loader.
//
// policy/load.go owns the stdlib-only parser for architecture/policy.yaml.
// The format is intentionally simple: one `key: value` pair per line, with
// `#` starting a line comment (mid-line `#` also starts a comment from
// that point onward). Lists are either comma-separated on one line
// (`key: a, b, c`) or multi-line bullets under a `key:` with an empty
// value (`key:` followed by indented `- bullet` lines). Unknown keys
// are ignored for forward-compat.
//
// Phase 0: Load() returns *Policy. Phase 1+ may extend to also return
// the typed Rule / Constraint / OwnerRef entries declared in
// model.go; today's parser extracts only the scalar Policy fields
// because the YAML schema for the forward-looking types is not yet
// specified.
//
// Cross-references:
//   - architecture/policy.yaml: the on-disk format
//   - cmd/archcheck/main.go: the caller (loadPolicy → policy.Load)
package policy

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Load parses the flat key:value portions of architecture/policy.yaml.
// Multi-line list values are exposed in the report header but not
// consumed for enforcement in Phase 0. The returned *Policy is nil if
// the file cannot be opened or read; an error is returned with the
// underlying os.PathError wrapped for diagnostic context.
//
// The parser is line-based (`bufio.Scanner`, max line length 64K)
// which is sufficient for the canonical policy file (~250 lines).
// Larger policy files would need a streaming parser — not a current
// concern.
func Load(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	p := &Policy{}
	sc := bufio.NewScanner(f)
	inGrandfathered := false
	inStaleProse := false
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if inGrandfathered {
			// collect indented bullets via the shared collectBullet
			// helper (centralizes the indent + ASCII-quote trim; the
			// inStaleProse path below uses the same helper).
			if b, isBullet := collectBullet(line); isBullet {
				p.KnownGrandfathered = append(p.KnownGrandfathered, b)
				continue
			}
			inGrandfathered = false
		}

		if inStaleProse {
			// collect indented bullets via the shared collectBullet()
			// helper (handles the indent + ASCII-quote trim; see the
			// helper doc for semantics). Mirrors the inGrandfathered
			// path style.
			if b, isBullet := collectBullet(line); isBullet {
				p.StaleProseStems = append(p.StaleProseStems, b)
				continue
			}
			inStaleProse = false
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "max_files_per_package":
			p.MaxFilesPerPackage = atoiOrDefault(val, 40)
		case "max_lines_per_file":
			p.MaxLinesPerFile = atoiOrDefault(val, 500)
		case "cmd_main_max_lines":
			p.CmdMainMaxLines = atoiOrDefault(val, 200)
		case "max_constructor_deps":
			p.MaxConstructorDeps = atoiOrDefault(val, 8)
		case "max_struct_deps":
			p.MaxStructDeps = atoiOrDefault(val, 8)
		case "forbidden_top_level_dirs":
			p.ForbiddenTopLevelDirs = splitTrim(val)
		case "kernel_subzones":
			p.KernelSubzones = splitTrim(val)
		case "capabilities":
			p.Capabilities = splitTrim(val)
		case "platform_subzones":
			p.PlatformSubzones = splitTrim(val)
		case "legacy_internal_roots":
			p.LegacyInternalRoots = splitTrim(val)
		case "target_internal_roots":
			p.TargetInternalRoots = splitTrim(val)
		case "data_ownership_doc":
			p.DataOwnershipDoc = val
		case "legacy_policy_doc":
			p.LegacyPolicyDoc = val
		case "ci_gates_doc":
			p.CIGatesDoc = val
		case "agent_playbook_doc":
			p.AgentPlaybookDoc = val
		case "removal_doc":
			p.RemovalDoc = val
		case "known_grandfathered":
			if val == "" {
				inGrandfathered = true
			}
		case "stale_prose_paths":
			if val == "" {
				inStaleProse = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return p, nil
}

// atoiOrDefault parses a positive integer from s; returns def when s
// is empty, not an integer, or has surrounding whitespace that hides
// the value. Used for the four numeric Policy fields
// (MaxFilesPerPackage, MaxLinesPerFile, CmdMainMaxLines,
// MaxConstructorDeps) so a missing/garbled value in policy.yaml
// degrades to the documented default rather than os.Exit(2)-ing the
// scan.
func atoiOrDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// splitTrim splits a comma-separated scalar (e.g.
// "asset, job, script, event, identity, errors") into a slice of
// trimmed, non-empty strings. Empty entries are dropped (so a
// trailing comma does not produce a phantom element). The output
// preserves input order; the scan functions that consume the slice
// sort or hash it as needed.
func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// collectBullet parses one YAML indented-bullet line into a clean
// scalar, returning (bullet, true) when line is a bullet to append,
// or ("", false) when line has returned to top-level (calling code
// resets its in-list flag). Handles mixed quoted + unquoted YAML
// inline scalars; mirrors the original inGrandfathered block's
// semantics. Helper exists so two list-style keys don't duplicate
// the indent + dash + quote trim logic.
func collectBullet(line string) (string, bool) {
	if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
		return "", false
	}
	b := strings.Trim(strings.TrimSpace(strings.TrimLeft(line, " \t-")), "\"'")
	return b, b != ""
}
