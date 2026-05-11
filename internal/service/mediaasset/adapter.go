package mediaasset

import (
	"context"

	"velox/go-master/internal/core/processor"
)

// ToCoreProcessor adapts a mediaasset.Processor to core/processor.Processor.
func ToCoreProcessor(p *Processor) processor.Processor {
	return &coreAdapter{processor: p}
}

type coreAdapter struct {
	processor *Processor
}

func (a *coreAdapter) Process(ctx context.Context, input *processor.ProcessInput) (*processor.ProcessResult, error) {
	// Convert core input to mediaasset input (value, not pointer)
	assetInput := AssetInput{
		ID:               input.ID,
		Name:             input.Name,
		SourceURL:         input.SourceURL,
		Term:             input.Term,
		OutputDir:         input.OutputDir,
		Filename:         input.Filename,
		FolderID:         input.FolderID,
		Duration:         input.Duration,
		ForceKeyframes:    input.ForceKeyframes,
		StreamCopy:       input.StreamCopy,
		DownloadSections:  input.DownloadSections,
		Normalize:         input.Normalize,
		KeepAudio:         input.KeepAudio,
		DisableDuration:   input.DisableDuration,
		Metadata:         input.Metadata,
	}

	result, err := a.processor.DownloadProcessUpload(ctx, assetInput)
	if err != nil {
		return nil, err
	}

	return &processor.ProcessResult{
		ID:           result.ID,
		Filename:     result.Filename,
		LocalPath:    result.LocalPath,
		FileHash:     result.FileHash,
		DriveLink:    result.DriveLink,
		DriveFileID:  result.DriveFileID,
		DownloadLink: result.DownloadLink,
		Status:       result.Status,
		Error:        result.Error,
	}, nil
}
