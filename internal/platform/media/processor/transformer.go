package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Compile-time assertion: Processor satisfies the new MediaTransformer port.
var _ detail.MediaTransformer = (*Processor)(nil)

// Transform runs the media transformation pipeline on the file at
// input.LocalPath. It does NOT download, upload to Drive, or touch the
// database. This is the canonical MediaTransformer implementation.
func (p *Processor) Transform(ctx context.Context, input *detail.TransformInput) (*detail.TransformResult, error) {
	if input == nil {
		err := fmt.Errorf("detail.TransformInput is required")
		return &detail.TransformResult{Status: "failed", Error: err.Error()}, err
	}

	result := &detail.TransformResult{
		ID:     input.ID,
		Status: "failed",
	}

	if input.ID == "" {
		return result, fmt.Errorf("TransformInput.ID is required")
	}
	if input.Name == "" {
		return result, fmt.Errorf("TransformInput.Name is required")
	}
	if input.LocalPath == "" {
		return result, fmt.Errorf("TransformInput.LocalPath is required")
	}

	_, saveDir := p.setupDirectoriesFromTransform(input)
	finalFilename := filepath.Base(input.Filename)
	if finalFilename == "" || finalFilename == "." {
		finalFilename = textutil.SafeName(input.Name) + " " + input.ID + ".mp4"
	}
	processedPath := OutputPath(saveDir, finalFilename)

	if input.RenditionLayout {
		renditions, err := p.processRenditions(ctx, transformToProcessInput(input), input.LocalPath)
		if err != nil {
			result.Error = fmt.Sprintf("rendition processing failed: %v", err)
			return result, err
		}
		result.Renditions = renditions

		mezzanine := p.findRendition(renditions, detail.RenditionKindMezzanine)
		if mezzanine == nil {
			result.Error = "mezzanine rendition missing after processing"
			return result, fmt.Errorf("%s", result.Error)
		}
		processedPath = mezzanine.LocalPath
		result.LegacyFileMD5 = mezzanine.LegacyFileMD5
		result.LocalPath = mezzanine.LocalPath
		result.Filename = mezzanine.Filename
	} else {
		processedPath, err := p.processStep(ctx, transformToProcessInput(input), input.LocalPath, processedPath)
		if err != nil {
			result.Error = fmt.Sprintf("process failed: %v", err)
			return result, err
		}

		fileHash, err := p.hashStep(ctx, processedPath)
		if err != nil {
			_ = os.Remove(processedPath)
			result.Error = fmt.Sprintf("hash failed: %v", err)
			return result, err
		}

		result.LegacyFileMD5 = fileHash
		result.LocalPath = processedPath
		result.Filename = filepath.Base(processedPath)
	}

	result.Status = "processed"
	return result, nil
}

// setupDirectoriesFromTransform creates temp and save directories for a
// TransformInput, mirroring setupDirectories but without SourceURL/Term.
func (p *Processor) setupDirectoriesFromTransform(input *detail.TransformInput) (tmpDir, saveDir string) {
	tmpDir = filepath.Join(p.dataDir, p.tempDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		p.log.Error("failed to create temp directory; falling back to os.TempDir", zap.String("dir", tmpDir), zap.Error(err))
		tmpDir = os.TempDir()
	}

	saveDir = input.OutputDir
	if saveDir == "" {
		saveDir = filepath.Join(p.dataDir, "mediaassets", textutil.SafeName(input.ID))
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		p.log.Error("failed to create save directory; falling back to tmpDir", zap.String("dir", saveDir), zap.Error(err))
		saveDir = tmpDir
	}

	return tmpDir, saveDir
}

// transformToProcessInput builds a legacy ProcessInput from a TransformInput.
// This is a temporary adapter while the legacy Process method still exists;
// it will be removed once Process is retired.
func transformToProcessInput(input *detail.TransformInput) *detail.ProcessInput {
	return &detail.ProcessInput{
		ID:               input.ID,
		Name:             input.Name,
		LocalPath:        input.LocalPath,
		OutputDir:        input.OutputDir,
		Filename:         input.Filename,
		Duration:         input.Duration,
		ForceKeyframes:   input.ForceKeyframes,
		StreamCopy:       input.StreamCopy,
		DownloadSections: input.DownloadSections,
		Normalize:        input.Normalize,
		KeepAudio:        input.KeepAudio,
		DisableDuration:  input.DisableDuration,
		Width:            input.Width,
		Height:           input.Height,
		RenditionLayout:  input.RenditionLayout,
	}
}
