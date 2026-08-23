// Package app — Step 8 helper (registry.go split, August 2026).
//
// c3ValidateRuntimeGraph constructs the C3 MutableJobRegistry and runs the
// §4.5 runtime-graph validator at startup. Extracted from registry.go to
// keep the orchestrator file slim (same package, same public surface).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/documents"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/document"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/image"
	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	domainyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Step 8 helper (P0 Commit 3, July 2026) ───────────────────────────

// c3ValidateRuntimeGraph constructs the C3 MutableJobRegistry,
// populates it with the 5 canonical JobDefinitions wired with
// placeholder JobHandlerFunc bindings, freezes the registry, and
// runs the §4.5 validator. Returns nil on a clean graph, or an
// error wrapping ErrInvalidRuntimeGraph when ANY check fails.
//
// Why placeholders? C3 ships registry + validator contracts only.
// The handlers' bodies are NOT yet routed through def.PayloadCodec —
// that is the explicit scope of C4 (Commit 4). For C3's purposes,
// HasHandler=true (post-BindHandler) is the only invariant the
// validator checks; full payload/result dispatch lands in C4.
//
// The wire-up target is job.StartupValidationInput with Workflow =
// {TypeScriptGenerate, TypeImagesGenerate, TypeDocumentGenerate,
// TypeAssetsResolve, TypeClipRegister} — the canonical 5-family
// execution graph.
//
// A future contributor adding a 6th canonical job family must:
//  1. Append the literal to internal/domain/job/canonical_definitions.go.
//  2. Append the type constant to internal/domain/job/job.go.
//  3. Update workflowRefs below.
//  4. Update the per-family round-trip test in registry_codec_completeness_test.go.
//
// The compile-time assertions in startup_validator_test.go lock the
// canonical literal references — adding a 6th and forgetting step
// (3) surfaces as a test failure rather than a runtime mismatch.
func c3ValidateRuntimeGraph() error {
	mutableReg := job.NewMutableJobRegistry()

	// PR-JOB-TYPE-OWNER-LOCKS (July 2026, godlike/06 SSOT): each
	// owning package owns its own JobDefinition (canonical-name
	// identifier + wire-string value lifted verbatim from the
	// domain package). Composition root is the canonical single
	// registration point per AGENTS.md §composition-root. The
	// C3 handler binding below wires placeholder JobHandlerFunc
	// until C4 (Dispatcher Enqueue via def.PayloadCodec) replaces
	// them with real dispatch routing.
	//
	// The 6 owner-side JobXxx identifier constants are captured as
	// a slice here so the placeholder BindHandler loop below can
	// satisfy the HasHandler invariant for the 6 owner types just
	// like the existing CanonicalJobDefinitions entries.
	additionalOwnerTypes := []string{
		images.JobGenerate,
		domainyoutube.TypeClipExtract,
		script.TypeGenerate,
		documents.JobGenerate,
		domainvoiceover.TypeGenerate,
		media.TypeBulkUploadYouTubeClips,
	}
	ownerTypeSet := map[string]bool{
		images.JobGenerate:               true,
		domainyoutube.TypeClipExtract:    true,
		script.TypeGenerate:              true,
		documents.JobGenerate:            true,
		domainvoiceover.TypeGenerate:     true,
		media.TypeBulkUploadYouTubeClips: true,
	}
	for _, registerOwner := range []func(job.MutableJobRegistry) error{
		images.MustRegister,
		youtube.MustRegister,
		scripts.MustRegister,
		documents.MustRegister,
		voiceover.MustRegister,
		clips.MustRegister,
	} {
		if err := registerOwner(mutableReg); err != nil {
			return fmt.Errorf("c3: owner must-register: %w", err)
		}
	}

	for _, def := range job.CanonicalJobDefinitions {
		// PR-JOB-TYPE-OWNER-LOCKS (July 2026, godlike/06 SSOT): skip
		// owner-registered wire-strings so the canonical loop is
		// idempotent. Owner-side MustRegister is the live authority
		// for images.generate / script.generate / document.generate;
		// the CanonicalImagesGenerate / CanonicalScriptGenerate /
		// CanonicalDocumentGenerate literals are filter-skipped here
		// so they remain code-only reference (NOT runtime SSOT).
		// The placeholder BindHandler for those 3 overlaps lands in
		// the additionalOwnerTypes loop below — owner-side authority
		// extends uniformly to definition + handler binding.
		if ownerTypeSet[def.Type] {
			continue
		}
		if err := mutableReg.RegisterDefinition(def); err != nil {
			return fmt.Errorf("register %s: %w", def.Type, err)
		}
		// Placeholder JobHandlerFunc — replaced by C4 dispatch routing.
		// Read by the C3 validator's HasHandler check only; not invoked
		// at runtime until C4 wires def.PayloadCodec -> dispatcher.
		placeholder := func(_ context.Context, _ *job.Job, _ any) (any, error) {
			return nil, nil
		}
		if err := mutableReg.BindHandler(def.Type, placeholder); err != nil {
			return fmt.Errorf("bind handler %s: %w", def.Type, err)
		}
	}
	// PR-JOB-TYPE-OWNER-LOCKS: bind placeholder handlers for the 6
	// owner-side JobXxx types so post-Freeze HasHandler(t) returns
	// true uniformly across every AllDefinitions() entry.
	ownerPlaceholder := func(_ context.Context, _ *job.Job, _ any) (any, error) {
		return nil, nil
	}
	for _, ownerType := range additionalOwnerTypes {
		if err := mutableReg.BindHandler(ownerType, ownerPlaceholder); err != nil {
			return fmt.Errorf("bind owner handler %s: %w", ownerType, err)
		}
	}
	compiled, err := mutableReg.Freeze()
	if err != nil {
		return fmt.Errorf("freeze: %w", err)
	}
	workflowRefs := []string{
		script.TypeGenerate,
		image.TypeImagesGenerate,
		document.TypeGenerate,
		asset.TypeResolve,
		media.TypeClipRegister,
	}
	validator := job.DefaultStartupValidator{}
	if err := validator.ValidateRuntimeGraph(job.StartupValidationInput{
		Registry: compiled,
		Workflow: workflowRefs,
	}); err != nil {
		return err
	}
	return nil
}
