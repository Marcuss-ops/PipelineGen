package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// deprecationsFile is the on-disk shape of architecture/deprecations.yaml.
// deprecationRecord is a single entry; the CI validator enforces that every
// record carries all required fields and that no two records share the same id.
type deprecationsFile struct {
	Deprecations []deprecationRecord `yaml:"deprecations"`
	Audit        auditBlock          `yaml:"audit"`
}

type deprecationRecord struct {
	ID                string `yaml:"id"`
	OwnerCapability   string `yaml:"owner_capability"`
	ExactSymbol       string `yaml:"exact_symbol"`
	File              string `yaml:"file"`
	FileLine          string `yaml:"file_line"`
	Replacement       string `yaml:"replacement"`
	IntroductionDate  string `yaml:"introduction_date"`
	RemovalDate       string `yaml:"removal_date"`
	TrackingIssue     string `yaml:"tracking_issue"`
	CompatibilityTest string `yaml:"compatibility_test"`
	UsageMetric       string `yaml:"usage_metric"`
	MigrationPhase    string `yaml:"migration_phase"`
	Status            string `yaml:"status"`
	Notes             string `yaml:"notes"`
}

type auditBlock struct {
	ManifestVersion string `yaml:"manifest_version"`
	TotalRecords    int    `yaml:"total_records"`
	ByStatus        struct {
		Removed     int `yaml:"removed"`
		InProgress  int `yaml:"in_progress"`
		Keep        int `yaml:"keep"`
	} `yaml:"by_status"`
	ByMigrationPhase map[string]int `yaml:"by_migration_phase"`
	CIGateImpact     string         `yaml:"ci_gate_impact"`
}

// requiredDeprecationFields lists every field a deprecation record MUST carry.
var requiredDeprecationFields = []string{
	"id", "owner_capability", "exact_symbol", "file", "file_line",
	"replacement", "introduction_date", "removal_date", "tracking_issue",
	"compatibility_test", "usage_metric", "migration_phase", "status",
}

// checkDeprecations validates architecture/deprecations.yaml and returns
// stats (number of records, violations count) and a list of violations.
// Violations include:
//   - duplicate deprecation IDs
//   - missing required fields
//   - records whose removal_date is in the past but status != "removed"
//   - YAML parse errors (including duplicate mapping keys detected by the parser)
func checkDeprecations() (stats map[string]int, violations []string) {
	stats = map[string]int{
		"deprecations_total":      0,
		"deprecations_violations": 0,
	}

	const path = "architecture/deprecations.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		return stats, []string{fmt.Sprintf("deprecations: read %s: %v", path, err)}
	}

	// Parse with yaml.v3. Duplicate YAML mapping keys cause a parse error
	// (yaml.v3 rejects them in strict-decode mode). We use a two-pass approach:
	// first validate the YAML structure via yaml.Node to catch duplicate keys,
	// then unmarshal into the typed struct for field-level validation.
	if dupViolations := detectDuplicateYAMLKeys(raw, path); len(dupViolations) > 0 {
		violations = append(violations, dupViolations...)
		stats["deprecations_violations"] = len(violations)
		return stats, violations
	}

	var file deprecationsFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return stats, []string{fmt.Sprintf("deprecations: parse %s: %v", path, err)}
	}

	stats["deprecations_total"] = len(file.Deprecations)

	// Check for duplicate IDs.
	seenIDs := make(map[string]int) // id → first occurrence index
	for i, rec := range file.Deprecations {
		if prevIdx, ok := seenIDs[rec.ID]; ok {
			violations = append(violations, fmt.Sprintf(
				"deprecations: duplicate id %q at records %d and %d (file %s and %s)",
				rec.ID, prevIdx+1, i+1,
				file.Deprecations[prevIdx].File, rec.File))
		} else {
			seenIDs[rec.ID] = i
		}
	}

	// Check each record for required fields and expiry.
	for i, rec := range file.Deprecations {
		label := fmt.Sprintf("record #%d (id=%q)", i+1, rec.ID)

		// Required fields.
		for _, field := range requiredDeprecationFields {
			if fieldIsEmpty(rec, field) {
				violations = append(violations, fmt.Sprintf(
					"deprecations: %s missing required field %q", label, field))
			}
		}

		// Expiry check: if removal_date is in the past and status != "removed",
		// the record is an expired deprecation that should have been resolved.
		if rec.RemovalDate != "" && rec.RemovalDate != "never" && rec.Status != "removed" {
			if removalInPast(rec.RemovalDate) {
				violations = append(violations, fmt.Sprintf(
					"deprecations: %s has removal_date=%q (in the past) but status=%q (expected \"removed\")",
					label, rec.RemovalDate, rec.Status))
			}
		}
	}

	sort.Strings(violations)
	stats["deprecations_violations"] = len(violations)
	return stats, violations
}

