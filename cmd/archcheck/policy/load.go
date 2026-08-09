// Package policy loads the enforced top-level keys from architecture/policy.yaml.
package policy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type fieldBinding struct {
	Field    string
	Consumer string
	List     bool
	Apply    func(*Policy, string) error
}

var policyBindings = map[string]fieldBinding{
	"max_files_per_package":           intBinding("MaxFilesPerPackage", "scan.ScanPackages", func(p *Policy, n int) { p.MaxFilesPerPackage = n }),
	"max_lines_per_file":              intBinding("MaxLinesPerFile", "scan.ScanPackages", func(p *Policy, n int) { p.MaxLinesPerFile = n }),
	"max_lines_per_file_strict":       intBinding("MaxLinesPerFileStrict", "scan.ScanFileLinesStrict", func(p *Policy, n int) { p.MaxLinesPerFileStrict = n }),
	"max_lines_strict_allowlist":      stringBinding("MaxLinesStrictAllowlist", "scan.ScanFileLinesStrict", func(p *Policy, v string) { p.MaxLinesStrictAllowlist = v }),
	"cmd_main_max_lines":              intBinding("CmdMainMaxLines", "scan.ScanCommandBinaries", func(p *Policy, n int) { p.CmdMainMaxLines = n }),
	"max_constructor_deps":            intBinding("MaxConstructorDeps", "scan.ScanConstructors", func(p *Policy, n int) { p.MaxConstructorDeps = n }),
	"max_struct_deps":                 intBinding("MaxStructDeps", "scan.ScanStructDeps", func(p *Policy, n int) { p.MaxStructDeps = n }),
	"max_clip_ingest_pipeline_fields": intBinding("MaxClipIngestPipelineFields", "scan.ScanStructDeps clip-ingest exception", func(p *Policy, n int) { p.MaxClipIngestPipelineFields = n }),
	"max_warnings":                    nonNegativeIntBinding("MaxWarnings", "runner warning budget", func(p *Policy, n int) { p.MaxWarnings = n }),
	"forbidden_top_level_dirs":        stringListBinding("ForbiddenTopLevelDirs", "scan.ScanForbiddenDirs", func(p *Policy, v []string) { p.ForbiddenTopLevelDirs = v }),
	"kernel_subzones":                 stringListBinding("KernelSubzones", "scan.ScanKernelSubzoneHints + ScanKernelSubzoneIntegrity", func(p *Policy, v []string) { p.KernelSubzones = v }),
	"capabilities":                    stringListBinding("Capabilities", "report policy snapshot and target-tree checks", func(p *Policy, v []string) { p.Capabilities = v }),
	"canonical_application_areas":     canonicalApplicationAreasBinding(),
	"platform_subzones":               stringListBinding("PlatformSubzones", "report policy snapshot and target-tree checks", func(p *Policy, v []string) { p.PlatformSubzones = v }),
	"legacy_internal_roots":           stringListBinding("LegacyInternalRoots", "scan.ScanUnknownInternalRoots", func(p *Policy, v []string) { p.LegacyInternalRoots = v }),
	"target_internal_roots":           stringListBinding("TargetInternalRoots", "scan.ScanUnknownInternalRoots", func(p *Policy, v []string) { p.TargetInternalRoots = v }),
	"data_ownership_doc":              stringBinding("DataOwnershipDoc", "scan.ScanOwnershipDoc", func(p *Policy, v string) { p.DataOwnershipDoc = v }),
	"legacy_policy_doc":               stringBinding("LegacyPolicyDoc", "scan.ScanLegacyPolicyDoc", func(p *Policy, v string) { p.LegacyPolicyDoc = v }),
	"ci_gates_doc":                    stringBinding("CIGatesDoc", "scan.ScanCIGatesDoc", func(p *Policy, v string) { p.CIGatesDoc = v }),
	"agent_playbook_doc":              stringBinding("AgentPlaybookDoc", "scan.ScanAgentPlaybookDoc", func(p *Policy, v string) { p.AgentPlaybookDoc = v }),
	"removal_doc":                     stringBinding("RemovalDoc", "scan.ScanRemovalDoc", func(p *Policy, v string) { p.RemovalDoc = v }),
	"known_grandfathered":             stringListBinding("KnownGrandfathered", "report grandfathered_known", func(p *Policy, v []string) { p.KnownGrandfathered = v }),
	"stale_prose_paths":               stringListBinding("StaleProseStems", "scan.ScanStaleProsePaths", func(p *Policy, v []string) { p.StaleProseStems = v }),
	"hard_gates":                      stringListBinding("HardGates", "runner hard-gate escalation", func(p *Policy, v []string) { p.HardGates = v }),
}

// These top-level sections are documentation or are consumed by legacy shell
// gates. They are accepted explicitly, rather than falling through an unknown
// key path.
var acceptedDocumentSections = map[string]struct{}{
	"app_infra_bridge_ratchet": {}, // Wave-22 ratchet baseline (doc-only; full enforcement deferred per Phase-2 promotion; canonical owner = architecture/policy.yaml::app_infra_bridge_ratchet, godlike/06 SSOT)
	"prometheus_boundary":      {},
	"cross_project_refs":       {},
	"lint_gates":               {},
	"wave_qdrant_005d_hygiene": {},
	"debt_budget":              {},
}

