package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// defaultArtifactKind is used when the artifact does not declare a kind.
const defaultArtifactKind = "segment"

// insertArtifact persists the artifact metadata and links it to the job via
// render_jobs.artifact_id. It is called inside the Complete transaction so the
// job state, attempt, event and artifact are committed atomically.
func insertArtifact(ctx context.Context, tx *sql.Tx, jobID string, a model.Artifact) error {
	kind := a.Kind
	if kind == "" {
		kind = defaultArtifactKind
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO render_artifacts
		    (id, job_id, kind, storage_key, artifact_url, sha256, mime_type, size_bytes,
		     width, height, fps_num, fps_den, frame_count, duration_us,
		     profile_id, copy_eligible, codec, codec_profile, closed_gop, first_frame_keyframe,
		     backend, chronon_version, drive_file_id, drive_link, container, pixel_format, audio_streams,
		     chronon_timing_storage_key, chronon_timing_url, chronon_timing_sha256,
		     chronon_timing_size_bytes, chronon_timing_content_type, output_facts, provenance)
		VALUES
		    ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33::jsonb,$34::jsonb)
		ON CONFLICT (id) DO UPDATE SET
		    drive_file_id = EXCLUDED.drive_file_id,
		    drive_link    = EXCLUDED.drive_link`,
		a.ID, jobID, kind, a.StorageKey, nullIfEmpty(a.ArtifactURL), nullIfEmpty(a.ArtifactHash),
		nullIfEmpty(a.ContentType), a.SizeBytes, a.Width, a.Height, a.FPSNum, a.FPSDen,
		a.FrameCount, a.DurationUS, nullIfEmpty(a.ProfileID), a.CopyEligible,
		nullIfEmpty(a.Codec), nullIfEmpty(a.CodecProfile), a.ClosedGOP, a.FirstFrameKeyframe,
		nullIfEmpty(a.Backend), nullIfEmpty(a.ChrononVersion), nullIfEmpty(a.DriveFileID), nullIfEmpty(a.DriveLink),
		nullIfEmpty(a.Container), nullIfEmpty(a.PixelFormat), a.AudioStreams,
		nullIfEmpty(a.ChrononTimingStorageKey), nullIfEmpty(a.ChrononTimingURL), nullIfEmpty(a.ChrononTimingSHA256),
		a.ChrononTimingSizeBytes, nullIfEmpty(a.ChrononTimingContentType), jsonFacts(a.OutputFacts), jsonProvenance(a.Provenance))
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET artifact_id = $2
		WHERE id = $1`, jobID, a.ID)
	return err
}

// jsonFacts serializes the complete structural certification for the JSONB
// column. nil stays NULL (a legacy worker that did not certify the fact set),
// never an empty object.
func jsonFacts(facts *model.OutputFacts) any {
	if facts == nil {
		return nil
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return nil
	}
	return string(raw)
}

func jsonProvenance(provenance *model.ArtifactProvenance) any {
	if provenance == nil {
		return nil
	}
	raw, err := json.Marshal(provenance)
	if err != nil {
		return nil
	}
	return string(raw)
}

