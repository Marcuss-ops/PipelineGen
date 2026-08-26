package texttracks

import (
	"context"
	"fmt"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"io"
	"os"
)

func (s *AcquireService) acquireFromDrive(ctx context.Context, cmd AcquireCommand) (*AcquireResult, error) {
	rc, _, err := s.drive.DownloadFile(ctx, cmd.DriveFileID)
	if err != nil {
		return nil, fmt.Errorf("download Drive file %s: %w", cmd.DriveFileID, err)
	}
	defer rc.Close()
	f, err := os.CreateTemp("", "pipelinegen-drive-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("create Drive temp file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return nil, fmt.Errorf("write Drive temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close Drive temp file: %w", err)
	}
	transcript, err := s.whisper.TranscribeAudioWithDetection(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("transcribe Drive clip: %w", err)
	}
	if transcript.Text == "" {
		return nil, ErrNoSourceAcquired
	}
	return &AcquireResult{
		AssetID:      cmd.AssetID,
		PlainText:    transcript.Text,
		Cues:         transcript.Cues,
		LanguageCode: transcript.DetectedLanguage,
		SourceType:   detail.TextSourceWhisper,
		SourcePath:   path,
		Confidence:   transcript.Confidence,
		Priority:     25,
		DurationMs:   transcript.DurationMs,
	}, nil
}
