package wiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type localizedRenderIdentity struct {
	assetID     string
	clipID      string
	clipIDChild string
	sourceLang  string
	targetLang  string
}

func (a *localizedRenderEnqueuerAdapter) resolveRenderIdentity(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (localizedRenderIdentity, error) {
	assetID := strings.TrimSpace(in.ClipAssetID)
	if assetID == "" {
		assetID = strings.TrimSpace(in.ClipID)
	}
	clipID := strings.TrimSpace(in.ClipID)
	if clipID == "" {
		clipID = assetID
	}
	sourceLang := string(in.SourceLanguage)
	if sourceLang == "" {
		sourceLang = strings.TrimSpace(a.cfg.SourceLanguage)
	}
	targetLang := string(in.Language)
	if targetLang == "" {
		targetLang = sourceLang
	}
	if sourceLang == "" || targetLang == "" {
		return localizedRenderIdentity{}, fmt.Errorf("localized render: source and target language are required (scene %q)", in.SceneID)
	}
	resolved, err := a.resolveExistingSubtitleLanguage(ctx, assetID, sourceLang)
	if err != nil {
		return localizedRenderIdentity{}, err
	}
	if resolved != "" {
		sourceLang = resolved
	}
	if sourceLang != targetLang {
		track, cues, findErr := a.tracks.FindReady(ctx, assetID, targetLang, detail.TextTrackTranscript)
		if findErr != nil {
			return localizedRenderIdentity{}, fmt.Errorf("localized render: find translated subtitles for %q/%q: %w", assetID, targetLang, findErr)
		}
		if track == nil || len(cues) == 0 {
			targetLang = sourceLang
		}
	}
	return localizedRenderIdentity{assetID: assetID, clipID: clipID, clipIDChild: clipID, sourceLang: sourceLang, targetLang: targetLang}, nil
}

var _ texttracks.TimedCueWriter = (*localizedRenderEnqueuerAdapter)(nil)
