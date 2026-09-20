package cli

import (
	"context"
	"database/sql"
	"fmt"

	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// OpenMediaPostgres opens the PostgreSQL media SSOT handle for an admin
// command that needs to read the media domain.
//
// WHY THIS EXISTS. Commands that only touched operational tables open the
// SQLite set with OpenDatabaseSet. A command that ALSO reads media_assets needs
// the media SSOT handle, and it must resolve it from the SAME engine decision
// point as the runtime (mediasub.RequireMediaPostgres) rather than opening a
// second DSN of its own.
//
// The handle is returned open; the caller owns Close. A nil handle with a nil
// error means the media plane is intentionally not deployed
// (cfg.MediaPostgreSQL.Enabled == false) — callers MUST fail closed rather than
// degrading onto the operational SQLite store, because the operational mirror
// holds no committed media rows.
func OpenMediaPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	db, err := mediasub.RequireMediaPostgres(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open media postgres: %w", err)
	}
	return db, nil
}
