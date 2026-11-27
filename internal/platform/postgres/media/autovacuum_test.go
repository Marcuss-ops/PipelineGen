package media_test

import (
	"database/sql"
	"strings"
	"testing"
)

// DB-06: the high-churn media tables must carry explicit autovacuum
// thresholds; the default 20% scale factor never fires on them.
func TestMediaAutovacuumSettingsApplied(t *testing.T) {
	db := newMediaTestDB(t)
	for _, table := range []string{
		"media_assets",
		"outbox_events",
		"asset_text_tracks",
		"asset_text_track_segments",
	} {
		var options sql.NullString
		if err := db.QueryRow(
			`SELECT array_to_string(reloptions, ',') FROM pg_class WHERE oid = $1::regclass`, table,
		).Scan(&options); err != nil {
			t.Fatalf("read reloptions for %s: %v", table, err)
		}
		if !options.Valid {
			t.Errorf("%s has no reloptions after migration 017", table)
			continue
		}
		for _, want := range []string{
			"autovacuum_vacuum_scale_factor=0.02",
			"autovacuum_vacuum_insert_scale_factor=0.02",
			"autovacuum_analyze_scale_factor=0.01",
		} {
			if !strings.Contains(options.String, want) {
				t.Errorf("%s reloptions %q missing %q", table, options.String, want)
			}
		}
	}
}
