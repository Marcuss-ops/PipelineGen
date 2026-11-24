package texttracks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

func (m *SubtitleArtifactMaterializer) Materialize(ctx context.Context, in SubtitleMaterializerInput) (*SubtitleMaterializerOutput, error) {
	if err := validateSubtitleInput(in); err != nil {
		return nil, err
	}

	textHash, cuesHashBytes, cuesHash, err := computeSubtitleHashes(in)
	if err != nil {
		return nil, err
	}
	if current, ok, err := m.reuseCurrentSubtitle(ctx, in, textHash, cuesHashBytes); err != nil || ok {
		return current, err
	}

	localPath, fileHash, err := m.writeSubtitleFile(in, cuesHash)
	if err != nil {
		return nil, err
	}

	vErr := validateASSFile(localPath, in.ClipDurationMs)
	validationErrorStr := ""
	status := detail.SubtitleStatusReady
	if vErr != nil {
		validationErrorStr = vErr.Error()
		status = detail.SubtitleStatusFailed
	}

	output := buildSubtitleOutput(in, localPath, fileHash, cuesHashBytes, textHash, validationErrorStr)
	art := buildSubtitleArtifact(in, output, fileHash, status, validationErrorStr)
	if err := m.repo.Upsert(ctx, art); err != nil {
		return nil, fmt.Errorf("ass_materializer: db upsert: %w", err)
	}
	if status != detail.SubtitleStatusReady {
		return output, nil
	}
	return m.publishSubtitleArtifact(ctx, in, art, localPath, fileHash, output)
}

func validateSubtitleInput(in SubtitleMaterializerInput) error {
	if in.AssetID == "" {
		return fmt.Errorf("ass_materializer: asset_id is required")
	}
	if in.LanguageCode == "" {
		return fmt.Errorf("ass_materializer: language_code is required")
	}
	if len(in.TimedCues) == 0 {
		return fmt.Errorf("ass_materializer: timed_cues is empty")
	}
	return nil
}

func computeSubtitleHashes(in SubtitleMaterializerInput) (string, string, string, error) {
	var fullTextBuilder strings.Builder
	for i, c := range in.TimedCues {
		if i > 0 {
			fullTextBuilder.WriteString(" ")
		}
		fullTextBuilder.WriteString(c.Text)
	}
	textHash := digest.SHA256Bytes([]byte(fullTextBuilder.String()))

	cuesJSON, err := json.Marshal(in.TimedCues)
	if err != nil {
		return "", "", "", fmt.Errorf("ass_materializer: marshal cues: %w", err)
	}
	cuesHashBytes := digest.SHA256Bytes(cuesJSON)
	cuesHash := cuesHashBytes[:8]
	return textHash, cuesHashBytes, cuesHash, nil
}

func (m *SubtitleArtifactMaterializer) reuseCurrentSubtitle(
	ctx context.Context,
	in SubtitleMaterializerInput,
	textHash string,
	cuesHashBytes string,
) (*SubtitleMaterializerOutput, bool, error) {
	current, err := m.repo.FindCurrent(ctx, in.AssetID, in.LanguageCode, detail.SubtitleFormatASS)
	if err != nil {
		return nil, false, fmt.Errorf("ass_materializer: lookup current artifact: %w", err)
	}
	if !subtitleArtifactMatches(current, in, textHash, cuesHashBytes) {
		return nil, false, nil
	}
	if _, statErr := os.Stat(current.LocalPath); statErr != nil {
		return nil, false, fmt.Errorf("ass_materializer: canonical subtitle %q exists on Drive but is not materialized locally; refusing duplicate generation/upload", current.DriveFileID)
	}
	return &SubtitleMaterializerOutput{
		LocalPath:       current.LocalPath,
		ContentSHA256:   current.ContentSHA256,
		CuesHash:        current.CuesHash,
		TextHash:        current.TextHash,
		CoveredDuration: current.LastCueEndMs,
		CueCount:        current.CueCount,
		LastCueEndMs:    current.LastCueEndMs,
	}, true, nil
}

func subtitleArtifactMatches(current *detail.SubtitleArtifact, in SubtitleMaterializerInput, textHash, cuesHashBytes string) bool {
	return current != nil &&
		current.Status == detail.SubtitleStatusReady &&
		current.IsCurrent &&
		current.TextHash == textHash &&
		current.CuesHash == cuesHashBytes &&
		current.StyleVersion == in.SubtitleStyleID &&
		current.ClipDurationMs == in.ClipDurationMs &&
		(in.ClipContentHash == "" || current.ClipContentHash == in.ClipContentHash) &&
		current.DriveFileID != "" &&
		current.DriveURL != ""
}

func (m *SubtitleArtifactMaterializer) writeSubtitleFile(in SubtitleMaterializerInput, cuesHash string) (string, string, error) {
	localDir := filepath.Join(m.root, in.AssetID, in.LanguageCode)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return "", "", fmt.Errorf("ass_materializer: mkdir: %w", err)
	}
	localPath := filepath.Join(localDir, fmt.Sprintf("%s.ass", cuesHash))
	assContent := m.generateASSContent(in.TimedCues, in.SubtitleStyleID)
	if err := os.WriteFile(localPath, []byte(assContent), 0644); err != nil {
		return "", "", fmt.Errorf("ass_materializer: write file: %w", err)
	}
	return localPath, digest.SHA256Bytes([]byte(assContent)), nil
}

