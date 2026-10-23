package wiring

import (
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/rendercacheinfo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	overlayrendercache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/overlayrendercache"
	"go.uber.org/zap"
)

// wireOverlayRenderCache installs the content-addressed reuse index for
// certified per-item overlay renders. The index lives in the rebuildable cache
// plane; the bytes stay in the object store and a HEAD verifies each hit
// before it is served. A cache outage is a miss, never a failed run, and the
// kill switch restores the old path.
//
// The process-wide status published on /health is set here, in the same
// composition step that installs the adapter, so the endpoint reports the
// instance the enqueuer actually uses.
func wireOverlayRenderCache(root *ComposeRoot, renderEnqueuer *scriptgen.QueueRenderEnqueuer, log *zap.Logger) {
	// Baseline status for the /health render_cache document. Overwritten below
	// when the cache is wired.
	rendercacheinfo.SetRuntime(rendercacheinfo.Status{Reason: rendercacheinfo.ReasonNotConfigured})
	if root.CacheDB != nil && root.CacheDB.DB != nil && scriptgen.OverlayRenderCacheEnabled() {
		renderCache, cacheErr := overlayrendercache.New(root.CacheDB.DB)
		if cacheErr != nil {
			rendercacheinfo.SetRuntime(rendercacheinfo.Status{
				Enabled: true, Wired: false, Reason: cacheErr.Error(),
			})
			log.Warn("overlay render cache not wired", zap.Error(cacheErr))
			return
		}
		epoch := scriptgen.OverlayRenderCacheEpoch()
		renderEnqueuer.SetOverlayRenderCache(renderCache, epoch)
		renderEnqueuer.SetOverlayArtifactProber(renderinggen.NewOverlayArtifactProber())
		rendercacheinfo.SetRuntime(rendercacheinfo.Status{
			Enabled: true, Wired: true, Epoch: epoch, Table: rendercacheinfo.TableName,
		})
		log.Info("overlay render cache wired",
			zap.String("epoch", epoch),
			zap.String("table", rendercacheinfo.TableName))
		return
	}
	reason := rendercacheinfo.ReasonNoCacheDB
	if !scriptgen.OverlayRenderCacheEnabled() {
		reason = rendercacheinfo.ReasonKillSwitch
	}
	rendercacheinfo.SetRuntime(rendercacheinfo.Status{
		Enabled: scriptgen.OverlayRenderCacheEnabled(), Wired: false, Reason: reason,
	})
	log.Info("overlay render cache disabled",
		zap.String("reason", reason),
		zap.Bool("kill_switch", !scriptgen.OverlayRenderCacheEnabled()))
}
