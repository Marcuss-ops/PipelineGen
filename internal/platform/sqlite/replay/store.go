// Package replay persists the durable replay bundle registry in SQLite
// (table replay_bundles, migration 218). It is the single store for replay
// snapshots, keyed by original job id.
package replay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

var ErrNotWired = errors.New("replay sqlite adapter: not wired")

type Store struct{ db *sql.DB }

// New constructs the adapter. Fail-closed: a nil database is a construction
// error.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, ErrNotWired
	}
	return &Store{db: db}, nil
}

var _ capreplay.BundleStore = (*Store)(nil)

// Save upserts the bundle for its original job. The latest canonical
// snapshot wins (a crash between render and Save never leaves a duplicate,
// and a re-save after a renderer upgrade converges on the same row).
// Fail-closed: an invalid bundle is never persisted.
func (s *Store) Save(ctx context.Context, bundle capreplay.ReplayBundle) error {
	if err := bundle.Validate(); err != nil {
		return err
	}
	planJSON, err := json.Marshal(bundle.RenderPlan)
	if err != nil {
		return fmt.Errorf("replay: marshal render plan: %w", err)
	}
	assetsJSON, err := json.Marshal(bundle.Assets)
	if err != nil {
		return fmt.Errorf("replay: marshal assets: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO replay_bundles
			(original_job_id, version, plan_sha256, renderer_version, rust_protocol_version, ffmpeg_version, encoder_policy_hash, render_plan_json, assets_json, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(original_job_id) DO UPDATE SET
			version = excluded.version,
			plan_sha256 = excluded.plan_sha256,
			renderer_version = excluded.renderer_version,
			rust_protocol_version = excluded.rust_protocol_version,
			ffmpeg_version = excluded.ffmpeg_version,
			encoder_policy_hash = excluded.encoder_policy_hash,
			render_plan_json = excluded.render_plan_json,
			assets_json = excluded.assets_json,
			created_at = excluded.created_at`,
		bundle.OriginalJobID, bundle.Version, bundle.PlanSHA256,
		bundle.RendererVersion, bundle.RustProtocolVersion, bundle.FFmpegVersion,
		bundle.EncoderPolicyHash, string(planJSON), string(assetsJSON),
		bundle.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save replay bundle %s: %w", bundle.OriginalJobID, err)
	}
	return nil
}

// Get returns the job's replay bundle, or (nil, nil) when none is saved.
// Fail-closed: an empty job id is an error, never a silent miss.
func (s *Store) Get(ctx context.Context, originalJobID string) (*capreplay.ReplayBundle, error) {
	if strings.TrimSpace(originalJobID) == "" {
		return nil, fmt.Errorf("%w: original_job_id is required", capreplay.ErrInvalidBundle)
	}
	var (
		bundle         capreplay.ReplayBundle
		renderPlanJSON string
		assetsJSON     string
		createdAt      string
	)
	err := s.db.QueryRowContext(ctx, `SELECT original_job_id, version, plan_sha256, renderer_version, rust_protocol_version, ffmpeg_version, encoder_policy_hash, render_plan_json, assets_json, created_at
		FROM replay_bundles WHERE original_job_id = ?`, originalJobID).
		Scan(&bundle.OriginalJobID, &bundle.Version, &bundle.PlanSHA256,
			&bundle.RendererVersion, &bundle.RustProtocolVersion, &bundle.FFmpegVersion,
			&bundle.EncoderPolicyHash, &renderPlanJSON, &assetsJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get replay bundle %s: %w", originalJobID, err)
	}
	if err := json.Unmarshal([]byte(renderPlanJSON), &bundle.RenderPlan); err != nil {
		return nil, fmt.Errorf("get replay bundle %s: unmarshal render plan: %w", originalJobID, err)
	}
	if err := json.Unmarshal([]byte(assetsJSON), &bundle.Assets); err != nil {
		return nil, fmt.Errorf("get replay bundle %s: unmarshal assets: %w", originalJobID, err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("get replay bundle %s: invalid created_at %q: %w", originalJobID, createdAt, err)
	}
	bundle.CreatedAt = parsed
	return &bundle, nil
}
