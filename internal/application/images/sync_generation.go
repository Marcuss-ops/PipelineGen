// Package images — sync_generation.go: synchronous batch-of-one image
// generation adapter (PR-GODOBJ-3-IMAGES-GENERATION, July 2026).
//
// godlike/06 SSOT: canonical owner of the SYNCHRONOUS path for image
// generation. The sync adapter:
//   (1) Calls the usecase via RunUsage (NO legacy imageGen.Generate).
//   (2) Drops the typed *job.ArtifactManifest sidecar from RunOutput
//       (manifest is for the async finalizer path; sync ingestion is
//       direct).
//   (3) Calls storage.IngestImage directly to persist the on-disk
//       artifact — sync paths are the SOLE consumer of IngestImage
//       per PR-GODOBJ-3 KILL LIST b. The async job NEVER calls
//       IngestImage; persistence flows via the ArtifactManifest
//       sidecar + the runner's finalizer (out of scope for this file).
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~150 LoC) because
// the sync adapter hosts the canonical manifest-drop gate
// (RunUsage → manifest is dropped here, the async job keeps it as
// a sidecar) + the IngestImage call site + prompt-slug/filename
// canonicalisation that file-based ingest requires. Forward-pointer
// linked_issue: PR-GODOBJ-3f-SYNC-GEN-SLIM extracts the
// prompt+filename canonicalisation helper into pkg/imgsyncutil (≤30 LoC)
// and lets this file collapse to the dispatch + ingest wrapper.
// Deadline: 2026-08-15 (per zero-baseline rule).
//
// PR-GODOBJ-3 KILL LIST applied:
//   (a) No legacy imageGen.Generate fallback — dispatch goes through
//       registry ONLY (compose → generation_usecase.RunUsage →
//       dispatchToRegistry → ErrNoGenerationProviderWired on nil).
//   (c) GenerateSmartImageWithAccount REMOVED — SyncCommand has no
//       Account/Project fields. Tenant identity belongs in a separate
//       auth/tenancy port (NOT in image-generation request types).
package images

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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
// storage.IngestImage — sync paths are the SOLE consumer of
// IngestImage per PR-GODOBJ-3 KILL LIST b.
//
// The typed *job.ArtifactManifest returned by RunUsage is dropped here
// (the sync path persists through IngestImage directly; the manifest
// is needed only for the async finalizer's Sender-side upload cycle).
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

// ingestGeneratedImage is the SYNC-ONLY consumer of ImageStorageService
// per PR-GODOBJ-3 KILL LIST b: the async job NEVER calls this — it
// instead emits the ArtifactManifest sidecar via job.ManifestKey and
// the runner's finalizer handles media_assets persistence.
//
// This helper exists exclusively so GenerateSync (above) can route the
// sync-generated artifact straight into the canonical media_assets
// ingestion pipeline. The async job's HandleJob does NOT call this.
func (g *GenerationService) ingestGeneratedImage(
	ctx context.Context,
	result *GeneratedImage,
	style string,
	tags []string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	if result == nil {
		return nil, fmt.Errorf("generated image result is nil")
	}

	slug := textutil.Slugify(result.PromptUsed)
	if len(slug) > 50 {
		slug = slug[:50]
	}

	filename := result.PromptUsed
	if len(filename) > 80 {
		filename = filename[:80]
	}
	filename = textutil.Slugify(filename) + "." + result.Format

	description := fmt.Sprintf("AI generated image via Chrome/Playwright for prompt: %s", result.PromptUsed)

	source := result.Provider
	if source == "" {
		source = "google-slides"
	}

	var dataReader io.Reader = bytes.NewReader(result.Data)
	if result.OutputPath != "" {
		f, err := os.Open(result.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("ingestGeneratedImage: failed to open output path %s: %w", result.OutputPath, err)
		}
		defer f.Close()
		dataReader = f
	}

	if result.OutputPath == "" && len(result.Data) == 0 {
		return nil, fmt.Errorf("generated image has no data and no output path")
	}

	return g.storage.IngestImage(
		ctx,
		slug,
		style,
		result.SourceHash,
		dataReader,
		filename,
		source,
		description,
		tags,
		skipDrive,
		false,
	)
}
