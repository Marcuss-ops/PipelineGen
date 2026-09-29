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
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

type finalAudioPublisherAdapter struct {
	db          *sql.DB
	preparation finalization.ArtifactPreparationService
	assetTx     finalization.AssetFinalizerTx
	// gate is the process-wide fair Drive-upload gate owned by ComposeRoot,
	// shared with the voiceover per-item publisher. audio_publish used to reach
	// Drive through this adapter WITHOUT passing the rate-limited publisher, so
	// the 85.7 s queue wait measured on 2026-09-28 was outside the shared gate
	// entirely and could not be bounded or attributed. nil means the Drive
	// plane is closed for this composition: AcquireFairSlot treats it as an
	// unbounded slot, exactly as the voiceover adapter does.
	gate *concurrent.FairSemaphore
}

func newFinalAudioPublisher(root *ComposeRoot, committer assetspersistence.AssetCommitter, log *zap.Logger) scriptgen.FinalAudioPublisher {
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil || committer == nil {
		return nil
	}
	// The committer and the transaction passed to the finalizer must use the
	// same database engine. Production wiring uses the canonical PostgreSQL
	// media committer; keep the root DB fallback for legacy/test compositions
	// that provide a SQLite committer without a media database.
	mediaDB := root.DB.DB
	if root.MediaPostgres != nil {
		mediaDB = root.MediaPostgres
	}
	return &finalAudioPublisherAdapter{
		db: mediaDB,
		preparation: assetfinalizer.NewArtifactPreparation(
			drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log,
		),
		assetTx: assetfinalizer.NewAssetTxFinalizer(log, committer),
		gate:    root.DriveUploadGate,
	}
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

	// Acquire the shared, per-owner-fair Drive gate BEFORE the upload. Two
	// effects (both required by the 2026-09-28 measurement):
	//
	//  1. a starving owner (a job publishing its single final audio) is served
	//     ahead of another job's own pipelined voiceover uploads, so one job
	//     can no longer monopolize the process-wide capacity;
	//  2. the time spent waiting is recorded as a typed WaitSemaphore interval
	//     on the bound run, so it is visible instead of masquerading as a slow
	//     upload.
	//
	// Acquired around the Drive upload only (not the DB commit below), because
	// the shared ceiling is a Drive-upload ceiling, not a database one.
	artifactID := assetfinalizer.ComputeAssetID(finalization.KindVoiceover, fmt.Sprintf("%s:%s", runID, lang), 1)
	resolvedFolderID := strings.TrimSpace(voiceoverFolderID)
	filename := strings.TrimSpace(ref.Filename)
	if filename == "" {
		filename = fmt.Sprintf("voiceover [%s].m4a", lang)
	}
	published, err := p.prepareWithGate(ctx, finalization.VerifiedArtifact{
		ArtifactID: artifactID, Kind: finalization.KindVoiceover, Filename: filename,
		LocalPath: ref.Path, MIMEType: "audio/mp4", SizeBytes: ref.SizeBytes,
		SHA256: ref.FinalAudioSHA256, SourceVersion: 1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: fmt.Sprintf("%s:final_audio:%s:%s", runID, lang, ref.FinalAudioSHA256),
		Source:         "voiceover", ProjectID: runID, Language: lang,
		ResolvedFolderID: resolvedFolderID, RootFolderResolved: resolvedFolderID != "",
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

// prepareWithGate runs the Drive-upload half of PublishFinalAudio behind the
// shared fair gate. The gate is acquired here (not around the whole
// PublishFinalAudio) so the DB commit that follows is never blocked on Drive
// upload capacity, and the recorded wait interval maps exactly to the upload.
func (p *finalAudioPublisherAdapter) prepareWithGate(ctx context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	release, err := kernobs.AcquireFairSlot(ctx, p.gate, kernobs.WaitOwner(ctx), kernobs.ComponentDrive, kernobs.WaitSemaphore)
	if err != nil {
		return finalization.PublishedArtifact{}, err
	}
	defer release()
	return p.preparation.Prepare(ctx, artifact)
}
