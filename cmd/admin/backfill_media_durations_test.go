// cmd/admin/backfill_media_durations_test.go — contract tests for the
// canonical duration backfill precedence and report.
//
// Pins:
//   - classifyDurationAsset separates already-known (probe vs provider),
//     missing, corrupt-zero and corrupt-negative without ever fabricating a
//     value;
//   - the report tallies every outcome deterministically;
//   - the row selector includes local-only assets (not just Drive-backed)
//     and only widens to already-known rows under --force.
package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	_ "github.com/mattn/go-sqlite3"
)

func clipWithDuration(d time.Duration, durationSource string) *asset.Asset {
	a := &asset.Asset{Duration: d}
	if durationSource != "" {
		a.SetMetadataString("duration_source", durationSource)
	}
	return a
}

func TestClassifyDurationAsset(t *testing.T) {
	tests := []struct {
		name string
		clip *asset.Asset
		want durationBackfillState
	}{
		{"nil asset is missing", nil, durationStateMissing},
		{"positive probe provenance is known probe", clipWithDuration(10*time.Second, "probe"), durationStateKnownProbe},
		{"positive legacy ffprobe provenance is known probe", clipWithDuration(10*time.Second, "ffprobe_backfill"), durationStateKnownProbe},
		{"positive provider provenance is known provider", clipWithDuration(10*time.Second, "provider_metadata"), durationStateKnownProvider},
		{"positive declared fallback is known provider", clipWithDuration(10*time.Second, "declared_fallback"), durationStateKnownProvider},
		{"positive unprovenanced duration is known provider", clipWithDuration(10*time.Second, ""), durationStateKnownProvider},
		{"negative duration is corrupt negative", clipWithDuration(-1*time.Second, ""), durationStateNegative},
		{"zero with known provenance tag is corrupt zero", clipWithDuration(0, "probe"), durationStateInvalidZero},
		{"zero with provider provenance tag is corrupt zero", clipWithDuration(0, "provider_metadata"), durationStateInvalidZero},
		{"zero without provenance tag is missing", clipWithDuration(0, ""), durationStateMissing},
		{"zero with unknown provenance tag is missing", clipWithDuration(0, "bogus"), durationStateMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDurationAsset(tt.clip); got != tt.want {
				t.Fatalf("classifyDurationAsset() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDurationBackfillReport(t *testing.T) {
	var r durationBackfillReport
	r.AssetsTotal = 5
	// One per outcome kind.
	r.Count(durationBackfillOutcome{Kind: "already_known"})
	r.Count(durationBackfillOutcome{Kind: "provider_metadata"})
	r.Count(durationBackfillOutcome{Kind: "probed_local"})
	r.Count(durationBackfillOutcome{Kind: "probed_drive"})
	r.Count(durationBackfillOutcome{Kind: "still_unknown"})
	r.Count(durationBackfillOutcome{Kind: "invalid_zero_duration"})
	r.Count(durationBackfillOutcome{Kind: "negative_duration"})

	want := map[string]int{
		"already_known":         1,
		"provider_metadata":     1,
		"probed_local":          1,
		"probed_drive":          1,
		"still_unknown":         1,
		"invalid_zero_duration": 1,
		"negative_duration":     1,
	}
	got := map[string]int{
		"already_known":         r.AlreadyKnown,
		"provider_metadata":     r.ProviderMetadata,
		"probed_local":          r.ProbedLocal,
		"probed_drive":          r.ProbedDrive,
		"still_unknown":         r.StillUnknown,
		"invalid_zero_duration": r.InvalidZeroDuration,
		"negative_duration":     r.NegativeDuration,
	}
	for k, wantCount := range want {
		if gotCount := got[k]; gotCount != wantCount {
			t.Fatalf("report.%s = %d, want %d", k, gotCount, wantCount)
		}
	}
	if r.AssetsTotal != 5 {
		t.Fatalf("AssetsTotal = %d, want 5", r.AssetsTotal)
	}
}

func newDurationBackfillTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		media_type TEXT NOT NULL DEFAULT '',
		lifecycle_state TEXT NOT NULL DEFAULT '',
		index_state TEXT NOT NULL DEFAULT '',
		drive_file_id TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER,
		parent_folder_id TEXT NOT NULL DEFAULT '',
		drive_folder_id TEXT NOT NULL DEFAULT '',
		folder_id TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create media_assets: %v", err)
	}
	return db
}

func insertDurationBackfillRow(t *testing.T, db *sql.DB, id, mediaType, lifecycle, indexState, driveID, localPath string, durationMS *int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO media_assets
		(id, media_type, lifecycle_state, index_state, drive_file_id, local_path, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, mediaType, lifecycle, indexState, driveID, localPath, durationMS,
	); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func int64p(v int64) *int64 { return &v }

func TestSelectDurationBackfillRows_DefaultMissingOnly(t *testing.T) {
	db := newDurationBackfillTestDB(t)
	insertDurationBackfillRow(t, db, "local-only", "video", "ACTIVE", "INDEXED", "", "/clips/a.mp4", int64p(0))
	insertDurationBackfillRow(t, db, "drive-only", "clip", "active", "indexed", "d1", "", int64p(0))
	insertDurationBackfillRow(t, db, "negative", "video", "ACTIVE", "INDEXED", "d2", "", int64p(-1))
	insertDurationBackfillRow(t, db, "already-known", "video", "ACTIVE", "INDEXED", "d3", "", int64p(5000))
	insertDurationBackfillRow(t, db, "image", "image", "ACTIVE", "INDEXED", "d4", "", int64p(0))
	insertDurationBackfillRow(t, db, "inactive", "video", "INACTIVE", "INDEXED", "d5", "", int64p(0))
	insertDurationBackfillRow(t, db, "no-source", "video", "ACTIVE", "INDEXED", "", "", int64p(0))

	rows, err := selectDurationBackfillRows(context.Background(), db, "", "", 0, false)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	for _, want := range []string{"local-only", "drive-only", "negative"} {
		if !got[want] {
			t.Fatalf("default select missing %q (local+drive only, duration<=0)", want)
		}
	}
	for _, excluded := range []string{"already-known", "image", "inactive", "no-source"} {
		if got[excluded] {
			t.Fatalf("default select must exclude %q", excluded)
		}
	}
}

func TestSelectDurationBackfillRows_ForceIncludesKnown(t *testing.T) {
	db := newDurationBackfillTestDB(t)
	insertDurationBackfillRow(t, db, "already-known", "video", "ACTIVE", "INDEXED", "d3", "", int64p(5000))
	insertDurationBackfillRow(t, db, "missing", "video", "ACTIVE", "INDEXED", "d1", "", int64p(0))

	rows, err := selectDurationBackfillRows(context.Background(), db, "", "", 0, true)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	if !got["already-known"] || !got["missing"] {
		t.Fatalf("--force must include already-known rows alongside missing: %v", got)
	}
}
