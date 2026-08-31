// Package app — voiceover TTSProvider + AudioPostProcessor adapters
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Capability cluster: AUDIO synthesis (text→speech via *audioasset.Processor)
// + AUDIO post-processing through the media execution port.
//
// Both adapters satisfy Pattern 0 narrow ports declared in
// internal/capabilities/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *audio
// imports.
//
// TTSProvider                   ← *audioasset.Processor
// AudioPostProcessor            ← mediaexec.AudioProcessor adapter
//
// Fail-closed: nil proc panics at construction (fail-fast per
// AGENTS.md WireUp pattern). The AudioPostProcessor constructor is
// non-panicking (nil-safe log field is acceptable).
package wiring

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/platform/audio"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// TTSProvider adapter.
//
// Bridges *audioasset.Processor → voiceover.TTSProvider. The use case
// only ever supplies well-formed inputs (no path-traversal payloads
// past cmd.Validate), so the lower-level AudioInput fields EscapeHook
// semantics — UseStdin defaults to false. AudioResult carries
// LocalPath + CleanedPath + Voice + LegacyFileMD5 which map 1-a-1 to
// TTSOutput.
// ─────────────────────────────────────────────────────────────────────

// ttsGenerator is the narrow port the adapter depends on. The concrete
// *audioasset.Processor.Generate satisfies it; the local interface mirrors
// processorShape in the audioasset package (unexported there) so the
// adapter remains testable with a fake generator.
type ttsGenerator interface {
	Generate(ctx context.Context, input *audioasset.AudioInput) (*audioasset.AudioResult, error)
}

type useCaseTTSAdapter struct {
	proc ttsGenerator
}

func newUseCaseTTSAdapter(proc ttsGenerator) *useCaseTTSAdapter {
	if proc == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseTTSAdapter: proc is required (ttsGenerator)")
	}
	return &useCaseTTSAdapter{proc: proc}
}

func (a *useCaseTTSAdapter) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	input := &audioasset.AudioInput{
		Text: in.Text,
		// PR-VO-TYPED-PRIMITIVES (July 2026): in.Language is the
		// typed voiceover.Language envelope. The cross-package seam
		// converts to the raw string for audioasset.AudioInput
		// (the infrastructure layer stays un-typed per the audit
		// scope discipline).
		Language:      string(in.Language),
		Voice:         in.Voice,
		Filename:      in.Filename,
		OutputDir:     in.OutputDir,
		RemoveSilence: in.RemoveSilence,
		// UseStdin defaults to false: the use case path delivers
		// bounded, validated text inputs (POST /generate path →
		// command.Validate path-traversal rejection before field
		// access, mirrors TestGenerateBatch_RejectsPathTraversalPayload).
	}

	res, err := a.proc.Generate(ctx, input)
	if err != nil {
		return voiceover.TTSOutput{}, err
	}

	// Surface the RAW provider word boundaries captured in the same
	// synthesis stream as the audio. The canonical SpeechTimingArtifact
	// (hashes + monotonic validation + silence remap) is built by the use
	// case, never here — the adapter only normalizes the metadata.jsonl
	// lines into the provider-neutral shape.
	var boundaries []voiceover.RawSpeechBoundary
	skippedCorrupt := 0
	if res.MetadataPath != "" {
		boundaries, skippedCorrupt, err = parseEdgeWordBoundaries(res.MetadataPath)
		if err != nil {
			return voiceover.TTSOutput{}, fmt.Errorf("voiceover TTS: parse word boundaries %q: %w", res.MetadataPath, err)
		}
		// A truncated/partial boundary write (NUL bytes) loses one or more
		// word boundaries. Retry the synthesis once for a clean capture
		// before accepting the degraded result, so a one-off bridge glitch
		// never ships a silently incomplete timing artifact. The retry
		// re-runs the whole synthesis (audio + boundaries come from the SAME
		// stream), so res and boundaries stay consistent when the retry wins.
		if skippedCorrupt > 0 {
			if retryRes, retryErr := a.proc.Generate(ctx, input); retryErr == nil && retryRes != nil && retryRes.MetadataPath != "" {
				if retryBoundaries, retrySkipped, parseErr := parseEdgeWordBoundaries(retryRes.MetadataPath); parseErr == nil && retrySkipped == 0 {
					res = retryRes
					boundaries = retryBoundaries
				}
			}
		}
		// Boundary metadata is an intermediate Edge TTS artifact. It has
		// already been parsed (and, when needed, retried), so do not leave
		// one JSONL file per scene in the job workspace.
		_ = os.Remove(res.MetadataPath)
	}

	out := voiceover.TTSOutput{
		LocalPath:     res.LocalPath,
		CleanedPath:   res.CleanedPath,
		Voice:         res.Voice,
		LegacyFileMD5: res.LegacyFileMD5,
		Duration:      res.Duration,
		// The canonical provider identity for the Edge TTS bridge. The
		// bridge captures audio + WordBoundary in ONE synthesis pass, so
		// the provider identity is fixed here (never inferred downstream).
		Provider: "edge-tts",
	}
	if len(boundaries) > 0 {
		out.BoundaryMode = audio.BoundaryWord
		out.WordBoundaries = boundaries
	}
	return out, nil
}

