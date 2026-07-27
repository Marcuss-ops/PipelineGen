// Package mediamemory — types_linker.go is the canonical home
// for the Fase 3.2 linker wire envelopes: LinkerRequest (per-
// candidate input bundle), LinkerResult (per-candidate output
// bundle with persisted binding IDs + indexed concept IDs +
// detected entities + status + failures + idempotency-skip
// flag), EncodingChannels (multichannel encoder input), and the
// canonical model-output envelopes MediaEmbedding /
// TranscriptSegment / Keyframe for the transcriber + keyframe +
// embedding pipeline.
//
// godlike/06 SSOT (narrow port doctrine): LinkerRequest carries
// ONLY the candidate and a ProjectID; provider + media type are
// derived from the candidate itself so the worker cannot branch
// on caller-supplied ownership data that could drift from the
// canonical MediaCandidate row.
//
// godlike/06 SSOT (vector SSOT): MediaEmbedding.Vector is dense
// float32 in the model's native dimensionality. Empty / nil
// vectors NEVER silently satisfy a Qdrant payload write — the
// canonical embedding call site checks len(Vector) > 0 BEFORE
// stamping the payload and surfaces ErrLinkerEmbeddingFailed
// otherwise.
//
// godlike/06 SSOT (linker sentinels companion): the linker
// ErrLinker* sentinels live in types_sentinels.go — this file
// holds the SSOT envelopes, the sentinels file holds the typed
// fail-closed envelopes they raise.
//
// File split ownership (godlike/06 SSOT):
//   - types.go               : package doc + SlotKind alias
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe  ← this file
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)
package mediamemory

// ── Fase 3.2 linker wire envelopes (godlike/06 SSOT) ─────────────

// LinkerRequest is the per-candidate input bundle consumed by
// LinkerWorker.EnrichCandidate. godlike/06 SSOT (narrow port
// doctrine): the envelope carries ONLY the candidate and a
// ProjectID for rights-trail context. Provider name + media
// type are derived from the candidate itself so the worker
// cannot branch on caller-supplied ownership data that could
// drift from the canonical MediaCandidate row.
type LinkerRequest struct {
	Candidate MediaCandidate
	ProjectID string
	Language  string
}

// LinkerResult is the per-candidate output of EnrichCandidate.
// godlike/06 SSOT: PersistedBindingIDs + IndexedConceptIDs +
// DiscoveryStatus are the canonical durable footprint of one
// EnrichCandidate call; Failures is the canonical per-step
// failure record for the dashboard. The orchestrator (batch_
// service.EnrichLinker) accumulates Failures into the parent
// Failure channel without re-formatting them.
type LinkerResult struct {
	PersistedBindingIDs []string
	IndexedConceptIDs   []string
	DetectedEntities    []string // canonical free-text labels, NOT concept IDs
	Status              DiscoveryStatus
	Failures            []string
	Empty               bool // true when the linker short-circuited via the idempotency no-op path
}

// EncodingChannels is the canonical multichannel input bundle
// for EmbeddingEncoder.Encode. godlike/06 SSOT (channel
// SSOT): text + transcript + visual_desc + audio + BM25
// sparse are the canonical channels. Empty strings are NOT a
// silent zero-output (godlike/07) — receivers can choose to
// reject an Encode call whose Text AND Transcript AND
// VisualDesc are all empty, but the canonical default is to
// return a zero-vector so the canonical embedding call site
// surfaces ErrLinkerEmbeddingFailed on its own predicate.
type EncodingChannels struct {
	Text       string
	Transcript string
	VisualDesc string
	Audio      string
}

// MediaEmbedding is the canonical model output of
// EmbeddingEncoder.Encode. godlike/06 SSOT (vector SSOT):
// Vector is dense float32 in the model's native dimensionality.
type MediaEmbedding struct {
	Vector []float32
	Dim    int
	Model  string // encoder identifier (used by EmbeddingIndexer for Qdrant payload stamping)
}

// TranscriptSegment is one window of the transcriber output.
// godlike/06 SSOT: StartMs / EndMs / Text is the canonical
// 3-tuple. Phase 3.5 stockpipeline-level transcriber is the
// production adapter.
type TranscriptSegment struct {
	StartMs int64
	EndMs   int64
	Text    string
}

// Keyframe is one still-frame the linker indexes for visual
// description. godlike/06 SSOT: the canonical wire shape
// carries timestamp + raw URL/blob + an optional pre-computed
// embedding (forward-pointer to Fase 4.1 visual channel).
// For Fase 3.2 the URL is the canonical envelope; ImageData
// is left optional.
type Keyframe struct {
	Ms        int64
	ImageURL  string
	ImageData []byte    // optional pre-fetched bytes for offline encoders
	Embedding []float32 // optional, set by Fase 4.1 visual-channel encoder
}