// fieldIsEmpty reports whether the named field on the record has its zero value.
func fieldIsEmpty(rec deprecationRecord, field string) bool {
	switch field {
	case "id":
		return rec.ID == ""
	case "owner_capability":
		return rec.OwnerCapability == ""
	case "exact_symbol":
		return rec.ExactSymbol == ""
	case "file":
		return rec.File == ""
	case "file_line":
		return rec.FileLine == ""
	case "replacement":
		return rec.Replacement == ""
	case "introduction_date":
		return rec.IntroductionDate == ""
	case "removal_date":
		return rec.RemovalDate == ""
	case "tracking_issue":
		return rec.TrackingIssue == ""
	case "compatibility_test":
		return rec.CompatibilityTest == ""
	case "usage_metric":
		return rec.UsageMetric == ""
	case "migration_phase":
		return rec.MigrationPhase == ""
	case "status":
		return rec.Status == ""
	default:
		return false
	}
}

// removalInPast returns true when the removal_date string represents a date
// that is before today. Supported formats: YYYY-MM-DD, YYYY-QN (quarter).
// Dates like "never" are handled by the caller; YYYY-QN dates are treated as
// the last day of that quarter.
func removalInPast(removalDate string) bool {
	// Quarterly: 2026-Q3 → approximate as 2026-09-30
	if strings.Contains(removalDate, "-Q") {
		parts := strings.SplitN(removalDate, "-Q", 2)
		if len(parts) != 2 {
			return false
		}
		year := parts[0]
		quarter := parts[1]
		var month string
		switch quarter {
		case "1":
			month = "03-31"
		case "2":
			month = "06-30"
		case "3":
			month = "09-30"
		case "4":
			month = "12-31"
		default:
			return false
		}
		removalDate = year + "-" + month
	}
	t, err := time.Parse("2006-01-02", removalDate)
	if err != nil {
		return false // unparseable dates are not treated as expired
	}
	return time.Now().After(t)
}

// detectDuplicateYAMLKeys walks the raw YAML document tree and reports
// any mapping node that contains duplicate keys. This catches the
// class of error where a human-edited YAML file has two `id:` keys
// or two `audit:` blocks at the same level — yaml.v3 Unmarshal silently
// keeps the last value, so without this check duplicates go undetected.
func detectDuplicateYAMLKeys(raw []byte, path string) []string {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		// If the YAML itself is malformed, Unmarshal will fail —
		// that's handled by the caller. Here we only inspect the tree.
		return nil
	}
	var violations []string
	walkYAMLForDupKeys(&root, &violations, path)
	return violations
}

func walkYAMLForDupKeys(node *yaml.Node, violations *[]string, path string) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		// MappingNode children come in key-value pairs: [key0, val0, key1, val1, ...]
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i]
			if key == nil {
				continue
			}
			if seen[key.Value] {
				*violations = append(*violations, fmt.Sprintf(
					"deprecations: duplicate YAML key %q in %s (yaml.v3 silently overwrites; fix the duplicate)",
					key.Value, path))
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		walkYAMLForDupKeys(child, violations, path)
	}
}