// edgeBoundaryLine is one metadata.jsonl line from the Edge TTS bridge
// (see scripts/bridges/edge_tts_bridge/boundaries.py::boundary_line).
// Values are already normalized from Edge 100ns ticks to integer
// microseconds at the single conversion site in the bridge.
type edgeBoundaryLine struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

// parseEdgeWordBoundaries reads the bridge's metadata.jsonl and returns
// the provider-neutral raw word boundaries in file order, plus the number
// of corrupt boundary lines skipped. Non-WordBoundary lines are skipped
// defensively. A corrupt line (a truncated/partial write — typically NUL
// bytes from an interrupted bridge stream, which surface as invalid JSON)
// is skipped rather than failing the whole synthesis: losing one boundary
// is strictly better than failing a run, and the downstream canonical
// SpeechTimingArtifact validation still catches gross corruption. The
// caller uses skippedCorrupt to decide whether to retry the synthesis for
// a clean capture.
func parseEdgeWordBoundaries(metadataPath string) ([]voiceover.RawSpeechBoundary, int, error) {
	f, err := os.Open(metadataPath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var boundaries []voiceover.RawSpeechBoundary
	skippedCorrupt := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry edgeBoundaryLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			skippedCorrupt++
			continue
		}
		if entry.Type != "WordBoundary" {
			continue
		}
		boundaries = append(boundaries, voiceover.RawSpeechBoundary{
			Text:    entry.Text,
			StartUS: entry.StartUS,
			EndUS:   entry.EndUS,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, skippedCorrupt, err
	}
	return boundaries, skippedCorrupt, nil
}

var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// AudioPostProcessor adapter.
//
// Implements voiceover.AudioPostProcessor.Process by wrapping the
// mediaexec.AudioProcessor.RemoveSilence capability. The cleaned-path
// convention is deterministic: <OutputDir>/cleaned_<Filename> so
// filename uniqueness rules (P1.3) keep the call surface predictable.
// Nil-safe at the use case boundary (only invoked when
// cmd.RemoveSilence == true).
// ─────────────────────────────────────────────────────────────────────

type useCaseAudioAdapter struct {
	log   *zap.Logger
	media mediaexec.AudioProcessor
}

func newUseCaseAudioAdapter(log *zap.Logger, media mediaexec.AudioProcessor) *useCaseAudioAdapter {
	return &useCaseAudioAdapter{log: log, media: media}
}

func (a *useCaseAudioAdapter) Process(ctx context.Context, in voiceover.AudioPostInput) (voiceover.AudioPostOutput, error) {
	if in.LocalPath == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty LocalPath (use case passed a missing local path)")
	}
	if in.OutputDir == "" || in.Filename == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty OutputDir/Filename (use case contract violation)")
	}
	// clean file goes to <OutputDir>/cleaned_<basename>; matches the
	// canonical convention used by audioasset.Processor (processor.go:103).
	cleaned := in.OutputDir + "/cleaned_" + in.Filename
	if a.log != nil {
		a.log.Info("voiceover.audio_post: stripping silence",
			zap.String("input", in.LocalPath),
			zap.String("output_dir", in.OutputDir),
			zap.String("filename", in.Filename))
	}
	if a.media == nil {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: media executor unavailable")
	}
	if err := a.media.RemoveSilence(ctx, in.LocalPath, cleaned); err != nil {
		if a.log != nil {
			a.log.Warn("voiceover.audio_post: RemoveSilence failed (caller decides whether to fail-fast)",
				zap.String("input", in.LocalPath),
				zap.String("output", cleaned),
				zap.Error(err))
		}
		return voiceover.AudioPostOutput{}, err
	}
	// Probe the final file immediately: callers use this duration for the
	// scene timeline, never the pre-clean Edge duration.
	var durationUS int64
	if info, err := a.media.Probe(ctx, cleaned); err == nil && info != nil {
		durationUS = info.Duration.Microseconds()
	} else if err != nil {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: probe cleaned audio: %w", err)
	}
	return voiceover.AudioPostOutput{CleanedPath: cleaned, DurationUS: durationUS}, nil
}

var _ voiceover.AudioPostProcessor = (*useCaseAudioAdapter)(nil)
