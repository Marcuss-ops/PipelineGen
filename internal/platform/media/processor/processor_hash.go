package processor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"go.uber.org/zap"
)

// hashStep calculates the MD5 hash of the processed file via the
// canonical checksum SSOT (streaming, never buffers the whole file).
func (p *Processor) hashStep(ctx context.Context, path string) (string, error) {
	p.log.Info("calculating file hash", zap.String("path", path))
	return checksum.LegacyMD5File(path)
}
