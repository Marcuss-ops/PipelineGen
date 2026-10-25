package scriptgeneration

import (
	"context"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"go.uber.org/zap"
)

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

// SetOverlayCanvas wires the target render canvas for the derived
// OverlayPlan. Nil-safe; a zero spec falls back to the golden canary
// default (1280×720 @ 30 FPS).
func (r *Runner) SetOverlayCanvas(canvas OverlayCanvasSpec) {
	if r != nil {
		r.overlayCanvas = canvas
	}
}

// SetOverlayRegistry wires the canonical entity type→template registry used
// by the EntityOverlayPlanner. Nil-safe; nil means overlay intent planning
// is skipped.
func (r *Runner) SetOverlayRegistry(registry *capabilityoverlay.ChrononOverlayRegistry) {
	if r != nil {
		r.overlayRegistry = registry
	}
}

// SetOverlayPrepareEnqueuer wires the overlay.prepare job enqueuer. Nil-safe;
// nil means prepare is not registered (no prepare job is enqueued).
func (r *Runner) SetOverlayPrepareEnqueuer(enqueuer OverlayPrepareEnqueuer) {
	if r != nil {
		r.overlayPrepareEnqueuer = enqueuer
	}
}

// SetOverlayRenderEnqueuer wires the timing-frozen overlay.render path.
func (r *Runner) SetOverlayRenderEnqueuer(enqueuer OverlayRenderEnqueuer) {
	if r != nil {
		r.overlayRenderEnqueuer = enqueuer
	}
}

// OverlayRenderEnqueuer returns the configured Chronon queue boundary so the
// legacy batch child path can share the exact same publisher, fresh-render
// policy and queue client as the durable runner. It is read-only wiring; the
// runner remains the owner of the field.
func (r *Runner) OverlayRenderEnqueuer() OverlayRenderEnqueuer {
	if r == nil {
		return nil
	}
	return r.overlayRenderEnqueuer
}

// SetLocalizedRenderEnqueuer wires the per-(scene, language) localized render
// fan-out. A nil enqueuer disables the fan-out (render not registered); a
// non-nil enqueuer is fail-closed (an enqueue error fails the run).
func (r *Runner) SetLocalizedRenderEnqueuer(enqueuer LocalizedRenderEnqueuer) {
	if r != nil {
		r.localizedRenderEnqueuer = enqueuer
	}
}

// SetGeocoder wires the provider-neutral geocoder used to enrich grounded
// place annotations when the request enables media_plan.provider_policy.geocoding. A nil
// adapter with an enabled policy fails closed at enrichment time.
func (r *Runner) SetGeocoder(geocoder capabilitygeocoding.Geocoder) {
	if r != nil {
		r.geocoder = geocoder
	}
}

// SetMapPlateResolver wires the run's ONLY map source: the certified basemap
// plate resolver loaded from the operator manifest. A nil resolver (or an
// uncovered place) emits no map overlay — never a fabricated one.
func (r *Runner) SetMapPlateResolver(resolver capabilityoverlay.PlateResolver) {
	if r != nil {
		r.mapPlates = resolver
	}
}

// SetVoiceoverPublishDrainer wires the async voiceover publish pool
// (P0.4: separate TTS pool from publish pool). After the voiceover
// phase, the runner drains the pool so Drive links are hydrated before
// downstream stages (audio compile, docs) consume them. A nil drainer
// means synchronous publish (backward compat).
func (r *Runner) SetVoiceoverPublishDrainer(drainer interface{ Wait() }) {
	if r != nil {
		r.voiceoverPublishDrainer = drainer
	}
}

// SetOverlayPublicationDrainer wires the bounded post-render publication pool.
// The runner joins it immediately before completion so Drive failures remain
// terminal even though they no longer occupy a RenderingGen render slot.
// The drainer is handed the run's context, because the pool is process-wide
// while the join must be scoped to the run that queued the work.
func (r *Runner) SetOverlayPublicationDrainer(drainer interface{ Wait(context.Context) error }) {
	if r != nil {
		r.overlayPublicationDrainer = drainer
	}
}

// SetMediaPreflight wires the fail-fast media requirement verification
// (P0.5). When wired, the runner executes it synchronously after normalize
// and before Gemma; any failure aborts the run before LLM, translation, or
// TTS. Fixed-media requests fail closed when it is not wired.
func (r *Runner) SetMediaPreflight(preflight MediaPreflight) {
	if r != nil {
		r.mediaPreflight = preflight
	}
}
