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
//	{
//	  "request_id": "video-123",
//	  "items": [
//	    {"text": "...", "language": "it-IT", "voice": "...", "filename": "intro-it.mp3"},
//	    ...
//	  ],
//	  "destination": {"kind": "group", "group": "Promozionali"},
//	  "options": {"remove_silence": true, "strategy": "replace", "parallelism": 2}
//	}
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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	jobvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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

	// Project is the canonical semantic project identifier for the
	// voiceover batch publish (ThreadingCampaign, 2026-07-08).
	// Threaded verbatim to GenerateVoiceoversCommand.Project by
	// ToCommand() (this file) and ultimately to delivery.Publisher.Publish
	// via the fanout loop (jobs/fanout.go) so voiceovers land in
	// `{project}/{language}/` Drive subdirs.
	//
	// godlike/06 SSOT (one canonical owner per fact): this field is
	// the SOURCE-OF-TRUTH for Project at the API layer; the parent
	// GenerateVoiceoversCommand is the storage OF-TRUTH through the
	// async boundary; the per-item GenerateVoiceoverItemCommand is the
	// WORKER CONTRACT OF-TRUTH. All three layers map 1:1 with no
	// transformation (string-in, string-out).
	//
	// godlike/07 minimum-blast-radius: empty Project is allowed and
	// falls through to the pre-P12 default (legacy FolderID + canonical
	// voiceover ID) so existing callers do not break.
	Project string `json:"project,omitempty"`
}

// VoiceoverItem is a single (text, language, voice, filename) row
// in the request items[] array.
//
// Step 5 (P0.3 items-model recovery, June 2026): the wire-shape and
// the application payload now share the SAME struct via a type
// alias — voiceover.VoiceoverItem is canonical, this alias keeps
// the existing `voiceover.VoiceoverItem` literal usage in handler
// tests compiling without renaming. No field-set drift.
type VoiceoverItem = voiceover.VoiceoverItem

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
	// Timing is the canonical voiceover timing policy (voiceover_timing
	// wire block). nil applies the canonical defaults
	// (best_effort / word / [json]) — timing capture is never
	// implicitly mandatory.
	Timing *audio.TimingRequest `json:"voiceover_timing,omitempty"`
}

// Validate runs the canonical request-side validation. Returns nil
// when every slot is safe to consume downstream. Each check is
// deliberately a no-op (returns nil) when the slot is empty so
// absence stays a legitimate signal.
//
// PR-VO-C1 / godlike/07 invariant: destination.kind="group" + empty
// group is a hard 400 ("no fake availability" rule).
//
// Step 5 (P0.3 items-model recovery, June 2026): the P0.2 shared-text
// invariant is REMOVED (mixed-text requests are first-class). Each
// item is validated independently — items MAY have different texts,
// duplicate languages with different voices, and per-item filenames.
// The P0.2 rule "items[i].text: differs from items[0].text" is gone.
func (r *GenerateVoiceoversRequest) Validate() error {
	if r == nil {
		return errors.New("nil request")
	}
	// Delegate to the canonical Command validator — the single source of
	// truth shared by API, worker, and internal callers. ToCommand() also
	// normalises Strategy (unknown → "verify"), so the validation runs
	// against the exact payload the worker will consume.
	return r.ToCommand().Validate()
}