func buildSubtitleOutput(in SubtitleMaterializerInput, localPath, fileHash, cuesHashBytes, textHash, validationError string) *SubtitleMaterializerOutput {
	lastCueEndMs := subtitleLastCueEnd(in.TimedCues)
	return &SubtitleMaterializerOutput{
		LocalPath:       localPath,
		ContentSHA256:   fileHash,
		CuesHash:        cuesHashBytes,
		TextHash:        textHash,
		CoveredDuration: lastCueEndMs,
		ValidationError: validationError,
		CueCount:        len(in.TimedCues),
		LastCueEndMs:    lastCueEndMs,
	}
}

func subtitleLastCueEnd(cues []detail.TimedCue) int64 {
	if len(cues) == 0 {
		return 0
	}
	return cues[len(cues)-1].EndMs
}

func buildSubtitleArtifact(in SubtitleMaterializerInput, output *SubtitleMaterializerOutput, fileHash string, status detail.SubtitleArtifactStatus, validationError string) *detail.SubtitleArtifact {
	return &detail.SubtitleArtifact{
		AssetID:          in.AssetID,
		TextTrackID:      in.TextTrackID,
		LanguageCode:     in.LanguageCode,
		Format:           detail.SubtitleFormatASS,
		LocalPath:        output.LocalPath,
		ContentSHA256:    fileHash,
		TextHash:         output.TextHash,
		CuesHash:         output.CuesHash,
		ClipContentHash:  in.ClipContentHash,
		CueCount:         len(in.TimedCues),
		ClipDurationMs:   in.ClipDurationMs,
		LastCueEndMs:     output.LastCueEndMs,
		StyleVersion:     in.SubtitleStyleID,
		GeneratorVersion: "vidrush-ass-v2",
		Status:           status,
		IsCurrent:        true,
		ValidationError:  validationError,
	}
}

func (m *SubtitleArtifactMaterializer) publishSubtitleArtifact(
	ctx context.Context,
	in SubtitleMaterializerInput,
	art *detail.SubtitleArtifact,
	localPath, fileHash string,
	output *SubtitleMaterializerOutput,
) (*SubtitleMaterializerOutput, error) {
	if m.publisher == nil {
		return m.markSubtitleFailed(ctx, art, fmt.Errorf("ass_materializer: Drive publisher is not configured"))
	}
	if strings.TrimSpace(in.DriveFolderID) == "" {
		return m.markSubtitleFailed(ctx, art, fmt.Errorf("ass_materializer: Drive folder is not configured for asset %s", in.AssetID))
	}

	result, err := m.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: in.DriveFolderID,
		LocalPath:           localPath,
		Filename:            subtitlePublishFilename(in),
		AssetID:             in.AssetID,
		DestinationSubpath:  subtitleDriveSubpath(in),
		ContentHash:         fileHash,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, in.AssetID+":"+in.LanguageCode, fileHash, in.TextTrackID),
		ConflictPolicy:      delivery.ConflictOverwrite,
	})
	if err != nil {
		return m.markSubtitleFailed(ctx, art, fmt.Errorf("ass_materializer: publish Drive artifact: %w", err))
	}
	if result == nil || result.FileID == "" || result.WebViewLink == "" {
		return m.markSubtitleFailed(ctx, art, fmt.Errorf("ass_materializer: publish Drive artifact returned incomplete result"))
	}

	art.DriveFileID = result.FileID
	art.DriveURL = result.WebViewLink
	if err := m.repo.Upsert(ctx, art); err != nil {
		return nil, fmt.Errorf("ass_materializer: persist Drive artifact: %w", err)
	}
	return output, nil
}

func subtitlePublishFilename(in SubtitleMaterializerInput) string {
	filename := fmt.Sprintf("%s.%s.ass", in.AssetID, in.LanguageCode)
	if strings.TrimSpace(in.DriveFilename) == "" {
		return filename
	}
	driveFilename := strings.TrimSpace(in.DriveFilename)
	base := strings.TrimSuffix(filepath.Base(driveFilename), filepath.Ext(driveFilename))
	if base == "" {
		return filename
	}
	return fmt.Sprintf("%s.%s.ass", base, in.LanguageCode)
}

func subtitleDriveSubpath(in SubtitleMaterializerInput) []string {
	if in.DriveSubpath == nil {
		return []string{"Ass Sub"}
	}
	return in.DriveSubpath
}

func (m *SubtitleArtifactMaterializer) markSubtitleFailed(ctx context.Context, art *detail.SubtitleArtifact, cause error) (*SubtitleMaterializerOutput, error) {
	art.Status = detail.SubtitleStatusFailed
	art.ValidationError = cause.Error()
	if persistErr := m.repo.Upsert(ctx, art); persistErr != nil {
		return nil, fmt.Errorf("%w; persist failed status: %v", cause, persistErr)
	}
	return nil, cause
}
