package renderinggen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// overlayTimingReceipt is deliberately a small, stable JSON sidecar. It
// keeps queue-observation timings separate from worker timings, making it
// possible to see whether time was spent waiting, rendering, or publishing.
type overlayTimingReceipt struct {
	SchemaVersion int                `json:"schema_version"`
	Kind          string             `json:"kind"`
	CreatedAt     time.Time          `json:"created_at"`
	PlanID        string             `json:"plan_id,omitempty"`
	ScriptName    string             `json:"script_name"`
	Language      string             `json:"language"`
	OverlayItem   receiptOverlayItem `json:"overlay_item,omitempty"`
	Video         receiptVideo       `json:"video"`
	Timing        receiptTiming      `json:"timing"`
}

type receiptOverlayItem struct {
	ID               string `json:"id,omitempty"`
	Kind             string `json:"kind,omitempty"`
	EntityID         string `json:"entity_id,omitempty"`
	Text             string `json:"text,omitempty"`
	SourceStartUS    int64  `json:"source_start_us,omitempty"`
	SourceEndUS      int64  `json:"source_end_us,omitempty"`
	TargetDurationUS int64  `json:"target_duration_us,omitempty"`
}

type receiptVideo struct {
	Filename       string `json:"filename"`
	DriveFileID    string `json:"drive_file_id,omitempty"`
	DriveLink      string `json:"drive_link,omitempty"`
	DriveFolderID  string `json:"drive_folder_id,omitempty"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	MIMEType       string `json:"mime_type"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	FPSNum         int    `json:"fps_num,omitempty"`
	FPSDen         int    `json:"fps_den,omitempty"`
	FrameCount     int    `json:"frame_count,omitempty"`
	DurationUS     int64  `json:"duration_us,omitempty"`
	Backend        string `json:"backend,omitempty"`
	ChrononVersion string `json:"chronon_version,omitempty"`
}

type receiptTiming struct {
	CompletionWaitMS           int64 `json:"completion_wait_ms,omitempty"`
	PollingSleepMS             int64 `json:"polling_sleep_ms,omitempty"`
	PollingIntervalMS          int64 `json:"polling_interval_ms,omitempty"`
	PollCount                  int   `json:"poll_count,omitempty"`
	RenderMS                   int64 `json:"render_ms,omitempty"`
	EncodeMS                   int64 `json:"encode_ms,omitempty"`
	MaterializeMS              int64 `json:"materialize_ms,omitempty"`
	PlanMS                     int64 `json:"plan_ms,omitempty"`
	ProbeMS                    int64 `json:"probe_ms,omitempty"`
	HashMS                     int64 `json:"hash_ms,omitempty"`
	ObjectStoreUploadMS        int64 `json:"objectstore_upload_ms,omitempty"`
	RenderingGenDrivePublishMS int64 `json:"renderinggen_drive_publish_ms,omitempty"`
	VideoDrivePublishMS        int64 `json:"video_drive_publish_ms"`
	ReceiptPreparationMS       int64 `json:"receipt_preparation_ms"`
	EndToEndObservedMS         int64 `json:"end_to_end_observed_ms"`
}

