// Package vidrush — cas_promotion.go owns the local durability of an acquired
// image: promoting its bytes into the content-addressed store before
// publication so the ephemeral download can be deleted.
package vidrush

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// BuildVidRushCASStore resolves the canonical content-addressed store of the
// VidRush image pipeline. It is optional by design: a configuration without a
// data dir keeps the historical behavior rather than failing the whole
// materialization composition.
func BuildVidRushCASStore(cfg *config.Config, log *zap.Logger) *cas.Store {
	if cfg == nil {
		return nil
	}
	casRoot := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(casRoot, ".staging")})
	if err != nil {
		if log != nil {
			log.Warn("vidrush finalizer: CAS staging unavailable; downloaded images keep their temp path", zap.Error(err))
		}
		return nil
	}
	store, err := cas.NewStore(cas.Config{Root: casRoot, Stager: stager})
	if err != nil {
		if log != nil {
			log.Warn("vidrush finalizer: CAS store unavailable; downloaded images keep their temp path", zap.Error(err))
		}
		return nil
	}
	return store
}

// promoteArtifactToCAS copies the artifact bytes into the CAS and returns the
// canonical object path. Any failure returns the original path: a CAS failure
// degrades local durability, it never fails an already-verified artifact.
//
// The content address carried by the artifact must equal the address the CAS
// computed. A mismatch means the verified hash and the bytes disagree, so the
// CAS address is authoritative and the original path is kept — the normal
// finalization/indexing path will surface the disagreement.
func (f *vidRushArtifactFinalizer) promoteArtifactToCAS(ctx context.Context, localPath, contentSHA256 string) string {
	localPath = strings.TrimSpace(localPath)
	if f == nil || f.cas == nil || localPath == "" {
		return localPath
	}
	file, err := os.Open(localPath)
	if err != nil {
		if f.log != nil {
			f.log.Warn("vidrush finalizer: CAS promotion could not open artifact", zap.String("local_path", localPath), zap.Error(err))
		}
		return localPath
	}
	defer func() { _ = file.Close() }()
	object, err := f.cas.Put(ctx, file)
	if err != nil {
		if f.log != nil {
			f.log.Warn("vidrush finalizer: CAS promotion failed", zap.String("local_path", localPath), zap.Error(err))
		}
		return localPath
	}
	expected := asset.ResolveContentAddress(contentSHA256)
	if expected != "" && !strings.EqualFold(object.SHA256, expected) {
		if f.log != nil {
			f.log.Warn("vidrush finalizer: CAS address disagrees with the verified hash",
				zap.String("expected", expected), zap.String("actual", object.SHA256))
		}
		return localPath
	}
	casPath, err := f.cas.LocalPath(object.SHA256)
	if err != nil {
		return localPath
	}
	if f.log != nil {
		f.log.Debug("vidrush finalizer: artifact promoted to CAS",
			zap.String("content_sha256", object.SHA256), zap.String("cas_path", casPath), zap.Bool("dedup", object.Dedup))
	}
	return casPath
}
