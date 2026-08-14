package capabilities

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

// finalAudioPublisherAdapter exposes the canonical ArtifactPreparation
// publisher to the script runtime. It publishes only certified audio and
// returns the provider's canonical web link; it never constructs Drive URLs.
type finalAudioPublisherAdapter struct {
	preparation finalization.ArtifactPreparationService
}

func newFinalAudioPublisher(root *wiring.ComposeRoot, log *zap.Logger) scriptgen.FinalAudioPublisher {
	if root == nil || root.Drive == nil || root.Drive.Publisher == nil {
		return nil
	}
	return &finalAudioPublisherAdapter{
		preparation: assetfinalizer.NewArtifactPreparation(
			drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log,
		),
	}
}

func (p *finalAudioPublisherAdapter) PublishFinalAudio(ctx context.Context, runID string, language scriptgen.Language, ref scriptgen.FinalAudioReference) (string, error) {
	if p == nil || p.preparation == nil {
		return "", fmt.Errorf("final audio publisher is not configured")
	}
	if strings.TrimSpace(ref.Path) == "" || strings.TrimSpace(ref.FinalAudioSHA256) == "" {
		return "", fmt.Errorf("certified final audio has no local path or hash")
	}
	lang := strings.TrimSpace(string(language))
	if lang == "" {
		return "", fmt.Errorf("final audio language is empty")
	}
	published, err := p.preparation.Prepare(ctx, finalization.VerifiedArtifact{
		ArtifactID:     ref.AssetID,
		Kind:           finalization.KindVoiceover,
		Filename:       fmt.Sprintf("final_audio_%s.m4a", lang),
		LocalPath:      ref.Path,
		MIMEType:       "audio/mp4",
		SizeBytes:      ref.SizeBytes,
		SHA256:         ref.FinalAudioSHA256,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: fmt.Sprintf("%s:final_audio:%s:%s", runID, lang, ref.FinalAudioSHA256),
		Source:         "voiceover",
		ProjectID:      runID,
		Language:       lang,
	})
	if err != nil {
		return "", err
	}
	link := strings.TrimSpace(published.Location.WebViewLink)
	if link == "" {
		link = strings.TrimSpace(published.Location.DownloadLink)
	}
	if link == "" {
		return "", fmt.Errorf("published final audio has no canonical Drive link")
	}
	return link, nil
}
