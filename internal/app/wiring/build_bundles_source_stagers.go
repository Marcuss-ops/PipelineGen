// Package app contains composition-root wiring for source acquisition.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/platform/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ErrStockPipelineStagerInit identifies a composition-time failure while
// constructing the canonical acquisition stager.
var ErrStockPipelineStagerInit = errors.New("internal/app: acquisition stager initialization failed")

// WireAcquisitionStager constructs the canonical filesystem-backed stager
// used by the stock pipeline. Unwired fetching remains fail-closed: callers
// receive ErrAcquisitionPrepareFailed rather than a successful no-op.
func WireAcquisitionStager(cfg *config.Config, log *zap.Logger, fetch infacq.FetchFn) (appacq.SourceStager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cfg is nil", ErrStockPipelineStagerInit)
	}
	if log == nil {
		log = zap.NewNop()
	}
	if fetch == nil {
		fetch = func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(string)) error {
			return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, "acquisition fetch is not wired")
		}
	}

	stager, err := infacq.NewFilesystemStager(infacq.Options{
		StagingRoot: filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging"),
		Fetch:       fetch,
		Log:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStockPipelineStagerInit, err)
	}
	return stager, nil
}
