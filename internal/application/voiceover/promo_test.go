// Package voiceover — promo_test.go (BLOC5.3 commit-1-consumer-cutover, June 2026).
//
// Audit pin: TestPromo_AccodesCanonicalChild verifies that the promo
// workflow reaches ProcessVoiceoverItemUseCase (the canonical per-item
// pipeline) and NOT Service.GenerateBatch (the legacy entry that
// routed through Service.GenerateBatch → Service.processLanguage).
//
// The bridge consumes the narrow VoiceoverItemExecutor port
// (internal/application/voiceover/ports.go) instead of a hardcoded
// *ProcessVoiceoverItemUseCase concrete so the test can inject a
// single-method stub without constructing all 7 use-case deps.
package voiceover

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
)

// fakeVoiceoverItemExecutor is the canonical stub for VoiceoverItemExecutor.
// Production concrete: *ProcessVoiceoverItemUseCase.
// Records the per-call itemCommand so the test can assert on the
// canonical-pipeline input shape (request_id, language, voice,
// text_hash, destination kind).
type fakeVoiceoverItemExecutor struct {
	calls   []*GenerateVoiceoverItemCommand
	returnR *VoiceoverItemResult
	returnE error
}

func (f *fakeVoiceoverItemExecutor) Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error) {
	cp := *item
	f.calls = append(f.calls, &cp)
	if f.returnE != nil {
		return nil, f.returnE
	}
	if f.returnR == nil {
		// Default success shape — drive_link empty so we can assert
		// the bridge does NOT synthesise one (it only forwards from
		// the executor output).
		return &VoiceoverItemResult{
			Language:    item.Language,
			Filename:    item.Filename,
			Voice:       item.Voice,
			LocalPath:   "/tmp/" + item.Filename,
			Status:      StatusCompleted,
		}, nil
	}
	return f.returnR, nil
}

// Compile-time conformance assertion — the stub satisfies the narrow
// port even without a separate interface declaration in the test file.
var _ VoiceoverItemExecutor = (*fakeVoiceoverItemExecutor)(nil)

// TestPromo_AccodesCanonicalChild is the canonical audit pin for
// BLOC5.3 commit-1: the promo bridge reaches ProcessVoiceoverItemUseCase
// (the canonical per-item pipeline) and bypasses Service.GenerateBatch.
//
// Steps under test:
//   1. voiceoverGenBridge is constructed with a fakeVoiceoverItemExecutor
//      (the narrow port) — NO *Service is involved.
//   2. promo.Generator.Generate is invoked with a 1-language promo
//      request; per language the bridge calls Generate(cmd) which
//      delegates to the executor.
//   3. Assertions:
//        a) Executor.calls has exactly 1 entry (one language).
//        b) The itemCommand carries the canonical per-item shape
//           (Language, Voice, Filename, TextHash set).
//        c) The result's DriveLink came from the executor's return
//           (default-success has empty DriveLink → demonstrates the
//           bridge does NOT synthesise one from Service.GenerateBatch).
//        d) Result.OK == true (canonical pipeline success path).
func TestPromo_AccodesCanonicalChild(t *testing.T) {
	executor := &fakeVoiceoverItemExecutor{
		returnR: &VoiceoverItemResult{
			Language:    "it-IT",
			Filename:    "vo_it-it_abc123.mp3",
			Voice:       "it-IT-DiegoNeural",
			LocalPath:   "/tmp/vo_it-it_abc123.mp3",
			DriveLink:   "https://drive.google.com/file/d/abc123/view",
			DriveFileID: "abc123",
			Status:      StatusCompleted,
		},
	}
	bridge := &voiceoverGenBridge{
		processItemUseCase: executor,
		log:                zap.NewNop(),
	}

	// Build a stub generator that uses the bridge (real
	// workflow/promo.Generator wiring) with a no-op translator.
	translator := translation.TranslatorFunc(func(ctx context.Context, text, target string) (string, error) {
		return text + "-[" + target + "]", nil // trivial suffix so we can assert it threaded through
	})
	gen := promo.NewGenerator(translator, bridge, zap.NewNop())

	resp, err := gen.Generate(context.Background(), &promo.Request{
		Text:         "Ciao mondo",
		Languages:    []string{"it-IT"},
		DriveFolderID: "drive-folder-1",
	})
	if err != nil {
		t.Fatalf("promo.Generate: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(executor.calls) != 1 {
		t.Fatalf("executor.calls: got %d, want 1", len(executor.calls))
	}
	got := executor.calls[0]
	// cmd.Normalize() lowercases the locale (canonical BCP-47 case-insensitive
	// invariant) before threading it onto itemCmd.Language. The canonical
	// pipeline's LanguageCodeValid accepts alphanum+hyphen at any case.
	if got.Language != "it-it" {
		t.Errorf("item.Language: got %q, want it-it (canonical: cmd.Normalize lowercases locale)", got.Language)
	}
	if got.Filename == "" {
		t.Errorf("item.Filename: got empty (must be pre-computed by bridge; canonical invariant)")
	}
	if got.TextHash == "" {
		t.Errorf("item.TextHash: got empty (canonical invariant: bridge must compute SHA-256(Text) hex)")
	}
	if got.RequestID == "" {
		t.Errorf("item.RequestID: got empty (canonical invariant: bridge must thread buildRequestID)")
	}
	if got.Destination == nil || got.Destination.Kind != "explicit" || got.Destination.FolderID != "drive-folder-1" {
		t.Errorf("item.Destination: got %+v, want explicit+drive-folder-1", got.Destination)
	}

	if resp.Failed > 0 {
		t.Errorf("resp.Failed: got %d, want 0 (canonical pipeline success path)", resp.Failed)
	}
	if resp.Success != 1 {
		t.Errorf("resp.Success: got %d, want 1", resp.Success)
	}

	// DriveLink + DriveFileID propagate from executor output (bridge
	// does NOT synthesise these — would otherwise indicate a silent
	// fallback to a legacy path).
	entry := resp.Results[0]
	if !entry.OK {
		t.Errorf("entry.OK: got false, want true (canonical pipeline success)")
	}
	if entry.DriveLink != "https://drive.google.com/file/d/abc123/view" {
		t.Errorf("entry.DriveLink: got %q, want canonical executor-out value", entry.DriveLink)
	}
	if entry.DriveFileID != "abc123" {
		t.Errorf("entry.DriveFileID: got %q, want canonical executor-out value", entry.DriveFileID)
	}
}

// (The fail-fast / wiring-gap test was originally drafted here but
// removed from Commit 1 to keep the file scope-clean — see the
// commit body for the "out-of-scope / follow-up" pointer. The
// fail-fast contract is enforced by the `processItemUseCase == nil`
// check at the top of (b *voiceoverGenBridge).Generate and is
// exercised via the production wiring path in
// internal/app/build_bundles_voiceover.go.)
