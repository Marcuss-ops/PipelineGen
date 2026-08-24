// Package app — build_voiceover_jobs.go
// Voiceover job handler registration (voiceover.generate Catena A P0 +
// voiceover.generate_item BLOC5.3 child fanout). Extracted from
// build_bundles_voiceover.go as part of the July 2026 domain split:
// tts / destinations / jobs / validators.
package wiring

import (
	"fmt"


	"go.uber.org/zap"

	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
)

// wireVoiceoverJobBindings registers voiceover.generate (Catena A P0) +
// voiceover.generate_item (BLOC5.3 child fanout) handlers into jobs.Service.
// Extracted from NewComposition per PG-028 (July 2026).
func wireVoiceoverJobBindings(domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	// Voiceover registration moved to the new GenerateJobHandler path
	// (P0.1, June 2026) — see buildVoiceoverService.
	// The legacy Service.RegisterHandler hook (which registered
	// voiceover.batch + voiceover.promo) is intentionally removed here;
	// the legacy codes will be retired in the next refactor (P0.3).
	if domains.VoiceoverGenerateHandler != nil && jobs.Service != nil {
		// Catena A P0 (June 2026): the canonical `voiceover.generate`
		// job type is now backfilled with the typed-port GenerateJobHandler.
		// The boot smoke test at internal/app/voiceover_wiring_test.go
		// fails closed if this registration is absent — the failure mode
		// of HEAD pre-Catena-A was /api/voiceover/generate → 202 → job
		// queued → no consumer → silence.
		//
		// Audit P0 #2 (July 2026): Register now returns error so this
		// wiring step fails loud at boot instead of silently dropping
		// jobs onto an unsigned dispatcher.
		if err := domains.VoiceoverGenerateHandler.Register(jobs.Service); err != nil {
			return fmt.Errorf("voiceover.generate handler wiring (Catena A P0): %w", err)
		}
		log.Info("voiceover.generate handler registered (Catena A P0 wiring complete)")
	} else {
		log.Warn("voiceover.generate handler NOT registered (typed-port chain incomplete — Drive / destResolver / outbox / lifecycle / repo / audio / db must all be wired)",
			zap.Bool("generate_handler_built", domains.VoiceoverGenerateHandler != nil),
			zap.Bool("jobs_service_available", jobs.Service != nil))
	}
	// PR-VOICEOVER-PARENT-CHILD-FANOUT (P0.3, June 2026): construct the
	// parent GenerateJobHandler (Fanout-bound) and the child
	// GenerateItemJobHandler (per-language) at composition time, where
	// jobs.Service is available for both FanoutUseCase construction AND
	// the late-binding Register calls.
	//
	// Audit P0 #2 (July 2026): both Register calls now return error;
	// NewComposition aborts if either fails (fail-closed at boot).
	// Pre-P0 #2 a silent-Warn here would lose the parent-child wiring
	// and the parent fan-out would dead-letter every N children.
	if jobs.Service != nil && domains.VoiceoverProcessItem != nil {
		fanout := voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{
			Enqueuer: jobs.Service,
			Logger:   log,
		})
		parentHandler := voiceoverjobs.NewGenerateJobHandler(fanout, log)
		// Audit P0 #2 (July 2026): the dispatcher's duplicate-
		// Register contract is not part of its surface. Block A above
		// may have already bound a handler for TypeVoiceoverGenerate
		// when BuildDomainBundle succeeded. The pre-P0 #2 silent-Warn
		// path masked this; Post-P0 #2 must explicitly preserve
		// idempotency via dispatcher's HasHandler probe (canonical per
		// internal/app/voiceover_wiring_test.go).
		// If already bound, skip the re-Register — the domains field
		// is still overwritten with the BLOC5.3 fanout-bound handler
		// for downstream state-tracking consumers.
		if !jobs.Service.HasHandler(domainvoiceover.TypeGenerate) {
			if err := parentHandler.Register(jobs.Service); err != nil {
				return fmt.Errorf("voiceover.generate parent handler Register (BLOC5.3 commit-2): %w", err)
			}
		} else {
			log.Info("voiceover.generate handler already bound (Catena A P0 wiring succeeded) — preserving dispatcher binding; BLOC5.3 fanout-bound handler canonicals the domains.VoiceoverGenerateHandler field reference for downstream state-tracking",
				zap.String("job_type", domainvoiceover.TypeGenerate))
		}
		domains.VoiceoverGenerateHandler = parentHandler

		// TypeVoiceoverGenerateItem is NOT pre-registered by Block A
		// (Block A only touches TypeVoiceoverGenerate). Per-language
		// child handler registration is uniquely owned by this block;
		// any failure surfaces as a typed error and aborts composition
		// (fail-closed at boot, audit P0.2).
		childHandler := voiceoverjobs.NewGenerateItemJobHandler(domains.VoiceoverProcessItem, log)
		if err := childHandler.Register(jobs.Service); err != nil {
			return fmt.Errorf("voiceover.generate_item child handler Register (BLOC5.3 commit-2): %w", err)
		}
		domains.VoiceoverGenerateItemHandler = childHandler
		log.Info("BLOC5.3 commit-2 voiceover handlers wired: parent voiceover.generate + child voiceover.generate_item")
	}
	return nil
}
