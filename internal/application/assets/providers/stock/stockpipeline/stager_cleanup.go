package stockpipeline

import (
	"context"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Cleanup removes the staged file's parent temp directory AND
// releases the shared-lease refcount (if any).
func (s *StockStager) Cleanup(_ context.Context, staged *assets.StagedAsset) error {
	if staged == nil || staged.LocalPath == "" {
		return nil
	}

	fs, fsErr := s.fs()
	if fsErr != nil {
		return fsErr
	}

	leaseKeyAny, hasLease := s.assetLeases.LoadAndDelete(staged.LocalPath)

	var ownErr error
	if hasLease {
		leaseKey, _ := leaseKeyAny.(string)
		if !s.isLeaseLeader(leaseKey, staged.LocalPath) {
			ownDir := filepath.Dir(staged.LocalPath)
			if ownDir != "" && ownDir != "." && ownDir != "/" {
				ownErr = fs.RemoveAll(ownDir)
			}
		}
		if rerr := s.releaseSharedLease(leaseKey); rerr != nil {
			s.assetLeases.Store(staged.LocalPath, leaseKey)
			if s.svc != nil && s.svc.log != nil {
				s.svc.log.Warn("stock stager: release shared lease failed",
					zap.String("lease_key", leaseKey),
					zap.Error(rerr))
			}
			if ownErr == nil {
				ownErr = rerr
			}
		}
		return ownErr
	}

	ownDir := filepath.Dir(staged.LocalPath)
	if ownDir == "" || ownDir == "." || ownDir == "/" {
		return nil
	}
	return fs.RemoveAll(ownDir)
}
