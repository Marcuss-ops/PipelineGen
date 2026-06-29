// Package voiceover — types.go (P0.2 wire-shape, June 2026).
//
// GenerateVoiceoversRequest is the canonical HTTP wire shape for
// POST /api/media/voiceover/generate. It supersedes the previous
// flat handler binding of *application.GenerateVoiceoversCommand
// directly: the previous BindJSON target was the internal Command,
// which violated AGENTS.md Pattern 6 (wire-shape/payload split) —
// any future rename of the Command field set would have leaked to
// the public API. The wire-shape/payload split keeps the API
// contract stable across internal refactors.
//
// Canonical wire shape (JSON, snake_case):
//
//   {
//     "request_id": "video-123",
//     "items": [
//       {"text": "...", "language": "it-IT", "voice": "...", "filename": "intro-it.mp3"},
//       ...
//     ],
//     "destination": {"kind": "group", "group": "Promozionali"},
//     "options": {"remove_silence": true, "strategy": "replace", "parallelism": 2}
//   }
//
// Translation contract (P0.2): items[] is collapsed into the
// canonical GenerateVoiceoversCommand shape (1 Text + N Languages +
// VoiceOverrides map keyed by language). All items MUST share the
// same text — mixed-text requests return 400; full per-item
// multi-text fan-out is P0.3 scope (parent → child job per item).
//
// PR-VO-C1 invariant: destination.kind="group" + empty group is a
// hard 400 ("no fake availability" per godlike/07).
package voiceover

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// GenerateVoiceoversRequest is the canonical HTTP wire shape for
// POST /api/media/voiceover/generate.
type GenerateVoiceoversRequest struct {
	// RequestID is the caller-supplied correlation identifier; it
	// is wired to the canonical kernel EnqueueRequest.CorrelationID
	// so the worker side log stream and the dispatcher audit both
	// carry the original caller intent across the async boundary.
	RequestID string `json:"request_id,omitempty"`

	// Items is the list of (text, language, voice, filename) rows.
	// All items MUST share the same text — mixed-text requests are
	// rejected with 400. Per-item filename overrides the
	// FilenameTemplate (last non-empty wins; with shared-text
	// invariant this is the only non-empty entry).
	Items []VoiceoverItem `json:"items"`

	// Destination is the optional routing payload (Group/FolderID/...).
	Destination *voiceover.DestinationRequest `json:"destination,omitempty"`

	// Options wraps three optional fields from the canonical Command
	// that callers prefer as a sub-map (matches the verdict body).
	Options VoiceoverOptions `json:"options,omitempty"`
}

