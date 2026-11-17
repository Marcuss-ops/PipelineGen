package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SubtitleCue is the wire form of one timed caption. It mirrors the Rust
// `semantic_core::cue_timing::TimedCue` and the Go `detail.TimedCue`.
type SubtitleCue struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// SubtitlePolicy is the short-form readability contract. A zero field means
// "canonical default", so callers may send only the axes they override.
type SubtitlePolicy struct {
	MaxCPS          float64 `json:"max_cps,omitempty"`
	MinCueMs        int64   `json:"min_cue_ms,omitempty"`
	MaxCharsPerLine int     `json:"max_chars_per_line,omitempty"`
	MaxLines        int     `json:"max_lines,omitempty"`
	MaxGapMergeMs   int64   `json:"max_gap_merge_ms,omitempty"`
}

type subtitlesKernelRequest struct {
	Operation      string          `json:"operation"`
	Mode           string          `json:"mode"`
	Cues           []SubtitleCue   `json:"cues,omitempty"`
	Text           string          `json:"text,omitempty"`
	StyleID        string          `json:"style_id,omitempty"`
	ClipDurationMs int64           `json:"clip_duration_ms,omitempty"`
	Filename       string          `json:"filename,omitempty"`
	Content        string          `json:"content,omitempty"`
	Policy         *SubtitlePolicy `json:"policy,omitempty"`
}

type subtitlesKernelResponse struct {
	OK           bool          `json:"ok"`
	Error        string        `json:"error,omitempty"`
	Cues         []SubtitleCue `json:"cues,omitempty"`
	PlainText    string        `json:"plain_text,omitempty"`
	ASSContent   string        `json:"ass_content,omitempty"`
	CueCount     int           `json:"cue_count,omitempty"`
	LastCueEndMs int64         `json:"last_cue_end_ms,omitempty"`
}

// callSubtitlesKernel is the single boundary to the Rust cue kernel. Every
// response is a typed result: an `ok:false` payload becomes a Go error and is
// never substituted with a locally computed fallback.
func (a *SemanticEngineAdapter) callSubtitlesKernel(ctx context.Context, wire subtitlesKernelRequest) (*subtitlesKernelResponse, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	wire.Operation = "subtitles_kernel"
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode subtitles_kernel: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: subtitles_kernel execute: %w", err)
	}
	var response subtitlesKernelResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: decode subtitles_kernel: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine: subtitles_kernel worker: %s", strings.TrimSpace(response.Error))
	}
	return &response, nil
}

// NormalizeSubtitleCues rewrites source cues into readable short-form
// captions. An empty input is an empty output: captions are never invented.
func (a *SemanticEngineAdapter) NormalizeSubtitleCues(ctx context.Context, cues []SubtitleCue, policy *SubtitlePolicy) ([]SubtitleCue, error) {
	if len(cues) == 0 {
		return nil, nil
	}
	response, err := a.callSubtitlesKernel(ctx, subtitlesKernelRequest{Mode: "normalize", Cues: cues, Policy: policy})
	if err != nil {
		return nil, err
	}
	return response.Cues, nil
}

// DistributeSubtitleCues projects an already translated full transcript onto
// the canonical source cue windows, preserving timing, order and segmentation.
func (a *SemanticEngineAdapter) DistributeSubtitleCues(ctx context.Context, timing []SubtitleCue, text string) ([]SubtitleCue, error) {
	if len(timing) == 0 {
		return nil, fmt.Errorf("semantic-engine: distribute requires canonical timing")
	}
	response, err := a.callSubtitlesKernel(ctx, subtitlesKernelRequest{Mode: "distribute", Cues: timing, Text: text})
	if err != nil {
		return nil, err
	}
	if len(response.Cues) != len(timing) {
		return nil, fmt.Errorf("semantic-engine: distribute returned %d cues for %d windows", len(response.Cues), len(timing))
	}
	for i, cue := range response.Cues {
		if cue.StartMs != timing[i].StartMs || cue.EndMs != timing[i].EndMs {
			return nil, fmt.Errorf("semantic-engine: distribute moved the window of cue %d", i)
		}
	}
	return response.Cues, nil
}

// CompileSubtitleASS is the canonical deterministic ASS generator. Identical
// cues + style always produce identical bytes; empty cues fail closed.
func (a *SemanticEngineAdapter) CompileSubtitleASS(ctx context.Context, cues []SubtitleCue, styleID string) (string, error) {
	if len(cues) == 0 {
		return "", fmt.Errorf("semantic-engine: compile requires at least one cue")
	}
	response, err := a.callSubtitlesKernel(ctx, subtitlesKernelRequest{Mode: "compile", Cues: cues, StyleID: styleID})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(response.ASSContent) == "" {
		return "", fmt.Errorf("semantic-engine: compile returned empty ASS content")
	}
	if response.CueCount != len(cues) {
		return "", fmt.Errorf("semantic-engine: compile returned %d cues for %d inputs", response.CueCount, len(cues))
	}
	if got, want := response.LastCueEndMs, cues[len(cues)-1].EndMs; got != want {
		return "", fmt.Errorf("semantic-engine: compile last cue end %d does not match input %d", got, want)
	}
	return response.ASSContent, nil
}

// ValidateSubtitleASS runs the single structural validator before an artifact
// is sealed into a plan.
func (a *SemanticEngineAdapter) ValidateSubtitleASS(ctx context.Context, content string, clipDurationMs int64) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("semantic-engine: validate requires ASS content")
	}
	_, err := a.callSubtitlesKernel(ctx, subtitlesKernelRequest{Mode: "validate", Content: content, ClipDurationMs: clipDurationMs})
	return err
}

// ParseSubtitleText extracts (plain_text, cues) from VTT or SRT content. The
// format is selected from the filename extension, matching the Go parser.
func (a *SemanticEngineAdapter) ParseSubtitleText(ctx context.Context, filename, content string) (string, []SubtitleCue, error) {
	response, err := a.callSubtitlesKernel(ctx, subtitlesKernelRequest{Mode: "parse", Filename: filename, Content: content})
	if err != nil {
		return "", nil, err
	}
	if len(response.Cues) == 0 {
		return "", nil, fmt.Errorf("semantic-engine: parse returned no cues for %q", filename)
	}
	return response.PlainText, response.Cues, nil
}
