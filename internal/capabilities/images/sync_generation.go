// Package images — sync_generation.go (commit 8, 2026-07):
// SYNCHRONOUS batch-of-one image generation adapter — slim form.
//
// godlike/06 SSOT — file ownership after the commit 8 split:
//
//	sync_generation.go           — owns ONLY `SyncCommand` + `GenerateSync`
//	generated_image_ingest.go    — owns `ingestGeneratedImage`
//	                               (sync-only IngestImage caller)
//	generated_image_naming.go    — owns the 4 pure naming helpers
//	                               (buildGeneratedImageSlug,
//	                               buildGeneratedImageFilename,
//	                               buildGeneratedImageDescription,
//	                               resolveGeneratedImageSource)
//
// All three files are in the SAME images package; the
// pre-split monolithic `sync_generation.go` is now slim to
// ONLY SyncCommand + GenerateSync (dispatch via RunUsage +
// ingestGeneratedImage, both same-package invocations). The
// helper file is pure free-functions; the ingest file is the
// cwd stateful piece.
//
// PR-GODOBJ-3 KILL LIST applied (carried from pre-split):
//
//	(a) No legacy imageGen.Generate fallback — dispatch goes through
//	    registry ONLY (compose → generation_usecase.RunUsage →
//	    dispatchToRegistry → ErrNoGenerationProviderWired on nil).
//	(b) GenerateSync calls ingestGeneratedImage via the same-package
//	    method; SVY persistence flows through IngestImage directly.
//	(c) SyncCommand has no Account/Project fields — tenant identity
//	    belongs in a separate auth/tenancy port (NOT in image-
//	    generation request types).
//
// godlike/07 honest-limitation: SyncCommand + GenerateSync +
// the dispatch block (manifest-drop + RunUsage + same-package
// ingestGeneratedImage invocation) is the minimum surface for
// a sync adapter with PR-GODOBJ-3 KILL LIST applied (no legacy
// fallback, no Account/Project fields, dispatch through
// registry ONLY). The 66-LoC transitional cap is advisory.
package images

import (
	"context"
	"fmt"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SyncCommand is the canonical typed input to GenerateSync. PR-GODOBJ-3
// KILL LIST c: NO account/project fields (cleaned from the legacy
// GenerateSmartImageWithAccount surface).
type SyncCommand struct {
	Subject   string
	Topic     string
	Style     string
	Prompts   []string
	Tags      []string
	Width     int
	Height    int
	Model     string
	SkipDrive bool
}

// GenerateSync is the synchronous batch-of-one adapter. It invokes
// the usecase and routes the on-disk artifact directly into
// storage.IngestImage via the same-package ingestGeneratedImage
// (defined in generated_image_ingest.go) — sync paths are the
// SOLE consumer of IngestImage per PR-GODOBJ-3 KILL LIST b.
//
// The typed *job.ArtifactManifest returned by RunUsage is dropped
// here (the sync path persists through IngestImage directly; the
// manifest is needed only for the async finalizer's Sender-side
// upload cycle).
func GenerateSync(ctx context.Context, svc *GenerationService, cmd SyncCommand) (*asset.ImageAsset, error) {
	cleanPrompt := pickImagePrompt(cmd.Subject, cmd.Topic, cmd.Prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	usecaseOut, err := RunUsage(ctx, UsecaseDeps{
		Registry: svc.registry,
		Styles:   svc.styles,
		Log:      svc.log,
	}, UsecaseCommand{
		JobID:      fmt.Sprintf("sync-%s", textutil.Slugify(cmd.Subject)),
		Prompt:     cleanPrompt,
		Style:      cmd.Style,
		Model:      cmd.Model,
		Width:      cmd.Width,
		Height:     cmd.Height,
		Tags:       cmd.Tags,
		OutputPath: "", // sync path lets provider decide output path
	})
	if err != nil {
		return nil, fmt.Errorf("image generation failed: %w", err)
	}

	return svc.ingestGeneratedImage(ctx, usecaseOut.Result, cmd.Style, cmd.Tags, cmd.SkipDrive)
}
