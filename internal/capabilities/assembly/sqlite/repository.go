package assembly

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	assembly "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assembly"
	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("assembly sqlite repository: nil db")
	}
	const ddl = `CREATE TABLE IF NOT EXISTS assembly_sessions (assembly_id TEXT PRIMARY KEY, parent_job_id TEXT NOT NULL, preparation_job_id TEXT NOT NULL DEFAULT '', preparation_id TEXT NOT NULL DEFAULT '', preparation_hash TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, runtime_assets_json TEXT NOT NULL DEFAULT '[]', finalize_plan_json TEXT NOT NULL DEFAULT '', project TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP); CREATE INDEX IF NOT EXISTS idx_assembly_sessions_parent_job ON assembly_sessions(parent_job_id)`
	if _, err := db.Exec(ddl); err != nil {
		return nil, fmt.Errorf("assembly sqlite schema: %w", err)
	}
	_, _ = db.Exec(`ALTER TABLE assembly_sessions ADD COLUMN runtime_assets_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE assembly_sessions ADD COLUMN finalize_plan_json TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE assembly_sessions ADD COLUMN project TEXT NOT NULL DEFAULT ''`)
	return &Repository{db: db}, nil
}
func (r *Repository) Put(ctx context.Context, s *assembly.Session) error {
	if s == nil || s.AssemblyID == "" {
		return fmt.Errorf("assembly session is empty")
	}
	b, _ := json.Marshal(s.RuntimeAssets)
	plan, _ := json.Marshal(s.FinalizePlan)
	_, err := r.db.ExecContext(ctx, `INSERT INTO assembly_sessions (assembly_id,parent_job_id,preparation_job_id,preparation_id,preparation_hash,status,revision,runtime_assets_json,finalize_plan_json,project,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(assembly_id) DO UPDATE SET parent_job_id=excluded.parent_job_id, preparation_job_id=excluded.preparation_job_id, preparation_id=excluded.preparation_id, preparation_hash=excluded.preparation_hash, status=excluded.status, revision=excluded.revision, runtime_assets_json=excluded.runtime_assets_json, finalize_plan_json=excluded.finalize_plan_json, project=excluded.project, updated_at=excluded.updated_at`, s.AssemblyID, s.ParentJobID, s.PreparationJobID, s.PreparationID, s.PreparationHash, string(s.Status), s.Revision, string(b), string(plan), s.Project, time.Now().UTC())
	return err
}
func (r *Repository) Get(ctx context.Context, id string) (*assembly.Session, error) {
	s := &assembly.Session{}
	var status string
	var updated string
	var assetsJSON string
	var planJSON, project string
	err := r.db.QueryRowContext(ctx, `SELECT assembly_id,parent_job_id,preparation_job_id,preparation_id,preparation_hash,status,revision,runtime_assets_json,finalize_plan_json,project,updated_at FROM assembly_sessions WHERE assembly_id=?`, id).Scan(&s.AssemblyID, &s.ParentJobID, &s.PreparationJobID, &s.PreparationID, &s.PreparationHash, &status, &s.Revision, &assetsJSON, &planJSON, &project, &updated)
	if err != nil {
		return nil, err
	}
	s.Status = assembly.SessionStatus(status)
	_ = json.Unmarshal([]byte(assetsJSON), &s.RuntimeAssets)
	if planJSON != "" {
		s.FinalizePlan = &contract.FinalizeV1{}
		_ = json.Unmarshal([]byte(planJSON), s.FinalizePlan)
	}
	s.Project = project
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return s, nil
}
