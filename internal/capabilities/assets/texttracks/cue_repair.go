package texttracks

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type TimedCueWriter interface {
	ReplaceTranscriptCues(context.Context, string, map[string][]detail.TimedCue) error
}

type FolderPathWriter interface {
	UpdateFolderPath(ctx context.Context, assetID, folderPath string) error
}

type FolderPathRepairService struct{ writer FolderPathWriter }

func NewFolderPathRepairService(writer FolderPathWriter) (*FolderPathRepairService, error) {
	if writer == nil {
		return nil, fmt.Errorf("texttracks: folder path writer is required")
	}
	return &FolderPathRepairService{writer: writer}, nil
}

func (s *FolderPathRepairService) Repair(ctx context.Context, assetID, folderPath string) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("texttracks: folder path writer is not configured")
	}
	if assetID == "" || folderPath == "" {
		return fmt.Errorf("texttracks: asset id and folder path are required")
	}
	return s.writer.UpdateFolderPath(ctx, assetID, folderPath)
}

type CueRepairService struct{ writer TimedCueWriter }

func NewCueRepairService(writer TimedCueWriter) (*CueRepairService, error) {
	if writer == nil {
		return nil, fmt.Errorf("texttracks: timed cue writer is required")
	}
	return &CueRepairService{writer: writer}, nil
}
func (s *CueRepairService) Repair(ctx context.Context, assetID string, cues map[string][]detail.TimedCue) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("texttracks: timed cue writer is not configured")
	}
	if assetID == "" || len(cues) == 0 {
		return fmt.Errorf("texttracks: asset id and cues are required")
	}
	return s.writer.ReplaceTranscriptCues(ctx, assetID, cues)
}