// VoiceoverItem is a single (text, language, voice, filename) row
// in the request items[] array.
type VoiceoverItem struct {
	Text     string `json:"text"`
	Language string `json:"language"`
	Voice    string `json:"voice,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// VoiceoverOptions is the options sub-map. Mirrors the canonical
// Command's optional fields that callers want as a sub-map (the
// metadata field is exposed here so godlike/07 no-fake-availability
// is honoured — silent loss of caller-supplied metadata would be
// the worst kind of fake availability).
type VoiceoverOptions struct {
	// RemoveSilence toggles AudioPostProcessor.PostProcess after TTS.
	RemoveSilence bool `json:"remove_silence,omitempty"`
	// Strategy is the pipeline strategy (verify / skip / replace).
	// Unknown values are coerced to "verify" by asset.NormalizeStrategy
	// (the canonical normaliser at the use-case boundary).
	Strategy string `json:"strategy,omitempty"`
	// Parallelism is the requested fan-out concurrency (clamped
	// by the use case to min(requested, MaxParallelism, len(Languages))).
	Parallelism int `json:"parallelism,omitempty"`
	// Metadata is the user-supplied meta overlay that flows into the
	// row's metadata column (process_metadata_test.go pins the
	// collision-drop contract from mergeUserMetadata).
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Validate runs the canonical request-side validation. Returns nil
// when every slot is safe to consume downstream. Each check is
// deliberately a no-op (returns nil) when the slot is empty so
// absence stays a legitimate signal.
//
// PR-VO-C1 / godlike/07 invariant: destination.kind="group" + empty
// group is a hard 400 ("no fake availability" rule).
func (r *GenerateVoiceoversRequest) Validate() error {
	if r == nil {
		return errors.New("nil request")
	}
	if len(r.Items) == 0 {
		return errors.New("items: must contain at least one item")
	}

	text0 := r.Items[0].Text
	for i, it := range r.Items {
		if it.Text == "" {
			return fmt.Errorf("items[%d].text: must be non-empty", i)
		}
		if it.Language == "" {
			return fmt.Errorf("items[%d].language: must be non-empty (BCP-47 code)", i)
		}
		if i > 0 && it.Text != text0 {
			return fmt.Errorf(
				"items[%d].text: differs from items[0].text (multi-text per-item fan-out is P0.3 scope; "+
					"all items must share the same text in P0.2)",
				i,
			)
		}
	}

	if r.Destination != nil {
		// PR-VO-C1 invariants (godlike/07 no-fake-availability):
		//   - kind="group" + empty group     → hard 400 (GroupsResolver would 404)
		//   - kind="explicit" + empty folder_id → hard 400 (would silently fall back to GroupsResolver)
		// Both branches raise the same fail-closed contract: a parseable
		// payload that names "explicit" routing but carries no folder_id
		// MUST NOT silently auto-resolve.
		if r.Destination.Kind == "group" && r.Destination.Group == "" {
			return errors.New("destination: kind=\"group\" requires non-empty group")
		}
		if r.Destination.Kind == "explicit" && r.Destination.FolderID == "" {
			return errors.New("destination: kind=\"explicit\" requires non-empty folder_id")
		}
		if err := r.Destination.Validate(); err != nil {
			return fmt.Errorf("destination: %w", err)
		}
	}

	return nil
}

// ToCommand collapses items[] into the canonical
// GenerateVoiceoversCommand shape (1 Text + N Languages + VoiceOverrides map).
// Strategy is normalised via asset.NormalizeStrategy(force=false:
// unknown values coerce to "verify").
//
// Per-item filename overrides the FilenameTemplate (last non-empty wins).
// Under the shared-text invariant in P0.2, at most one item carries a
// non-empty filename so the loop ends with the right value.
func (r *GenerateVoiceoversRequest) ToCommand() *voiceover.GenerateVoiceoversCommand {
	items := r.Items
	text := items[0].Text
	languages := make([]string, 0, len(items))
	voiceOverrides := make(map[string]string, len(items))
	filenameTemplate := ""
	for _, it := range items {
		languages = append(languages, it.Language)
		if it.Voice != "" {
			voiceOverrides[it.Language] = it.Voice
		}
		if it.Filename != "" {
			filenameTemplate = it.Filename
		}
	}

	return &voiceover.GenerateVoiceoversCommand{
		Text:             text,
		Languages:        languages,
		FilenameTemplate: filenameTemplate,
		VoiceOverrides:   voiceOverrides,
		Destination:      r.Destination,
		Strategy:         asset.NormalizeStrategy(r.Options.Strategy, false),
		RemoveSilence:    r.Options.RemoveSilence,
		Parallelism:      r.Options.Parallelism,
		// Metadata is forwarded verbatim through the canonical Command
		// (the worker's journal mergeUserMetadata uses it for collision
		// resolution on the voiceovers row's metadata column).
		Metadata: r.Options.Metadata,
	}
}

// ToEnqueueRequest builds the canonical kernel EnqueueRequest with
// CorrelationID populated from RequestID. The use case Execute path
// reads only the Payload; the worker log stream and the dispatcher
// audit both read CorrelationID so request-scoped tracing works
// across the async boundary end-to-end.
func (r *GenerateVoiceoversRequest) ToEnqueueRequest() *jobservice.EnqueueRequest {
	return &jobservice.EnqueueRequest{
		Type:          jobservice.TypeVoiceoverGenerate,
		Payload:       r.ToCommand(),
		CorrelationID: r.RequestID,
	}
}
