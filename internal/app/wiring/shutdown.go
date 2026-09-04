package wiring

import (
	"context"

	lifecyclewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/lifecycle"
	"go.uber.org/zap"
)

// buildCleanup is the composition-root facade that projects ComposeRoot into
// the narrow shutdown dependency callbacks owned by wiring/lifecycle.
func buildCleanup(dbs *Databases, root *ComposeRoot, _ *backgroundJobs, cancel context.CancelFunc, log *zap.Logger) CleanupFunc {
	var deps lifecyclewiring.ShutdownDeps
	if root != nil && root.Domains != nil && root.Domains.AudioProcessor != nil {
		deps.AudioStop = root.Domains.AudioProcessor.Stop
	}
	if root != nil && root.TextTracks != nil && root.TextTracks.ArgosServer != nil {
		deps.ArgosStop = root.TextTracks.ArgosServer.Stop
	}
	if root != nil && root.Outbox != nil && root.Outbox.EventsPool != nil {
		deps.EventsPoolStop = root.Outbox.EventsPool.Stop
	}
	if dbs != nil && dbs.Main != nil {
		deps.CloseMainDB = dbs.Main.Close
	}
	return CleanupFunc(lifecyclewiring.BuildCleanup(deps, cancel, log))
}
