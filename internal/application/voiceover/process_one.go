// Package voiceover — process_one.go (PR-VOICEOVER-PARENT-CHILD-FANOUT, P0.3, June 2026).
//
// Restored after origin/main drift: the cleanup commits
// `4cb13c86 fix(p1.6): uniform HTTP/application boundaries — channels
// + voiceover` and `75b2550a chore(p0.6): close Active Concerns #11`
// removed the ProcessOneVoiceoverUseCase type that
// internal/app/build_bundles_domain.go::BuildDomainBundle (line 221) and
// internal/app/composition.go::NewComposition (late-bindings block)
// instantiate via voiceover.NewProcessOneVoiceoverUseCase(...). Without
// this file, both composition sites fail to compile, which Cascades to
// a broken build across cmd/server + cmd/worker + cmd/admin.
//
// Scope: this file is the THIN typed-port adapter between the new
// per-language child-job handler (jobs/generate_item_handler.go) and
// the existing canonical Service.GenerateBatch surface. The full
// 7-port per-language use case (TTSProvider + AudioPostProcessor +
// AssetLifecycle + VoiceoverRepository + TransactionalOutbox) is a
// follow-up BACKFILL — flagged in build_bundles_domain.go's P0.3
// comment block.
//
// Wire contract:
//   - Execute takes a single-language child command
//     (*GenerateVoiceoverItemCommand, parent_job_id / request_id already
//     populated by the parent's FanoutUseCase).
//   - It forwards the work to deps.Service.GenerateBatch, which contains
//     the legacy fan-out pipeline. With N=1 Languages the legacy
//     path returns exactly one BatchItem, which we map onto the
//     canonical VoiceoverItemResult shape so downstream consumers can
//     read a uniform struct.
//   - Returns (*VoiceoverItemResult, error): success surfaces the
//     per-language row metadata; failure wraps the underlying
//     GenerateBatch error.
//
// Why Service (not the canonical 7-port use case): the BACKFILL
// invariant pinned in build_bundles_domain.go is "minimum wiring
// footprint, layer the full use case in a follow-up". Using the
// canonical Service guarantees a correct execution path TODAY; the
// 7-port use case migration lands in the next BACKFILL wave.
package voiceover

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// ProcessOneDeps wires dependencies for ProcessOneVoiceoverUseCase per
// AGENTS.md Pattern 0: a typed-port *Service that the composition root
// supplies from the legacy Service bundle (no `interface{}` carrier).
type ProcessOneDeps struct {
	// Service is the canonical voiceover Service pointer (the legacy
	// bundle) whose GenerateBatch path is reused as the per-language
	// worker. MANDATORY — fail-fast per AGENTS.md WireUp pattern.
	// Pointer type so the nil-check in NewProcessOneVoiceoverUseCase
	// is a Go-valid pointer comparison (the Service is a CONCRETE
	// STRUCT at service.go, not an interface).
	Service *Service

	// Logger is OPTIONAL (nil-safe via zap.NewNop() in the constructor).
	Logger *zap.Logger
}

// ProcessOneVoiceoverUseCase is the per-language child side of the
// P0.3 parent-child-fanout. Execute handles ONE language+voice pair
// from a GenerateVoiceoverItemCommand.
type ProcessOneVoiceoverUseCase struct {
	deps ProcessOneDeps
}

// NewProcessOneVoiceoverUseCase constructs the use case. Service is
// mandatory (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is optional (nil-safe via zap.NewNop()).
func NewProcessOneVoiceoverUseCase(deps ProcessOneDeps) *ProcessOneVoiceoverUseCase {
	if deps.Service == nil {
		panic("voiceover.NewProcessOneVoiceoverUseCase: Service is required (ProcessOneDeps.Service)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ProcessOneVoiceoverUseCase{deps: deps}
}

// StatusCompleted + StatusFailed are declared in result.go (canonical source of truth). The legacy Service.GenerateBatch emits these literal strings; this file re-uses them without redeclaration.

// Execute runs the canonical per-language pipeline once via the
// legacy Service.GenerateBatch path. Field mapping from
// GenerateVoiceoverItemCommand → BatchRequest:
//   - Text, Languages=[item.Language], so GenerateBatch's per-language
//     loop fires exactly once.
//   - Destination forwarded verbatim.
//   - Strategy + RemoveSilence + Metadata pass through.
//   - FilenameTemplate uses the pre-computed item.Filename so the
//     child never re-derives the same name.
//
// Voice overrides are forwarded via BatchRequest.VoiceOverrides so
// the legacy multi-language path can apply the same per-language
// TTS voice as the typed fan-out path.
func (u *ProcessOneVoiceoverUseCase) Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error) {
	if item == nil {
		return nil, fmt.Errorf("ProcessOneVoiceoverUseCase.Execute: nil item command")
	}
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("ProcessOneVoiceoverUseCase.Execute: validate: %w", err)
	}

	req := &BatchRequest{
		Text:             item.Text,
		Languages:        []string{item.Language},
		FilenameTemplate: item.Filename,
		VoiceOverrides:   map[string]string{},
		Strategy:         string(item.Strategy),
		Destination:      item.Destination,
		Metadata:         item.Metadata,
	}
	if item.RemoveSilence {
		tru := true
		req.RemoveSilence = &tru
	}
	if item.Voice != "" {
		req.VoiceOverrides[item.Language] = item.Voice
	}

	resp, err := u.deps.Service.GenerateBatch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ProcessOneVoiceoverUseCase.Execute: generate (request_id=%s, language=%s): %w",
			item.RequestID, item.Language, err)
	}
	if resp == nil || len(resp.Items) == 0 {
		return &VoiceoverItemResult{
			Language: item.Language,
			Status:   StatusFailed,
			Error:    "ProcessOneVoiceoverUseCase.GenerateBatch: empty items",
		}, nil
	}
	// GenerateBatch returns exactly one BatchItem for a single-language
	// request; map it onto the canonical VoiceoverItemResult shape so
	// the aggregator (P0.3 commit 2) reads a uniform struct.
	out := resp.Items[0]
	if !out.isSuccessful() {
		result := &VoiceoverItemResult{
			Language: out.Language,
			Status:   StatusFailed,
			Voice:    out.Voice,
		}
		if out.Error != "" {
			result.Error = out.Error
		} else {
			result.Error = "GenerateBatch returned a non-completed status"
		}
		if out.DriveLink != "" {
			result.DriveLink = out.DriveLink
		}
		if out.DriveFileID != "" {
			result.DriveFileID = out.DriveFileID
		}
		if out.LocalPath != "" {
			result.LocalPath = out.LocalPath
		}
		return result, nil
	}
	result := &VoiceoverItemResult{
		Language: out.Language,
		Status:   out.Status,
		Voice:    out.Voice,
	}
	if out.Error != "" {
		result.Error = out.Error
	}
	if out.DriveLink != "" {
		result.DriveLink = out.DriveLink
	}
	if out.DriveFileID != "" {
		result.DriveFileID = out.DriveFileID
	}
	if out.LocalPath != "" {
		result.LocalPath = out.LocalPath
	}
	return result, nil
}

// BuildRequestID is re-exported at the package level so the fan-out
// use case in voiceover/jobs can call it without crossing package
// boundaries. Mirrors the pre-existing package-private buildRequestID
// in types.go so the canonical "vo_<timestamp>_<6-hex>" shape is
// shared across both sites (parent + child).
func BuildRequestID() string {
	return buildRequestID()
}
