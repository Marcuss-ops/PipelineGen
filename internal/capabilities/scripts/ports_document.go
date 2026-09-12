// Package scriptgeneration — ports_document.go: the document-publication
// port family.
//
// DocumentPublisher (and its preflight / renderer / enqueuer siblings) is the
// SOLE canonical owner of the Google Docs publication boundary; every port
// here returns domain types only (verdetto invariant, ports.go).
//
// Extracted 2026-09-12 from ports.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── DocumentPublisher ───────────────────────────────────────────────

// DocumentInput carries the data needed to publish a document.
type DocumentInput struct {
	RunID    string
	Language Language
	Title    string
	Content  string
	FolderID string
	// IdempotencyKey overrides the default Drive idempotency identity
	// (run_id + ":" + language). Callers that already ship a different
	// key convention MUST set it explicitly so a provider change cannot
	// silently re-key — and therefore duplicate — existing documents.
	IdempotencyKey string
	// ForceRefresh overwrites an existing document whose content hash
	// still matches. It exists for late-bound refreshes (voiceover and
	// clip subtitle links that arrive after the first publish).
	ForceRefresh bool
}

// DocumentPublisher is the SOLE canonical owner of the Google Docs
// publication surface. Every code path that publishes a script document
// (durable runner, post-processor) MUST route through this port.
//
// Verdetto: must be UPSERT, not CREATE — the identity is deterministic
// (generation_run_id + language). On retry the same document is updated
// rather than creating a duplicate.
type DocumentPublisher interface {
	// UpsertDocument creates a new document or updates an existing one
	// identified by (run_id, language) Drive properties.
	UpsertDocument(ctx context.Context, input DocumentInput) (DocumentReference, error)
}

// ErrDocumentReferencePreserved marks the narrow provider case where the
// document exists (and is usable) but a post-create idempotency annotation
// failed. Callers may safely use the returned reference and must not retry
// publication as a new create.
var ErrDocumentReferencePreserved = errors.New("documents: reference preserved after non-fatal idempotency annotation failure")

// DocumentIdempotencyKey returns the canonical Drive idempotency key for the
// durable-runner publication path, keyed by (generation run, language).
//
// It is a pure function of stable identity inputs — never of wall-clock time
// or randomness — so a retry, resume, or process restart derives the SAME key
// and the existing Google Doc is updated instead of being duplicated.
func DocumentIdempotencyKey(runID string, language Language) string {
	return strings.TrimSpace(runID) + ":" + strings.TrimSpace(string(language))
}

// DocumentPostProcessorIdempotencyKey returns the canonical Drive idempotency
// key for the post-processor publication path, keyed by (generation plan,
// language).
//
// VERDETTO: this intentionally uses a DISTINCT separator ("-") from the
// durable runner (":"). Both conventions already have documents in the wild,
// and re-keying either one would orphan the existing Doc and create a duplicate
// on the next publication. Both derivations are therefore pinned by
// TestDocumentIdempotencyKeys_Parity: changing a separator is a breaking change
// that requires a Drive-side migration, never a silent edit.
func DocumentPostProcessorIdempotencyKey(planID, language string) string {
	return planID + "-" + language
}

// ResolveDocumentIdempotencyKey returns the Drive idempotency key a publication
// request must use. An explicit DocumentInput.IdempotencyKey always wins, so a
// caller with an established convention never re-keys its existing documents;
// otherwise the canonical runner key is derived from the stable identity.
func ResolveDocumentIdempotencyKey(input DocumentInput) string {
	if key := strings.TrimSpace(input.IdempotencyKey); key != "" {
		return key
	}
	return DocumentIdempotencyKey(input.RunID, input.Language)
}

// DocumentPublisherPreflight is implemented only by real provider-backed
// publishers. A requested Google Doc must be validated before generation;
// test/no-op publishers intentionally do not satisfy this interface.
type DocumentPublisherPreflight interface {
	DocumentPublisher
	Preflight(context.Context, string) error
}

