// Package scan — percheck_deprecation_registry.go restores the validator of
// `architecture/deprecations/` that was withdrawn on 2026-09-12 together with
// the legacy `scripts/archcheck` binary.
//
// BACKGROUND. The registry is sharded: `records/*.yaml` are the source of
// truth, `index.yaml` and `audit.yaml` are derived aggregates. The comment in
// `audit.yaml` states the consequence of the withdrawal verbatim: "the counts
// below can no longer be regenerated automatically and the deprecation-record
// validator (duplicate ids, expired removal_date, required keys across
// records/*.yaml) is WITHDRAWN — nothing validates these records today."
//
// That is how the registry reached this commit with `index.yaml` saying 6
// records and `audit.yaml` saying 7.
//
// WHAT THIS GATE ASSERTS
//   - every record carries the keys a reader needs to act on it;
//   - ids are unique across shards;
//   - `removal_date` is parseable (`YYYY-MM`, `YYYY-Qn`, `YYYY-MM-DD`,
//     `never`, `n/a`);
//   - `index.yaml` totals and per-shard counts equal the records on disk;
//   - `audit.yaml` totals, status and migration-phase buckets equal the
//     records on disk.
//
// OVERDUE DEBT IS A WARNING, NOT A VIOLATION. An expired `removal_date` is
// real residue, but making it a hard error would turn every archcheck run red
// for debt that cannot be paid in the same PR. It is reported through
// `report.Warnings` (the residue-accounting surface) on every run.
//
// NO-OP CONTRACT: when `architecture/deprecations/records/` is absent the gate
// is a no-op, so a stripped checkout never reports a fabricated violation.
//
// matched rule_id: `percheck_deprecation_registry`.
package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const deprecationRegistryRule = "percheck_deprecation_registry"

// deprecationRecord is the on-disk shape of one shard entry. Unknown keys are
// allowed (notes, file_line); the required subset is checked explicitly.
type deprecationRecord struct {
	ID                string `yaml:"id"`
	OwnerCapability   string `yaml:"owner_capability"`
	ExactSymbol       string `yaml:"exact_symbol"`
	File              string `yaml:"file"`
	Replacement       string `yaml:"replacement"`
	IntroductionDate  string `yaml:"introduction_date"`
	RemovalDate       string `yaml:"removal_date"`
	TrackingIssue     string `yaml:"tracking_issue"`
	CompatibilityTest string `yaml:"compatibility_test"`
	UsageMetric       string `yaml:"usage_metric"`
	Status            string `yaml:"status"`
	MigrationPhase    string `yaml:"migration_phase"`
}

type deprecationShard struct {
	Deprecations []deprecationRecord `yaml:"deprecations"`
}

type deprecationIndex struct {
	TotalRecords int `yaml:"total_records"`
	Records      []struct {
		Bucket string `yaml:"bucket"`
		File   string `yaml:"file"`
		Count  int    `yaml:"count"`
	} `yaml:"records"`
}

type deprecationAudit struct {
	Audit struct {
		TotalRecords     int            `yaml:"total_records"`
		ByStatus         map[string]int `yaml:"by_status"`
		ByMigrationPhase map[string]int `yaml:"by_migration_phase"`
	} `yaml:"audit"`
}

// deprecationRequiredFields is the subset a reader needs to understand what is
// deprecated, where, why, how to replace it and when it goes away.
var deprecationRequiredFields = []struct {
	name string
	get  func(deprecationRecord) string
}{
	{"id", func(record deprecationRecord) string { return record.ID }},
	{"owner_capability", func(record deprecationRecord) string { return record.OwnerCapability }},
	{"exact_symbol", func(record deprecationRecord) string { return record.ExactSymbol }},
	{"file", func(record deprecationRecord) string { return record.File }},
	{"replacement", func(record deprecationRecord) string { return record.Replacement }},
	{"introduction_date", func(record deprecationRecord) string { return record.IntroductionDate }},
	{"removal_date", func(record deprecationRecord) string { return record.RemovalDate }},
	{"tracking_issue", func(record deprecationRecord) string { return record.TrackingIssue }},
	{"compatibility_test", func(record deprecationRecord) string { return record.CompatibilityTest }},
	{"usage_metric", func(record deprecationRecord) string { return record.UsageMetric }},
	{"status", func(record deprecationRecord) string { return record.Status }},
	{"migration_phase", func(record deprecationRecord) string { return record.MigrationPhase }},
}

