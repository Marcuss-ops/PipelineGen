package operations

import "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

func newKeyedLocker() KeyedLocker {
	return concurrent.NewKeyedLocker()
}
