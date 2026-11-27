package buildbundle

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

// runBootDoctor is the DB-08 startup gate in warning mode: it never fails or
// delays the boot. Integrity is skipped here (the full scan belongs to
// `admin db doctor`/nightly); FK and ledger drift are still checked and logged,
// and every finding is projected onto db_health_findings so an operator can
// alert on it.
func runBootDoctor(log *zap.Logger, dbs *Databases) {
	if dbs == nil || dbs.Set == nil || log == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		planes := []struct {
			name string
			db   *storage.SQLiteDB
			dir  string
		}{
			{"primary", dbs.Set.Primary, "migrations/sqlite"},
			{"observability", dbs.Set.Observability, "migrations/sqlite"},
			{"cache", dbs.Set.Cache, "migrations/sqlite"},
			{"jobs", dbs.Set.Jobs, "migrations/sqlite_jobs"},
		}
		for _, p := range planes {
			if p.db == nil || p.db.DB == nil {
				continue
			}
			findings := storage.RunSQLiteDoctor(ctx, p.db.DB, storage.DoctorOptions{
				Plane:         p.name,
				DBPath:        p.db.Path(),
				Quick:         true,
				SkipIntegrity: true,
				LedgerDir:     p.dir,
				Target:        p.name,
			})
			observability.RecordDBHealthFindings(p.name, findings)
			for _, f := range findings {
				switch f.Severity {
				case dbhealth.SeverityError:
					log.Error("db doctor (boot): error finding",
						zap.String("plane", f.Plane), zap.String("check", f.Check),
						zap.String("detail", f.Detail), zap.String("remediation", f.Remediation))
				case dbhealth.SeverityWarning:
					log.Warn("db doctor (boot): warning finding",
						zap.String("plane", f.Plane), zap.String("check", f.Check),
						zap.String("detail", f.Detail), zap.String("remediation", f.Remediation))
				}
			}
		}
	}()
}