func insertRenderTelemetry(ctx context.Context, tx *sql.Tx, jobID, attemptID string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var doc struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode chronon telemetry: %w", err)
	}
	if doc.Schema == "" {
		return fmt.Errorf("chronon telemetry schema is missing")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO render_telemetry (job_id, attempt_id, schema, telemetry)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (job_id, attempt_id) DO UPDATE SET schema=EXCLUDED.schema, telemetry=EXCLUDED.telemetry`,
		jobID, nullIfEmpty(attemptID), doc.Schema, raw)
	return err
}

func insertProcessingMetrics(ctx context.Context, tx *sql.Tx, jobID, attemptID string, values map[string]float64) error {
	if len(values) == 0 {
		return nil
	}
	// Filter empty names once; keep value+unit paired.
	type row struct {
		name  string
		value float64
		unit  string
	}
	rows := make([]row, 0, len(values))
	for name, value := range values {
		if name == "" {
			continue
		}
		rows = append(rows, row{name: name, value: value, unit: metricUnit(name)})
	}
	if len(rows) == 0 {
		return nil
	}
	// Build a single INSERT with N value tuples — one round-trip instead of N.
	// Use ON CONFLICT on the unique (job_id, attempt_id, metric_name) added
	// in migration 022 so retries update rather than duplicate.
	//
	// The tuple list is built ONCE and reused by the fallback below. It used to
	// be rendered by two hand-copied loops, so the fallback had to stay
	// character-for-character in sync with the primary statement (a drifted copy
	// binds the wrong number of placeholders and fails at the driver, not at
	// review). strings.Builder also keeps the per-metric cost flat: the previous
	// `query += ...` reallocated the whole statement once per metric.
	const insertPrefix = "INSERT INTO processing_metrics (job_id, attempt_id, metric_name, metric_value, unit) VALUES "
	tuples := &strings.Builder{}
	tuples.Grow(len(rows) * 26)
	args := make([]any, 0, len(rows)*5)
	for i, r := range rows {
		if i > 0 {
			tuples.WriteString(", ")
		}
		base := i*5 + 1
		fmt.Fprintf(tuples, "($%d, $%d, $%d, $%d, $%d)", base, base+1, base+2, base+3, base+4)
		args = append(args, jobID, nullIfEmpty(attemptID), r.name, r.value, r.unit)
	}
	query := insertPrefix + tuples.String() +
		" ON CONFLICT (job_id, attempt_id, metric_name) DO UPDATE SET metric_value = EXCLUDED.metric_value, unit = EXCLUDED.unit"
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		// Fallback when the unique constraint hasn't been migrated yet:
		// plain multi-VALUES insert without ON CONFLICT still batches but
		// may duplicate on retry — acceptable until migration lands. The
		// duplicates are the same (job_id, attempt_id, metric_name) rows the
		// primary statement would upsert, so the fallback stays visible in
		// the log instead of passing as a clean success.
		if strings.Contains(err.Error(), "no unique or exclusion constraint") || strings.Contains(err.Error(), "ON CONFLICT") {
			log.Printf("WARN postgres: processing_metrics upsert unavailable (%v); falling back to a plain batch insert (duplicate metrics are possible until migration 022 lands)", err)
			if _, err2 := tx.ExecContext(ctx, insertPrefix+tuples.String(), args...); err2 != nil {
				return fmt.Errorf("insert processing metrics batch: %w", err2)
			}
			return nil
		}
		return fmt.Errorf("insert processing metrics batch: %w", err)
	}
	return nil
}

// metricUnit preserves the unit encoded by the canonical metric name.
//
// It has exactly two jobs, and both are pinned by tests:
//
//  1. DECLARED names (renderinggen/internal/metricnames) — the worker OWNS the
//     vocabulary, and every declared name must project to its declared unit.
//     The queue module cannot import the worker (the dependency runs
//     worker→queue), so this cross-module projection is pinned by
//     metric_unit_projection_test.go, which reads the worker's declaration and
//     asserts the derivation here agrees with it name by name.
//  2. UNDECLARED names — a payload from a producer that predates the
//     vocabulary, or a projection key the worker adds before registering it.
//     Those names carry no declared unit, so the queue derives one from the
//     documented suffix table below rather than labelling every one of them
//     "count". The table is a real contract (it decides what a persisted row
//     claims), not speculation, and is exercised by
//     TestMetricUnitFallbackTableForUndeclaredNames.
//
// The rules are ordered: suffix rules first (us, ms), then content rules
// (bytes, mb, fps, ratio/percent), then the count default.
func metricUnit(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, "_us"):
		return "us"
	case strings.HasSuffix(n, "_ms"):
		return "ms"
	case strings.Contains(n, "bytes"):
		return "bytes"
	case strings.HasSuffix(n, "_mb"):
		return "mb"
	case strings.Contains(n, "fps"):
		return "fps"
	case strings.Contains(n, "ratio"), strings.HasSuffix(n, "_percent"):
		return "ratio"
	default:
		return "count"
	}
}

// getArtifact loads a single artifact by ID.
func getArtifact(ctx context.Context, db *sql.DB, id string) (*model.Artifact, error) {
	row, err := queryArtifactRow(ctx, db, id)
	if err != nil {
		return nil, err
	}
	a, err := row.toModel(id)
	if err != nil {
		return nil, err
	}
	if err := loadArtifactMetrics(ctx, db, row.jobID, a, id); err != nil {
		return nil, err
	}
	loadArtifactTelemetry(ctx, db, row.jobID, a)
	return a, nil
}
