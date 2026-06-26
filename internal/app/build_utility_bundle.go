package app

import (
	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
)

// BuildUtilityBundle constructs the lightweight utility handlers
// and the health-check Service (PR1 Health boundary, June 2026).
func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB) *UtilityBundle {
	svc := buildHealthService(cfg, db)
	return &UtilityBundle{
		Utility:       common.NewUtilityHandler(),
		HealthService: svc,
		ReadyChecker:  systemhealth.NewReadyChecker(svc),
	}
}

// buildHealthService constructs the health.Service from infrastructure
// checkers. Lives here because it's the only place that wires concrete
// adapters (PR1 Health boundary, June 2026).
//
// PG-011 typed-handle migration (June 2026): the previous
// implementation unwrapped `db *storage.SQLiteDB` to `*sql.DB` via
// `var sqlDB *sql.DB; if db != nil { sqlDB = db.DB }` so it could hand
// a raw handle to infrahealth.NewSQLiteChecker / NewJobsChecker. The
// checkers now accept *storage.SQLiteDB directly (the underlying
// *sql.DB is reached via the embedded field), which removes the
// `database/sql` import from this file. The `db` arg may itself be
// nil — infrahealth.Checker constructors accept nil and the zero
// value remains safe.
func buildHealthService(cfg *config.Config, db *storage.SQLiteDB) *systemhealth.Service {
	if cfg == nil {
		return nil
	}

	var driveChecker systemhealth.DriveChecker
	credsPath := cfg.GetCredentialsPath()
	tokenPath := cfg.GetTokenPath()
	if credsPath != "" && tokenPath != "" {
		driveChecker = infrahealth.NewDriveChecker(credsPath, tokenPath)
	}

	// PG-034 (June 2026): QdrantChecker removed — Qdrant capability deleted.
	// `?check=qdrant` health-check probes return "unknown check" so
	// callers asking for the vector-search capability get a defensive
	// surface instead of silently passing on the typo.

	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:    infrahealth.NewSQLiteChecker(db),
		Drive: driveChecker,
		Jobs:  infrahealth.NewJobsChecker(db),
	})
}
