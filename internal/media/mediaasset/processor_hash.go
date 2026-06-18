package mediaasset

import (
	"context"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// hashStep calculates the MD5 hash of the processed file.
func (p *Processor) hashStep(ctx context.Context, path string) (string, error) {
	p.log.Info("calculating file hash", zap.String("path", path))
	return hashutil.MD5File(path)
}