var (
	deprecationMonthRe = regexp.MustCompile(`^(\d{4})-(\d{2})$`)
	deprecationDayRe   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	deprecationQuartRe = regexp.MustCompile(`^(\d{4})-q([1-4])$`)
)

// deprecationDeadline converts a removal_date into the last day it is still
// on time. The second return is false for "no deadline" values and for values
// this function cannot parse; the caller distinguishes the two by re-reading
// the raw string.
func deprecationDeadline(raw string) (time.Time, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "never", "n/a", "not applicable", "tbd":
		return time.Time{}, false
	}
	if match := deprecationDayRe.FindStringSubmatch(value); match != nil {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	}
	if match := deprecationMonthRe.FindStringSubmatch(value); match != nil {
		parsed, err := time.Parse("2006-01", value)
		if err != nil {
			return time.Time{}, false
		}
		return parsed.AddDate(0, 1, -1), true
	}
	if match := deprecationQuartRe.FindStringSubmatch(value); match != nil {
		year := 0
		quarter := 0
		if _, err := fmt.Sscanf(value, "%d-q%d", &year, &quarter); err != nil {
			return time.Time{}, false
		}
		endMonth := time.Month(quarter * 3)
		return time.Date(year, endMonth+1, 0, 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

func deprecationIsOpenEnded(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "never", "n/a", "not applicable", "tbd":
		return true
	}
	return false
}

func deprecationStatusBucket(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "removed", "in_progress", "keep":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "other"
	}
}

func deprecationViolation(r *report.Report, file, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File:        file,
		MatchedRule: deprecationRegistryRule,
		Rule:        deprecationRegistryRule,
		Severity:    string(report.SeverityError),
		Note:        note,
	})
}

func deprecationWarning(r *report.Report, note string) {
	r.Warnings = append(r.Warnings, deprecationRegistryRule+": "+note)
}

func readDeprecationYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

// ScanDeprecationRegistry validates the sharded deprecation registry.
func ScanDeprecationRegistry(root string, _ *policy.Policy, r *report.Report) {
	base := filepath.Join(root, "architecture", "deprecations")
	shards, err := filepath.Glob(filepath.Join(base, "records", "*.yaml"))
	if err != nil || len(shards) == 0 {
		return
	}
	sort.Strings(shards)

	total := 0
	perBucket := map[string]int{}
	statusCounts := map[string]int{}
	phaseCounts := map[string]int{}
	seen := map[string]string{}
	now := time.Now()

	for _, shardPath := range shards {
		relShard, relErr := filepath.Rel(root, shardPath)
		if relErr != nil {
			relShard = shardPath
		}
		bucket := strings.TrimSuffix(filepath.Base(shardPath), ".yaml")

		var shard deprecationShard
		if err := readDeprecationYAML(shardPath, &shard); err != nil {
			deprecationViolation(r, relShard, "cannot parse shard: "+err.Error())
			continue
		}
		for _, record := range shard.Deprecations {
			total++
			perBucket[bucket]++

			for _, field := range deprecationRequiredFields {
				if strings.TrimSpace(field.get(record)) == "" {
					deprecationViolation(
						r, relShard,
						fmt.Sprintf("record %q is missing required key %q", record.ID, field.name),
					)
				}
			}

			if previous, duplicate := seen[record.ID]; duplicate {
				deprecationViolation(
					r, relShard,
					fmt.Sprintf("duplicate id %q (also in %s)", record.ID, previous),
				)
			} else {
				seen[record.ID] = relShard
			}

			statusCounts[deprecationStatusBucket(record.Status)]++
			phase := strings.TrimSpace(record.MigrationPhase)
			if phase == "" {
				phase = "not applicable"
			}
			phaseCounts[phase]++

			if deprecationIsOpenEnded(record.RemovalDate) {
				continue
			}
			deadline, ok := deprecationDeadline(record.RemovalDate)
			if !ok {
				deprecationViolation(
					r, relShard,
					fmt.Sprintf("record %q has unparseable removal_date %q", record.ID, record.RemovalDate),
				)
				continue
			}
			status := strings.ToLower(strings.TrimSpace(record.Status))
			if status != "removed" && status != "keep" && now.After(deadline) {
				deprecationWarning(
					r,
					fmt.Sprintf("record %q is overdue: removal_date %s passed on %s",
						record.ID, record.RemovalDate, deadline.Format("2006-01-02")),
				)
			}
		}
	}

	checkDeprecationIndex(r, base, total, perBucket)
	checkDeprecationAudit(r, base, total, statusCounts, phaseCounts)
}

