package assets

// deps.go owns the typed input contracts of the assets module.
//
// NewArtifactCache took four positional arguments and is resolved from six
// composition sites with the same shape (cache handle, media handle, logger).
// Naming the fields makes the cache/content split explicit at every call site
// instead of relying on the reader to remember which *sql.DB comes first
// (BON-03 step B).

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// ArtifactCacheDeps is the input contract of NewArtifactCache. CacheDB holds
// the cache mappings and metrics; ContentDB is the canonical media database
// where the immutable CAS content registry lives. Both are mandatory: a cache
// wired to only one engine would split the artifact identity in two.
type ArtifactCacheDeps struct {
	Config    *config.Config
	CacheDB   *sql.DB
	ContentDB *sql.DB
	Log       *zap.Logger
}
