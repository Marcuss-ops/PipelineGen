package rustexec

// Scene Intelligence batch analyzer (P0.1).
//
// ONE persistent-process call analyzes N scenes: the Go side builds a single
// `{operation:analyze, scenes, entities_per_scene}` request for the
// `scene_intelligence` Rust worker (binary `bin/scene_intelligence`, mode
// `scene-intelligence`) and decodes + validates the single manifest. This
// replaces N per-scene spawns with one pipelined round-trip; the worker
// sorts scenes canonically so request order never affects the manifest.
//
// Fail-soft contract (same as phrase-impact): worker unavailable or
// transport failure returns an error and the caller continues WITHOUT
// Scene Intelligence — never failing the video. Contract errors (empty
// scenes, bad windows, schema violations) are also errors the caller must
// treat as "manifest not certified" rather than retrying blindly.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const siWorkerOutputLimit = 8 << 20

// SIAnalyzer drives the persistent scene-intelligence Rust worker.
type SIAnalyzer struct {
	binary string
	runner RustProcessRunner
}

// NewSIAnalyzer builds the analyzer; a nil runner gets the shared
// persistent process runner (same as the phrase-impact analyzer).
func NewSIAnalyzer(binary string, runner RustProcessRunner) *SIAnalyzer {
	if runner == nil {
		runner = newPersistentRustProcessRunner()
	}
	return &SIAnalyzer{binary: binary, runner: runner}
}

type siWorkerScene struct {
	SceneID      string `json:"scene_id"`
	Text         string `json:"text"`
	Language     string `json:"language,omitempty"`
	VideoStartMS int64  `json:"video_start_ms"`
	VideoEndMS   int64  `json:"video_end_ms"`
}

type siAnalyzeResponse struct {
	OK       bool                                `json:"ok"`
	Error    string                              `json:"error"`
	Manifest scriptpkg.SceneIntelligenceManifest `json:"manifest"`
}

type siSplitResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	Sentences []struct {
		Text      string `json:"text"`
		StartByte int    `json:"start_byte"`
		EndByte   int    `json:"end_byte"`
		SceneID   string `json:"scene_id"`
	} `json:"sentences"`
}

// AnalyzeBatch analyzes all scenes in ONE worker round-trip.
func (a *SIAnalyzer) AnalyzeBatch(ctx context.Context, scenes []SISceneRequest) (*scriptpkg.SceneIntelligenceManifest, error) {
	if len(scenes) == 0 {
		return nil, fmt.Errorf("scene intelligence batch requires scenes")
	}
	if a == nil || a.runner == nil || strings.TrimSpace(a.binary) == "" {
		return nil, fmt.Errorf("scene-intelligence Rust worker is not configured")
	}
	workerScenes := make([]siWorkerScene, len(scenes))
	entities := make([][]string, len(scenes))
	for i, s := range scenes {
		if strings.TrimSpace(s.SceneID) == "" || strings.TrimSpace(s.Text) == "" {
			return nil, fmt.Errorf("scene intelligence batch requires scene id and text")
		}
		if s.VideoEndMS <= s.VideoStartMS {
			return nil, fmt.Errorf("scene intelligence batch requires a valid video window")
		}
		workerScenes[i] = siWorkerScene{
			SceneID: s.SceneID, Text: s.Text, Language: s.Language,
			VideoStartMS: s.VideoStartMS, VideoEndMS: s.VideoEndMS,
		}
		entities[i] = append([]string(nil), s.Entities...)
	}
	payload, err := json.Marshal(map[string]any{
		"operation": "analyze", "scenes": workerScenes, "entities_per_scene": entities,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal scene-intelligence batch request: %w", err)
	}
	stdout, stderr, err := a.runner.Run(ctx, a.binary, append(payload, '\n'), siWorkerOutputLimit)
	if err != nil {
		return nil, fmt.Errorf("run scene-intelligence Rust worker: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	var response siAnalyzeResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &response); err != nil {
		return nil, fmt.Errorf("decode scene-intelligence Rust response: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("scene-intelligence Rust analysis: %s", response.Error)
	}
	raw, err := json.Marshal(response.Manifest)
	if err != nil {
		return nil, fmt.Errorf("re-encode scene-intelligence manifest: %w", err)
	}
	return DecodeSIManifest(raw)
}

// SplitSentencesViaWorker splits text with the canonical Rust boundary and
// verifies UTF-8 byte offsets (same pin as the phrase-impact split path).
func (a *SIAnalyzer) SplitSentencesViaWorker(ctx context.Context, text, sceneID string) ([]string, error) {
	if a == nil || a.runner == nil || strings.TrimSpace(a.binary) == "" {
		return nil, fmt.Errorf("scene-intelligence Rust worker is not configured")
	}
	payload, err := json.Marshal(map[string]string{
		"operation": "split_sentences", "text": text, "scene_id": sceneID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal scene-intelligence split request: %w", err)
	}
	stdout, stderr, err := a.runner.Run(ctx, a.binary, append(payload, '\n'), siWorkerOutputLimit)
	if err != nil {
		return nil, fmt.Errorf("run scene-intelligence Rust split: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	var response siSplitResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &response); err != nil {
		return nil, fmt.Errorf("decode scene-intelligence split response: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("scene-intelligence Rust split: %s", response.Error)
	}
	sentences := make([]string, len(response.Sentences))
	for i, sentence := range response.Sentences {
		if sentence.StartByte < 0 || sentence.EndByte <= sentence.StartByte ||
			sentence.EndByte > len(text) || text[sentence.StartByte:sentence.EndByte] != sentence.Text {
			return nil, fmt.Errorf("scene-intelligence sentence %d has invalid UTF-8 byte offsets", i)
		}
		sentences[i] = sentence.Text
	}
	return sentences, nil
}
