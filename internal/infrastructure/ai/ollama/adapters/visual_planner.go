// Package adapters — visual_planner.go bridges the application-layer
// VisualCandidatePlanner port with the real Ollama backend.
//
// The planner receives a closed list of candidates for a single scene
// slot, asks the LLM to pick the best one, validates the returned asset
// ID, and falls back to the highest-scoring candidate deterministically
// when the LLM fails, times out, or returns an unknown/invalid ID.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	"go.uber.org/zap"
)

// visualPlannerChatClient is the narrow LLM surface needed by the
// planner. It is implemented by *client.Client.
type visualPlannerChatClient interface {
	Chat(ctx context.Context, messages []types.Message, options map[string]any, format json.RawMessage) (string, error)
}

// OllamaVisualPlannerAdapter implements scriptadapters.VisualCandidatePlanner
// by delegating candidate selection to an Ollama-backed LLM.
type OllamaVisualPlannerAdapter struct {
	client visualPlannerChatClient
	log    *zap.Logger
}

// NewOllamaVisualPlannerAdapter constructs the adapter. client may be
// nil; in that case Select falls back to the deterministic scorer.
func NewOllamaVisualPlannerAdapter(client visualPlannerChatClient, log *zap.Logger) *OllamaVisualPlannerAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &OllamaVisualPlannerAdapter{client: client, log: log}
}

// Select asks the LLM to choose one candidate from the closed list.
// It always returns a valid candidate ID: either the LLM's validated
// choice or the highest-scoring candidate as a deterministic fallback.
func (p *OllamaVisualPlannerAdapter) Select(ctx context.Context, req scriptadapters.VisualSelectionRequest) (string, error) {
	if len(req.Candidates) == 0 {
		return "", fmt.Errorf("no candidates provided")
	}

	fallback := deterministicFallback(req.Candidates)

	if p.client == nil {
		p.log.Debug("visual planner: no LLM client wired, using deterministic fallback", zap.String("scene_id", req.SceneID))
		return fallback, nil
	}

	llmID, err := p.askLLM(ctx, req)
	if err != nil {
		p.log.Warn("visual planner: LLM selection failed, using deterministic fallback",
			zap.String("scene_id", req.SceneID),
			zap.String("segment_id", req.SegmentID),
			zap.String("slot", string(req.Slot)),
			zap.Error(err))
		return fallback, nil
	}

	if llmID == "" {
		p.log.Debug("visual planner: LLM returned empty selection, using deterministic fallback",
			zap.String("scene_id", req.SceneID))
		return fallback, nil
	}

	if !candidateIDExists(req.Candidates, llmID) {
		p.log.Warn("visual planner: LLM returned an unknown asset ID, using deterministic fallback",
			zap.String("scene_id", req.SceneID),
			zap.String("selected_asset_id", llmID),
			zap.String("fallback_asset_id", fallback))
		return fallback, nil
	}

	return llmID, nil
}

// askLLM builds the prompt and returns the raw selected_asset_id string.
func (p *OllamaVisualPlannerAdapter) askLLM(ctx context.Context, req scriptadapters.VisualSelectionRequest) (string, error) {
	prompt := buildVisualPlannerPrompt(req)

	messages := []types.Message{
		{Role: "system", Content: "You are a visual planning assistant. You select the single best matching asset from a closed candidate list for a video scene and slot. Always return valid JSON with exactly one field: selected_asset_id."},
		{Role: "user", Content: prompt},
	}

	opts := map[string]any{
		"temperature": 0.2,
		"num_predict": 128,
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := p.client.Chat(ctx, messages, opts, json.RawMessage(`"json"`))
	if err != nil {
		return "", err
	}

	return extractSelectedAssetID(resp)
}

func buildVisualPlannerPrompt(req scriptadapters.VisualSelectionRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SCENE TEXT:\n%s\n\n", req.Text)
	if req.VisualIntent != "" {
		fmt.Fprintf(&b, "VISUAL INTENT:\n%s\n\n", req.VisualIntent)
	}
	fmt.Fprintf(&b, "SLOT:\n%s\n\n", req.Slot)
	if req.SegmentID != "" {
		fmt.Fprintf(&b, "SEGMENT ID:\n%s\n\n", req.SegmentID)
	}
	fmt.Fprintln(&b, "CANDIDATES (closed list, choose one by asset_id):")
	for _, c := range req.Candidates {
		fmt.Fprintf(&b, "- asset_id=%s provider=%s media_type=%s score=%.3f\n",
			c.AssetID, c.Provider, c.MediaType, c.Score)
	}
	fmt.Fprintln(&b, "\nReturn ONLY valid JSON in this exact format: {\"selected_asset_id\": \"<the_chosen_asset_id>\"}")
	return b.String()
}

func extractSelectedAssetID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty LLM response")
	}

	// Ollama JSON mode sometimes returns a leading code block; strip it.
	clean := stripMarkdownCodeBlock(raw)

	var parsed struct {
		SelectedAssetID string `json:"selected_asset_id"`
	}
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return "", fmt.Errorf("parse LLM response: %w", err)
	}
	return strings.TrimSpace(parsed.SelectedAssetID), nil
}

func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 2 && (strings.HasPrefix(lines[0], "```") || strings.HasPrefix(lines[len(lines)-1], "```")) {
			start, end := 0, len(lines)
			if strings.HasPrefix(lines[0], "```") {
				start = 1
			}
			if strings.HasPrefix(lines[len(lines)-1], "```") {
				end = len(lines) - 1
			}
			s = strings.Join(lines[start:end], "\n")
		}
	}
	return s
}

func candidateIDExists(candidates []mediamemory.CandidateOption, id string) bool {
	for _, c := range candidates {
		if c.AssetID == id {
			return true
		}
	}
	return false
}

func deterministicFallback(candidates []mediamemory.CandidateOption) string {
	best := candidates[0].AssetID
	bestScore := candidates[0].Score
	for _, c := range candidates {
		if c.Score > bestScore {
			bestScore = c.Score
			best = c.AssetID
		}
	}
	return best
}
