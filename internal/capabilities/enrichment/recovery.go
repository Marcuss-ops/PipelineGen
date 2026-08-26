// Package enrichment contains the canonical, idempotent media-enrichment
// orchestration contracts. External projections are read-only inputs here;
// canonical text is persisted through the TextTrackRepository port.
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type HistoricalText struct {
	AssetID     string
	Description string
	SearchText  string
	Projection  string
}

type HistoricalTextReader interface {
	FindByAssetID(context.Context, string) (*HistoricalText, error)
}
type AssetReader interface {
	Exists(context.Context, string) (bool, error)
}
type TextTrackReader interface {
	Find(context.Context, string, string, detail.TextTrackKind) (*detail.TextTrack, error)
}
type RecoveryCommitter interface {
	CommitRecoveredText(context.Context, string, string, []detail.TextTrack, string) error
}
type RecoveryService struct {
	assets    AssetReader
	reader    HistoricalTextReader
	tracks    TextTrackReader
	committer RecoveryCommitter
}

func NewRecoveryService(assets AssetReader, reader HistoricalTextReader, tracks TextTrackReader, committer RecoveryCommitter) (*RecoveryService, error) {
	if assets == nil || reader == nil || tracks == nil || committer == nil {
		return nil, errors.New("enrichment recovery: all dependencies are required")
	}
	return &RecoveryService{assets: assets, reader: reader, tracks: tracks, committer: committer}, nil
}

type RecoveryResult struct {
	AssetID       string
	Recovered     int
	SkippedBetter int
	ReindexQueued bool
}

// RecoverAsset imports only missing canonical text. Existing READY,
// non-empty text always wins, regardless of the historical projection.
func (s *RecoveryService) RecoverAsset(ctx context.Context, assetID, language string) (RecoveryResult, error) {
	assetID, language = strings.TrimSpace(assetID), strings.TrimSpace(language)
	if assetID == "" || language == "" {
		return RecoveryResult{}, errors.New("enrichment recovery: asset_id and language are required")
	}
	ok, err := s.assets.Exists(ctx, assetID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("verify asset: %w", err)
	}
	if !ok {
		return RecoveryResult{AssetID: assetID}, nil
	}
	historical, err := s.reader.FindByAssetID(ctx, assetID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("read historical text: %w", err)
	}
	if historical == nil {
		return RecoveryResult{AssetID: assetID}, nil
	}
	result := RecoveryResult{AssetID: assetID}
	tracks := make([]detail.TextTrack, 0, 2)
	for _, candidate := range []struct {
		kind detail.TextTrackKind
		text string
	}{
		{detail.TextTrackDescription, historical.Description}, {detail.TextTrackSearchText, historical.SearchText},
	} {
		text := strings.TrimSpace(candidate.text)
		if text == "" {
			continue
		}
		existing, findErr := s.tracks.Find(ctx, assetID, language, candidate.kind)
		if findErr != nil {
			return RecoveryResult{}, fmt.Errorf("read existing %s: %w", candidate.kind, findErr)
		}
		if existing != nil && existing.Status == detail.TextTrackReady && strings.TrimSpace(existing.TextContent) != "" {
			result.SkippedBetter++
			continue
		}
		tracks = append(tracks, detail.TextTrack{AssetID: assetID, LanguageCode: language, TextKind: candidate.kind, TextContent: text, SourceType: detail.TextSourceQdrantRecovery, SourceLanguageCode: language, IsOriginal: true, Provider: "qdrant", ModelName: historical.Projection, TextHash: detail.TextHash(text, language, candidate.kind), Status: detail.TextTrackReady, IsCurrent: true})
	}
	if len(tracks) == 0 {
		return result, nil
	}
	if err := s.committer.CommitRecoveredText(ctx, assetID, language, tracks, historical.Projection); err != nil {
		return RecoveryResult{}, fmt.Errorf("persist recovered text and event atomically: %w", err)
	}
	result.Recovered, result.ReindexQueued = len(tracks), true
	return result, nil
}
