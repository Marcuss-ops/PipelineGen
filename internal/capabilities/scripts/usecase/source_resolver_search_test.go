package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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
	tracks map[string]*detail.TextTrack
}

func (s *stubTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	if kind != detail.TextTrackTranscript {
		return nil, nil, nil
	}
	key := assetID + ":" + languageCode
	if track, ok := s.tracks[key]; ok {
		return track, nil, nil
	}
	return nil, nil, nil
}

func (s *stubTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
	if kind != detail.TextTrackTranscript {
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

func makeTrack(assetID, language, text string) *detail.TextTrack {
	hash := fmt.Sprintf("%x", len(text))
	return &detail.TextTrack{
		AssetID:       assetID,
		LanguageCode:  language,
		TextKind:      detail.TextTrackTranscript,
		TextContent:   text,
		SourceType:    detail.TextSourceProvided,
		ModelVersion:  "test",
		TextHash:      hash,
		SourceVersion: "test",
		Status:        detail.TextTrackReady,
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
