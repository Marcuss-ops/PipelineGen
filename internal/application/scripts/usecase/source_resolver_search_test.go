package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type recordingSearchPort struct {
	lastQuery    string
	lastLimit    int
	lastLanguage string
	results      []SemanticSearchResult
	err          error
}

func (f *recordingSearchPort) SearchByText(_ context.Context, query string, limit int, language string) ([]SemanticSearchResult, error) {
	f.lastQuery = query
	f.lastLimit = limit
	f.lastLanguage = language
	return f.results, f.err
}

type stubTextTrackReader struct {
	tracks map[string]*asset.TextTrack
}

func (s *stubTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	if kind != asset.TextTrackTranscript {
		return nil, nil, nil
	}
	key := assetID + ":" + languageCode
	if track, ok := s.tracks[key]; ok {
		return track, nil, nil
	}
	return nil, nil, nil
}

func (s *stubTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	if kind != asset.TextTrackTranscript {
		return nil, nil
	}
	var langs []string
	for key := range s.tracks {
		if strings.HasPrefix(key, assetID+":") {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				langs = append(langs, parts[1])
			}
		}
	}
	return langs, nil
}

func makeTrack(assetID, language, text string) *asset.TextTrack {
	hash := fmt.Sprintf("%x", len(text))
	return &asset.TextTrack{
		AssetID:       assetID,
		LanguageCode:  language,
		TextKind:      asset.TextTrackTranscript,
		TextContent:   text,
		SourceType:    asset.TextSourceProvided,
		ModelVersion:  "test",
		TextHash:      hash,
		SourceVersion: "test",
		Status:        asset.TextTrackReady,
	}
}

func ptrFloat64(v float64) *float64 { return &v }

func ptrInt(v int) *int { return &v }

func slicesEqual[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
