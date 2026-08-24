package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcedl"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

type casContentStoreAdapter struct{ inner *cas.Store }

var _ sourcedl.ContentStore = (*casContentStoreAdapter)(nil)

func (a *casContentStoreAdapter) Put(ctx context.Context, content io.Reader) (sourcedl.StoredObject, error) {
	obj, err := a.inner.Put(ctx, content)
	if err != nil {
		return sourcedl.StoredObject{}, err
	}
	return sourcedl.StoredObject{SHA256: obj.SHA256, SizeBytes: obj.SizeBytes, Dedup: obj.Dedup}, nil
}

func (a *casContentStoreAdapter) Open(ctx context.Context, sha256 string) (io.ReadCloser, error) {
	return a.inner.Open(ctx, sha256)
}

func (a *casContentStoreAdapter) Exists(ctx context.Context, sha256 string) (bool, error) {
	return a.inner.Exists(ctx, sha256)
}

func (a *casContentStoreAdapter) Size(ctx context.Context, sha256 string) (int64, error) {
	obj, err := a.inner.Stat(ctx, sha256)
	if err != nil {
		return 0, err
	}
	if !obj.Exists {
		return 0, fmt.Errorf("cas object %q is missing", sha256)
	}
	return obj.SizeBytes, nil
}

func buildSourceAwareDownloader(cfg *config.Config, db *sql.DB, log *zap.Logger) (assets.MediaDownloader, error) {
	if cfg == nil || db == nil || log == nil {
		return nil, fmt.Errorf("build source-aware downloader: nil cfg/db/log")
	}
	casRoot := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(casRoot, ".staging")})
	if err != nil {
		return nil, fmt.Errorf("build source-aware downloader: local store: %w", err)
	}
	store, err := cas.NewStore(cas.Config{Root: casRoot, Stager: stager})
	if err != nil {
		return nil, fmt.Errorf("build source-aware downloader: cas store: %w", err)
	}
	identities, err := regsql.NewSourceIdentityStore(db)
	if err != nil {
		return nil, fmt.Errorf("build source-aware downloader: source identity store: %w", err)
	}
	wrapped, err := sourcedl.NewSourceAwareDownloader(
		downloader.NewMediaDownloader(90*time.Second),
		identities,
		&casContentStoreAdapter{inner: store},
		log,
	)
	if err != nil {
		return nil, fmt.Errorf("build source-aware downloader: %w", err)
	}
	if contentRegistry, registryErr := regsql.NewContentObjectStore(db); registryErr == nil {
		wrapped.SetContentRegistry(contentRegistry)
	} else {
		log.Warn("CAS downloader content-object registry unavailable", zap.Error(registryErr))
	}
	if cache, cacheErr := wiring.NewArtifactCache(cfg, db, log); cacheErr == nil {
		wrapped.SetMetrics(cache)
	} else {
		log.Warn("CAS downloader durable cache metrics unavailable", zap.Error(cacheErr))
	}
	log.Info("CAS-backed source-aware downloader wired (dedup + source identity registry)", zap.String("cas_root", casRoot))
	return wrapped, nil
}
