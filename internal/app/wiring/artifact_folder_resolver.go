package wiring

import (
	"database/sql"

	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
)

type sqliteArtifactFolderResolver = assetswiring.ArtifactFolderResolver

func newSQLiteArtifactFolderResolver(db *sql.DB) *sqliteArtifactFolderResolver {
	return assetswiring.NewArtifactFolderResolver(db)
}
