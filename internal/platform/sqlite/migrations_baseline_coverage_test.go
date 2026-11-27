package sqlite

import "testing"

func TestParseBaselineCovers(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"directive", "-- 000_baseline_268.sql\n-- database: all\n-- baseline-covers: 298\n\nCREATE TABLE x(id);", 298},
		{"absent", "-- 000_baseline_267.sql\n-- database: all\n\nCREATE TABLE x(id);", 0},
		{"malformed", "-- baseline-covers: nope\nCREATE TABLE x(id);", 0},
		{"non-positive", "-- baseline-covers: 0\nCREATE TABLE x(id);", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBaselineCovers([]byte(tc.content)); got != tc.want {
				t.Fatalf("parseBaselineCovers = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBaselineThresholdUsesCoverageDirective(t *testing.T) {
	baseline := migrationFile{
		version:        0,
		filename:       "000_baseline_268.sql",
		baselineCovers: 298,
	}
	if got := baselineThresholdForMigrations([]migrationFile{baseline}, "primary"); got != 298 {
		t.Fatalf("baseline threshold = %d, want 298 (coverage directive wins over filename 268)", got)
	}

	filenameOnly := migrationFile{version: 0, filename: "000_baseline_267.sql"}
	if got := baselineThresholdForMigrations([]migrationFile{filenameOnly}, "primary"); got != 267 {
		t.Fatalf("baseline threshold without directive = %d, want 267", got)
	}
}

func TestBaselineThresholdForAppliedRecordUsesAppliedFilename(t *testing.T) {
	// A database bootstrapped from 000_baseline_267.sql must keep applying
	// 268..298 even though the current corpus carries a 268 dump that already
	// covers them: the sentinel names the window that database actually has.
	record := appliedRecord{filename: "000_baseline_267.sql"}
	if got := baselineThresholdForAppliedRecord(record, nil); got != 267 {
		t.Fatalf("applied legacy baseline threshold = %d, want 267", got)
	}

	current := migrationFile{version: 0, filename: "000_baseline_268.sql", baselineCovers: 298}
	if got := baselineThresholdForAppliedRecord(appliedRecord{filename: current.filename}, []migrationFile{current}); got != 298 {
		t.Fatalf("applied current baseline threshold = %d, want 298", got)
	}

	if got := baselineThresholdForAppliedRecord(appliedRecord{filename: "001_core.sql"}, nil); got != 0 {
		t.Fatalf("non-baseline record threshold = %d, want 0", got)
	}
}
