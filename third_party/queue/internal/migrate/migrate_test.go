package migrate

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

func TestSplitStatements(t *testing.T) {
	script := "-- header comment; with semicolon\n" +
		"CREATE TABLE a (id text DEFAULT 'x;y');\n\n" +
		"CREATE TABLE b (id int); -- trailing; comment\n"
	got := splitStatements(script)
	want := []string{
		"CREATE TABLE a (id text DEFAULT 'x;y')",
		"CREATE TABLE b (id int)",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d statements, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statement %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestMigrationsOrdered(t *testing.T) {
	names, err := migrations.Names()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"001_render_jobs.sql",
		"002_render_attempts.sql",
		"003_render_artifacts.sql",
		"004_render_workers.sql",
		"005_render_events.sql",
		"006_processing_metrics.sql",
		"007_indexes.sql",
		"008_worker_heartbeats.sql",
		"009_render_plan.sql",
		"010_artifact_render_meta.sql",
		"011_idempotency_index.sql",
		"012_render_rendered_state.sql",
		"013_artifact_media_contract.sql",
		"014_chunk_parent_index.sql",
		"015_parent_finalizing_state.sql",
		"016_render_job_notifications.sql",
		"017_render_job_progress.sql",
		"018_render_telemetry.sql",
		"019_drop_render_jobs_priority.sql",
		"020_cancel_attempt_status.sql",
		"021_chronon_timing_artifact.sql",
		"022_processing_metrics_unique.sql",
		"023_drop_retry_wait_state.sql",
		"024_terminal_state_notifications.sql",
		"025_artifact_output_facts.sql",
		"026_drop_dead_job_columns.sql",
		"027_render_job_not_before.sql",
		"028_artifact_provenance.sql",
		"029_job_publication_metadata.sql",
	}
	if len(names) != len(want) {
		t.Fatalf("want %d migrations, got %d: %v", len(want), len(names), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("migration %d: want %q, got %q", i, want[i], names[i])
		}
	}
}

// TestDeadJobColumnsAreDroppedForFreshInstalls pins the schema intent in unit
// CI, which has no database (TestApplyAgainstPostgres skips without
// TEST_DATABASE_URL).
//
// The roll forward is the point: a fresh database applies 001 (which creates
// workflow_id/source_job_id) and then the later migration that removes them, so
// a NEW deployment converges on the schema an upgraded one reaches. Two ways to
// lose that, both silent, both caught here: deleting the drop migration (new
// databases keep two dead columns plus an index on the hottest table) or
// re-adding them in a later migration.
func TestDeadJobColumnsAreDroppedForFreshInstalls(t *testing.T) {
	names, err := migrations.Names()
	if err != nil {
		t.Fatal(err)
	}
	dead := []string{"workflow_id", "source_job_id"}
	droppedIn := map[string]string{}
	for _, name := range names {
		script, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(script)
		for _, column := range dead {
			if strings.Contains(text, "DROP COLUMN IF EXISTS "+column) {
				droppedIn[column] = name
			}
			for _, reintroduction := range []string{
				"ADD COLUMN " + column,
				"ADD COLUMN IF NOT EXISTS " + column,
				"COLUMN " + column + " TEXT",
			} {
				if strings.Contains(text, reintroduction) {
					t.Errorf("%s reintroduces the dead column %s (%q); 023 declared it dead and 026 removes it", name, column, reintroduction)
				}
			}
		}
	}
	for _, column := range dead {
		if droppedIn[column] == "" {
			t.Errorf("no migration drops render_jobs.%s, so a FRESH database still creates it (see 026_drop_dead_job_columns.sql)", column)
		}
	}
}