func checkDeprecationIndex(r *report.Report, base string, total int, perBucket map[string]int) {
	indexPath := filepath.Join(base, "index.yaml")
	var index deprecationIndex
	if err := readDeprecationYAML(indexPath, &index); err != nil {
		deprecationViolation(r, "architecture/deprecations/index.yaml", "cannot read index: "+err.Error())
		return
	}
	if index.TotalRecords != total {
		deprecationViolation(r, "architecture/deprecations/index.yaml",
			fmt.Sprintf("total_records is %d, records on disk are %d", index.TotalRecords, total))
	}
	indexed := map[string]int{}
	for _, entry := range index.Records {
		indexed[entry.Bucket] = entry.Count
		if perBucket[entry.Bucket] != entry.Count {
			deprecationViolation(r, "architecture/deprecations/index.yaml",
				fmt.Sprintf("bucket %q count is %d, records on disk are %d",
					entry.Bucket, entry.Count, perBucket[entry.Bucket]))
		}
	}
	for bucket, count := range perBucket {
		if _, ok := indexed[bucket]; !ok && count > 0 {
			deprecationViolation(r, "architecture/deprecations/index.yaml",
				fmt.Sprintf("bucket %q (%d records) is missing from the index", bucket, count))
		}
	}
}

func checkDeprecationAudit(
	r *report.Report,
	base string,
	total int,
	statusCounts map[string]int,
	phaseCounts map[string]int,
) {
	auditPath := filepath.Join(base, "audit.yaml")
	var audit deprecationAudit
	if err := readDeprecationYAML(auditPath, &audit); err != nil {
		deprecationViolation(r, "architecture/deprecations/audit.yaml", "cannot read audit: "+err.Error())
		return
	}
	if audit.Audit.TotalRecords != total {
		deprecationViolation(r, "architecture/deprecations/audit.yaml",
			fmt.Sprintf("total_records is %d, records on disk are %d", audit.Audit.TotalRecords, total))
	}
	compareDeprecationCounts(r, "audit.yaml by_status", audit.Audit.ByStatus, statusCounts)
	compareDeprecationCounts(r, "audit.yaml by_migration_phase", audit.Audit.ByMigrationPhase, phaseCounts)
}

func compareDeprecationCounts(r *report.Report, label string, declared, actual map[string]int) {
	keys := make([]string, 0, len(declared)+len(actual))
	for key := range declared {
		keys = append(keys, key)
	}
	for key := range actual {
		if _, ok := declared[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if declared[key] != actual[key] {
			deprecationViolation(r, "architecture/deprecations/audit.yaml",
				fmt.Sprintf("%s[%q] is %d, records on disk are %d",
					label, key, declared[key], actual[key]))
		}
	}
}