// ToCommand converts the wire-shape request to the canonical
// application Command. Step 5 (P0.3 items-model recovery, June 2026):
// the conversion is now a 1:1 pass-through of Items — no text/languages
// collapse, no voice-override map, no last-wins filename. Each
// VoiceoverItem in the wire carries text/language/voice/filename
// independently; the application layer fans out one child per item.
//
// Strategy is normalised via asset.NormalizeStrategy(force=false:
// unknown values coerce to "verify"). Destination, RemoveSilence,
// Parallelism, Metadata forward verbatim (batch-level fields).
func (r *GenerateVoiceoversRequest) ToCommand() *voiceover.GenerateVoiceoversCommand {
	items := make([]voiceover.VoiceoverItem, len(r.Items))
	for i, it := range r.Items {
		// PR-PROMOTE-REQUIRED-FIX (2026-07-08): VoiceoverItem is a
		// type alias (`type VoiceoverItem = voiceover.VoiceoverItem`)
		// so the item-to-item copy is a 1:1 field-for-field
		// pass-through via direct assignment. The previous explicit
		// 4-field struct literal SILENTLY DROPPED the Required
		// field (FASE 2, July 2026) — a REQUIRED-failed child whose
		// item.Required=true reaches the API → the parent would
		// treat the failure as OPTIONAL (Compute() FASE 1 semantics),
		// breaking godlike/07 NO-FAKE-AVAILABILITY. ThreadingCampaign
		// iteration 2: the assignment below is the canonical fix;
		// future fields added to voiceover.VoiceoverItem also flow
		// through 1:1 with no additional code at this seam. The
		// lookup path voiceover.VoiceoverItem{...} is preserved
		// ONLY at the canonical child-emit site (jobs/fanout.go) so
		// the field-set drift surface stays narrow at the validate
		// seam (item.Validate() returns 400 BEFORE invalid input
		// reaches the fanout loop).
		items[i] = it
	}

	return &voiceover.GenerateVoiceoversCommand{
		Items:         items,
		Destination:   r.Destination,
		Strategy:      asset.NormalizeStrategy(r.Options.Strategy, false),
		RemoveSilence: r.Options.RemoveSilence,
		Parallelism:   r.Options.Parallelism,
		Timing:        r.Options.Timing,
		// Metadata is forwarded verbatim through the canonical Command
		// (the worker's journal mergeUserMetadata uses it for collision
		// resolution on the voiceovers row's metadata column).
		Metadata: r.Options.Metadata,
		// ThreadingCampaign 2026-07-08: thread the API request's
		// Project field 1:1 into the parent GenerateVoiceoversCommand
		// so the fanout loop (internal/application/voiceover/jobs/
		// fanout.go) can propagate it into each
		// GenerateVoiceoverItemCommand → delivery.Publisher.Publish
		// for the `{project}/{language}/` Drive subdir layout.
		Project: r.Project,
	}
}

// ToEnqueueRequest builds the canonical kernel EnqueueRequest with
// CorrelationID populated from RequestID and ActiveKey populated from
// a deterministic fingerprint of the request contents so the broker's
// per-ActiveKey idempotency dedupes across retry windows.
//
// Parent ActiveKey shape: voiceover:parent:<hex-sha256-prefix>
// covering (Text + Languages + Destination.FolderID). Two identical
// POSTs produce the same ActiveKey; the broker's FindActiveByKey
// returns the existing non-terminal job instead of enqueuing a
// duplicate.
func (r *GenerateVoiceoversRequest) ToEnqueueRequest() *job.EnqueueRequest {
	return &job.EnqueueRequest{
		Type:          jobvoiceover.TypeGenerate,
		Payload:       r.ToCommand(),
		CorrelationID: r.RequestID,
		ActiveKey:     r.parentActiveKey(),
	}
}

// parentActiveKey builds the deterministic dedup key for the parent
// voiceover.generate job. Covers every item's (text + language) and
// the destination folder. Two POSTs producing the same items set +
// same destination collide on the same ActiveKey; the broker's
// FindActiveByKey returns the existing non-terminal job instead of
// enqueuing a duplicate.
//
// Step 5 (P0.3 items-model recovery, June 2026): the P0.2 key
// fingerprint was based on items[0].Text + every language code; with
// mixed texts that contract collapses equivalent mixed-text requests
// to the same key. The new fingerprint hashes EVERY item's text +
// language independently so two structurally-different batch requests
// produce distinct ActiveKeys.
func (r *GenerateVoiceoversRequest) parentActiveKey() string {
	if len(r.Items) == 0 {
		return "voiceover:parent:empty"
	}
	h := sha256.New()
	for _, it := range r.Items {
		h.Write([]byte(it.Text))
		h.Write([]byte("|"))
		// PR-VO-TYPED-PRIMITIVES (July 2026): it.Language is the
		// typed voiceover.Language envelope (via the VoiceoverItem
		// type alias). strings.ToLower requires a string argument;
		// explicit conversion at this seam preserves the wire-stable
		// canonical-lowercase hash.
		h.Write([]byte(strings.ToLower(string(it.Language))))
		h.Write([]byte("|"))
	}
	if r.Destination != nil && r.Destination.FolderID != "" {
		h.Write([]byte(r.Destination.FolderID))
	}
	// PR-PROMOTE-REQUIRED-FIX (2026-07-08): only hash Project when
	// non-empty (back-compat byte-identical for empty Project). Do
	// NOT remove the `if r.Project != ""` guard — unconditional
	// append triggers a broker key-rotation storm. Audited by
	// TestRequest_parentActiveKey_EmptyProjectProducesLegacyHash.
	if r.Project != "" {
		h.Write([]byte("|"))
		h.Write([]byte(r.Project))
	}
	return "voiceover:parent:" + hex.EncodeToString(h.Sum(nil))[:16]
}