func (p *DriveOverlayArtifactPublisher) publishReceipt(ctx context.Context, spec scriptgen.OverlayPublicationSpec, artifact *scriptgen.RenderArtifact, scriptName, language, artifactID, videoFilename string, videoPublishMS int64) error {
	started := time.Now()
	receipt := overlayTimingReceipt{
		SchemaVersion: 1,
		Kind:          "chronon_overlay_receipt",
		CreatedAt:     time.Now().UTC(),
		PlanID:        strings.TrimSpace(spec.PlanID),
		ScriptName:    scriptName,
		Language:      language,
		OverlayItem: receiptOverlayItem{
			ID: spec.OverlayItemID, Kind: spec.OverlayItemKind, EntityID: spec.OverlayEntityID,
			Text: spec.OverlayText, SourceStartUS: spec.SourceStartUS, SourceEndUS: spec.SourceEndUS,
			TargetDurationUS: spec.TargetDurationUS,
		},
		Video: receiptVideo{
			Filename:       videoFilename,
			DriveFileID:    artifact.DriveFileID,
			DriveLink:      artifact.DriveLink,
			DriveFolderID:  artifact.DriveFolderID,
			SHA256:         strings.ToLower(artifact.SHA256),
			SizeBytes:      artifact.SizeBytes,
			MIMEType:       firstNonEmpty(artifact.MimeType, "video/mp4"),
			Width:          artifact.Width,
			Height:         artifact.Height,
			FPSNum:         artifact.FPSNum,
			FPSDen:         artifact.FPSDen,
			FrameCount:     artifact.FrameCount,
			DurationUS:     artifact.DurationUS,
			Backend:        artifact.Backend,
			ChrononVersion: artifact.ChrononVersion,
		},
		Timing: receiptTiming{
			CompletionWaitMS:           spec.CompletionWait.Milliseconds(),
			PollingSleepMS:             spec.PollingSleep.Milliseconds(),
			PollingIntervalMS:          spec.PollingInterval.Milliseconds(),
			PollCount:                  spec.PollCount,
			RenderMS:                   artifact.RenderMS,
			EncodeMS:                   artifact.EncodeMS,
			MaterializeMS:              artifact.MaterializeMS,
			PlanMS:                     artifact.PlanMS,
			ProbeMS:                    artifact.ProbeMS,
			HashMS:                     artifact.HashMS,
			ObjectStoreUploadMS:        artifact.UploadMS,
			RenderingGenDrivePublishMS: artifact.DrivePublishMS,
			VideoDrivePublishMS:        videoPublishMS,
		},
	}

	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode receipt JSON: %w", err)
	}
	path, size, hash, err := writeReceiptFile(data)
	if err != nil {
		return err
	}
	defer os.Remove(path)

	receipt.Timing.ReceiptPreparationMS = time.Since(started).Milliseconds()
	receipt.Timing.EndToEndObservedMS = spec.CompletionWait.Milliseconds() + videoPublishMS

	// The timing fields above must be part of the uploaded JSON. Re-encode and
	// re-hash after filling the publication measurements.
	data, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode final receipt JSON: %w", err)
	}
	_ = os.Remove(path)
	path, size, hash, err = writeReceiptFile(data)
	if err != nil {
		return err
	}
	defer os.Remove(path)

	receiptArtifact := finalization.VerifiedArtifact{
		ArtifactID:         "overlay-receipt:" + firstNonEmpty(artifactID, artifact.SHA256),
		Kind:               finalization.KindScript,
		Filename:           strings.TrimSuffix(videoFilename, ".mp4") + ".receipt.json",
		LocalPath:          path,
		MIMEType:           "application/json",
		SizeBytes:          size,
		SHA256:             hash,
		SourceVersion:      1,
		Requirement:        finalization.ArtifactRequirementRequired,
		IdempotencyKey:     "overlay-receipt:" + strings.ToLower(artifact.SHA256),
		RootFolderName:     scriptName,
		Description:        "Chronon overlay timing receipt " + scriptName + " (" + language + ")",
		Source:             "chronon_receipt",
		ProjectID:          scriptName,
		Language:           language,
		ResolvedFolderID:   p.rootFolderID,
		RootFolderResolved: true,
		DriveSubpath:       []string{finalization.OverlayChildFolder},
		ArtifactMetadata:   map[string]any{"script_name": scriptName, "language": language, "source": "chronon_receipt", "plan_id": spec.PlanID, "video_sha256": artifact.SHA256},
	}
	if p.scriptLanguageRouting {
		receiptArtifact.DriveSubpath = []string{scriptName, language, finalization.OverlayChildFolder}
	}
	if _, err := p.publisher.Publish(ctx, receiptArtifact); err != nil {
		return err
	}
	return nil
}

func writeReceiptFile(data []byte) (path string, size int64, hash string, err error) {
	file, err := os.CreateTemp("", "pipelinegen-overlay-receipt-*.json")
	if err != nil {
		return "", 0, "", fmt.Errorf("create receipt staging file: %w", err)
	}
	path = file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", 0, "", fmt.Errorf("write receipt staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", 0, "", fmt.Errorf("close receipt staging file: %w", err)
	}
	// Content identity goes through the digest SSOT (godlike/06): importing
	// crypto/sha256 here is banned by percheck_digest_sha256_ban.
	return path, int64(len(data)), digest.SHA256Bytes(data), nil
}
