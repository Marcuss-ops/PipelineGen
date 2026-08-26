package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"

	"go.uber.org/zap"
)

// finalAudioPublisherAdapter exposes the canonical ArtifactPreparation +
// AssetFinalizerTx spine to the script runtime. It publishes only certified
// audio, registers it as a canonical MediaRegistry asset, and returns the
// canonical asset ID plus the provider's web link. The local filesystem path
// never crosses this boundary.
type finalAudioPublisherAdapter struct {
	db          *sql.DB
	preparation finalization.ArtifactPreparationService
	assetTx     finalization.AssetFinalizerTx
}

func newFinalAudioPublisher(root *ComposeRoot, committer assetspersistence.AssetCommitter, log *zap.Logger) scriptgen.FinalAudioPublisher {
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil || committer == nil {
		return nil
	}
	return &finalAudioPublisherAdapter{
		db: root.DB.DB,
		preparation: assetfinalizer.NewArtifactPreparation(
			drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log,
		),
		assetTx: assetfinalizer.NewAssetTxFinalizer(log, committer),
	}
}

func newFinalVideoPublisher(root *ComposeRoot, committer assetspersistence.AssetCommitter, log *zap.Logger) scriptgen.FinalVideoPublisher {
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil || committer == nil {
		return nil
	}
	return &finalAudioPublisherAdapter{db: root.DB.DB,
		preparation: assetfinalizer.NewArtifactPreparation(drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log),
		assetTx:     assetfinalizer.NewAssetTxFinalizer(log, committer)}
}

func (p *finalAudioPublisherAdapter) PublishFinalVideo(ctx context.Context, runID string, ref scriptgen.FinalVideoReference, folderID string) (scriptgen.FinalAudioPublishResult, error) {
	if p == nil || p.preparation == nil || p.assetTx == nil || p.db == nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final video publisher is not configured")
	}
	if strings.TrimSpace(ref.LocalPath) == "" || strings.TrimSpace(ref.SHA256) == "" || ref.SizeBytes <= 0 {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("certified final video has no local path, hash, or size")
	}
	artifactID := assetfinalizer.ComputeAssetID(finalization.KindVideo, runID+":final", 1)
	published, err := p.preparation.Prepare(ctx, finalization.VerifiedArtifact{
		ArtifactID: artifactID, Kind: finalization.KindVideo, Filename: "final.mp4", LocalPath: ref.LocalPath,
		MIMEType: "video/mp4", SizeBytes: ref.SizeBytes, SHA256: ref.SHA256, SourceVersion: 1,
		Requirement: finalization.ArtifactRequirementRequired, IdempotencyKey: runID + ":final_video:" + ref.SHA256,
		Source: "script-generation", ProjectID: runID, ResolvedFolderID: strings.TrimSpace(folderID), RootFolderResolved: strings.TrimSpace(folderID) != "",
	})
	if err != nil {
		return scriptgen.FinalAudioPublishResult{}, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final video publisher: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	published.ArtifactMetadata = map[string]any{"sha256": ref.SHA256, "size_bytes": ref.SizeBytes, "run_id": runID}
	if _, _, err := p.assetTx.FinalizeAsset(ctx, assetfinalizer.WrapTx(tx), published); err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final video publisher: register asset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final video publisher: commit tx: %w", err)
	}
	committed = true
	link := strings.TrimSpace(published.Location.WebViewLink)
	if link == "" {
		link = strings.TrimSpace(published.Location.DownloadLink)
	}
	if link == "" {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("published final video has no canonical Drive link")
	}
	return scriptgen.FinalAudioPublishResult{AssetID: artifactID, DriveLink: link}, nil
}

func (p *finalAudioPublisherAdapter) PublishFinalAudio(ctx context.Context, runID string, language scriptgen.Language, ref scriptgen.FinalAudioReference, voiceoverFolderID string) (scriptgen.FinalAudioPublishResult, error) {
	if p == nil || p.preparation == nil || p.assetTx == nil || p.db == nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final audio publisher is not configured")
	}
	if strings.TrimSpace(ref.Path) == "" || strings.TrimSpace(ref.FinalAudioSHA256) == "" {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("certified final audio has no local path or hash")
	}
	lang := strings.TrimSpace(string(language))
	if lang == "" {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final audio language is empty")
	}

	// Canonical identity: the final audio is registered under a deterministic
	// asset ID derived from its logical identity (kind + run + language), never
	// from the transient local temp path. The local path remains internal; only
	// this ID and the public Drive link cross the document boundary.
	artifactID := assetfinalizer.ComputeAssetID(finalization.KindVoiceover, fmt.Sprintf("%s:%s", runID, lang), 1)

	// The final master is a voiceover-kind artifact: it must publish into the
	// same caller-explicit voiceover folder as the per-scene TTS fragments.
	// When output.voiceover_folder_id is set we pin the destination directly
	// (RootFolderResolved) so the delivery registry never replaces it with an
	// empty/unconfigured default root; when empty the configured default (or
	// its fail-closed absence) still applies.
	resolvedFolderID := strings.TrimSpace(voiceoverFolderID)

	filename := strings.TrimSpace(ref.Filename)
	if filename == "" {
		filename = fmt.Sprintf("voiceover [%s].m4a", lang)
	}
	published, err := p.preparation.Prepare(ctx, finalization.VerifiedArtifact{
		ArtifactID:         artifactID,
		Kind:               finalization.KindVoiceover,
		Filename:           filename,
		LocalPath:          ref.Path,
		MIMEType:           "audio/mp4",
		SizeBytes:          ref.SizeBytes,
		SHA256:             ref.FinalAudioSHA256,
		SourceVersion:      1,
		Requirement:        finalization.ArtifactRequirementRequired,
		IdempotencyKey:     fmt.Sprintf("%s:final_audio:%s:%s", runID, lang, ref.FinalAudioSHA256),
		Source:             "voiceover",
		ProjectID:          runID,
		Language:           lang,
		ResolvedFolderID:   resolvedFolderID,
		RootFolderResolved: resolvedFolderID != "",
	})
	if err != nil {
		return scriptgen.FinalAudioPublishResult{}, err
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final audio publisher: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	published.ArtifactMetadata = map[string]any{
		"audio_contract_version": ref.AudioContractVersion,
		"audio_plan_version":     ref.AudioPlanVersion,
		"audio_plan_sha256":      ref.PlanSHA256,
		"final_audio_sha256":     ref.FinalAudioSHA256,
		"codec":                  ref.Codec,
		"profile":                ref.Profile,
		"sample_rate":            ref.SampleRate,
		"channels":               ref.Channels,
		"channel_layout":         ref.ChannelLayout,
		"bitrate":                ref.Bitrate,
		"duration_ms":            ref.DurationMS,
		"final_mix":              ref.FinalMix,
		"copy_eligible":          ref.CopyEligible,
	}
	if _, _, err := p.assetTx.FinalizeAsset(ctx, assetfinalizer.WrapTx(tx), published); err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final audio publisher: register asset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("final audio publisher: commit tx: %w", err)
	}
	committed = true

	link := strings.TrimSpace(published.Location.WebViewLink)
	if link == "" {
		link = strings.TrimSpace(published.Location.DownloadLink)
	}
	if link == "" {
		return scriptgen.FinalAudioPublishResult{}, fmt.Errorf("published final audio has no canonical Drive link")
	}
	return scriptgen.FinalAudioPublishResult{AssetID: artifactID, DriveLink: link}, nil
}