// DocumentRenderOptions are the caller-facing inputs for the canonical
// document renderer. The renderer owns presentation; the runner only supplies
// the canonical model and the requested language.
type DocumentRenderOptions struct {
	Title           string
	Language        Language
	DefaultLanguage Language
	// FullAudio is the already-certified master produced by the audio
	// pipeline. The renderer only projects this reference; it never probes,
	// trims, mixes, or uploads the file.
	FullAudio *DocumentAudioRef
	// FinalAudio is the full certification of the master asset (codec,
	// profile, sample rate, channels, hashes, mix/copy eligibility). The
	// renderer projects it verbatim so the video renderer consumes exactly
	// the same asset the document certifies. Nil when no master was built.
	FinalAudio *FinalAudioReference
	// JobPayload is the complete JSON request body retained for internal
	// provenance and downstream job operations. It is deliberately not
	// rendered into the human-facing Google Doc.
	JobPayload json.RawMessage
	// PayloadOnly suppresses internal machine JSON blocks in operator Docs.
	PayloadOnly bool
	// AudioTimeline is the canonical timeline used to compile FullAudio.
	AudioTimeline *audio.CanonicalTimeline
	// SceneSpeechTimings is the deterministic scene-level speech timing
	// projection (scene word boundaries + phrase spans in local and global
	// coordinates). The renderer projects it into the human surface and the
	// machine JSON section; it never derives or invents timestamps itself.
	SceneSpeechTimings []audio.SceneSpeechTiming
	// ClipMetadata is the canonical, pre-resolved clip-asset metadata
	// (total source duration in integer microseconds). The renderer formats
	// it verbatim; it never converts or derives clip durations.
	ClipMetadata []audio.ClipAssetMetadata
	// AudioSummary is the pre-computed aggregate of the audio facts (clip
	// totals, voiceover totals, counts) resolved at the capability boundary.
	// The renderer only formats it; it never sums durations across scenes.
	AudioSummary audio.DocumentAudioSummary
	// Overlay is the already-published reference to the completed render
	// overlay. It carries only the public artifact URL and copy-only
	// certification — never a local path — so the document and Velox copy
	// assembly reference the same immutable artifact. Nil when no render was
	// requested or the render has not produced an artifact.
	Overlay *DocumentOverlayRef
	// OverlayPlan is the sealed semantic plan sent to RenderingGen. The
	// document renders this exact plan so entity/phrase identity, timing and
	// content-addressed assets remain auditable beside the rendered artifact.
	OverlayPlan *capabilityoverlay.OverlayPlan
}

type DocumentAudioRef = scriptpkg.DocumentAudioRef

// DocumentOverlayRef is the published render-overlay reference projected into
// a document. See scriptpkg.DocumentOverlayRef for the field contract.
type DocumentOverlayRef = scriptpkg.DocumentOverlayRef

// DocumentRenderer is the single rendering seam used by every document
// producer. The composition root wires the canonical implementation, while
// the capability remains independent of HTML and Google Docs.
type DocumentRenderer interface {
	RenderDocument(*scriptpkg.ModelScriptOutputV1, DocumentRenderOptions) (string, error)
}

// SplittableDocumentRenderer is the optional early/late rendering seam. When
// a renderer implements it, the runner renders the scene-text-only skeleton
// at SceneTextReady (so the CPU half of DocsPrepare overlaps TTS and NLP) and
// then fills the late-bound markers after the audio join. Renderers that only
// implement DocumentRenderer keep the one-shot path; the two paths must be
// byte-equivalent for a non-nil model.
type SplittableDocumentRenderer interface {
	RenderDocumentSkeleton(DocumentSkeletonInput) string
	InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) string
}

// IdentifiedDocumentRenderer is an optional observability seam. Production
// renderers implement it so completed runs prove which formatter was used;
// test renderers may omit it and remain valid port fakes.
type IdentifiedDocumentRenderer interface {
	DocumentRendererID() string
}