// Load parses only top-level policy keys. Unknown top-level keys fail closed;
// nested documentation under explicitly accepted sections is left to its
// owning consumer.
func Load(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	p := &Policy{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	activeList := ""
	activeListGotValue := false
	seenKeys := make(map[string]struct{})
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := stripComment(sc.Text())
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if activeList != "" {
			if bullet, ok := collectBullet(line); ok {
				binding := policyBindings[activeList]
				if err := appendListValue(p, binding, bullet); err != nil {
					return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
				}
				activeListGotValue = true
				continue
			}
			if isIndented(line) {
				return nil, fmt.Errorf("%s:%d: expected a YAML bullet under %q", path, lineNo, activeList)
			}
			activeList = ""
			activeListGotValue = false
		}

		// Nested mappings and block-scalar bodies belong to an explicitly
		// accepted top-level section. Only top-level declarations participate
		// in the enforced Policy model.
		if isIndented(line) {
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%s:%d: expected top-level key: value", path, lineNo)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if binding, ok := policyBindings[key]; ok {
			if _, seen := seenKeys[key]; seen {
				return nil, fmt.Errorf("%s:%d: duplicate architecture policy key %q", path, lineNo, key)
			}
			seenKeys[key] = struct{}{}
			if binding.List && val == "" {
				activeList = key
				continue
			}
			if err := binding.Apply(p, val); err != nil {
				return nil, fmt.Errorf("%s:%d: key %q: %w", path, lineNo, key, err)
			}
			continue
		}
		if _, ok := acceptedDocumentSections[key]; ok {
			if _, seen := seenKeys[key]; seen {
				return nil, fmt.Errorf("%s:%d: duplicate architecture policy key %q", path, lineNo, key)
			}
			seenKeys[key] = struct{}{}
			continue
		}
		return nil, fmt.Errorf("%s:%d: unknown architecture policy key %q", path, lineNo, key)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if activeList != "" && !activeListGotValue {
		return nil, fmt.Errorf("%s: list key %q has no values", path, activeList)
	}
	return p, nil
}

func nonNegativeIntBinding(field, consumer string, set func(*Policy, int)) fieldBinding {
	return fieldBinding{Field: field, Consumer: consumer, Apply: func(p *Policy, raw string) error {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 0 {
			return fmt.Errorf("expected a non-negative integer, got %q", raw)
		}
		set(p, n)
		return nil
	}}
}

func intBinding(field, consumer string, set func(*Policy, int)) fieldBinding {
	return fieldBinding{Field: field, Consumer: consumer, Apply: func(p *Policy, raw string) error {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n <= 0 {
			return fmt.Errorf("expected a positive integer, got %q", raw)
		}
		set(p, n)
		return nil
	}}
}

func stringBinding(field, consumer string, set func(*Policy, string)) fieldBinding {
	return fieldBinding{Field: field, Consumer: consumer, Apply: func(p *Policy, raw string) error {
		v := strings.Trim(strings.TrimSpace(raw), "\"'")
		if v == "" {
			return fmt.Errorf("value cannot be empty")
		}
		set(p, v)
		return nil
	}}
}

func stringListBinding(field, consumer string, set func(*Policy, []string)) fieldBinding {
	return fieldBinding{Field: field, Consumer: consumer, List: true, Apply: func(p *Policy, raw string) error {
		values := splitTrim(raw)
		if len(values) == 0 {
			return fmt.Errorf("list cannot be empty")
		}
		set(p, values)
		return nil
	}}
}

func appendListValue(p *Policy, binding fieldBinding, value string) error {
	if binding.Field == "CanonicalApplicationAreas" {
		if err := validateCanonicalApplicationArea(value); err != nil {
			return err
		}
		for _, existing := range p.CanonicalApplicationAreas {
			if existing == value {
				return fmt.Errorf("duplicate canonical application area %q", value)
			}
		}
	}
	field := reflect.ValueOf(p).Elem().FieldByName(binding.Field)
	if !field.IsValid() || field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String {
		return fmt.Errorf("binding %s is not a []string Policy field", binding.Field)
	}
	field.Set(reflect.Append(field, reflect.ValueOf(value)))
	return nil
}

func stripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.Trim(strings.TrimSpace(part), "\"'"); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func canonicalApplicationAreasBinding() fieldBinding {
	return fieldBinding{
		Field:    "CanonicalApplicationAreas",
		Consumer: "scan.ScanCanonicalApplicationInfrastructureImports",
		List:     true,
		Apply: func(p *Policy, raw string) error {
			values := splitTrim(raw)
			if len(values) == 0 {
				return fmt.Errorf("list cannot be empty")
			}
			seen := make(map[string]struct{}, len(values))
			for _, value := range values {
				if err := validateCanonicalApplicationArea(value); err != nil {
					return err
				}
				if _, ok := seen[value]; ok {
					return fmt.Errorf("duplicate canonical application area %q", value)
				}
				seen[value] = struct{}{}
			}
			p.CanonicalApplicationAreas = values
			return nil
		},
	}
}

func validateCanonicalApplicationArea(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || filepath.IsAbs(value) {
		return fmt.Errorf("canonical application area must be a relative path, got %q", value)
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean != value || clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || clean == ".." {
		return fmt.Errorf("canonical application area must be normalized and cannot traverse parents, got %q", value)
	}
	if clean != "internal/application" && !strings.HasPrefix(clean, "internal/application/") {
		return fmt.Errorf("canonical application area must be under internal/application, got %q", value)
	}
	return nil
}

func collectBullet(line string) (string, bool) {
	if !isIndented(line) {
		return "", false
	}
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "-") {
		return "", false
	}
	value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), "\"'")
	return value, value != ""
}
