// Package job — execution_result.go (P0 Commit 10, July 2026).
//
// ExecutionResult is the canonical generic envelope returned by
// handlers whose work product is a typed Result PLUS a Sender-side
// ArtifactManifest sidecar. It is the explicit on-the-wire shape
// that ties the dual-type vocabulary together:
//
//	Data        — the caller-facing typed result (T). Marshalled
//	              according to T's own JSON tags.
//	Artifacts   — the Sender-safe sidecar. The runner reads this
//	              via job.Decode() and routes each Artifact through
//	              the upload cycle (delivery.Publisher.Publish).
//
// Why this envelope exists (the C10 motivation): pre-C10 handlers
// returned a `map[string]any` (the legacy job boundary shape) with
// the manifest injected under job.ManifestKey. The contract was
// implicit: handlers had to remember to put the manifest under that
// key, callers had to remember to both read `data` AND look for the
// manifest. C10 makes the envelope type explicit; the manifest is
// a first-class field (not a magic key), so a future contributor
// cannot accidentally drop it or rename it.
//
// Why generic (T any): each handler returns a DIFFERENT typed
// Result (DocumentResult for documents, a VoiceoverResult for
// voiceover jobs, etc.). Adopting a single concrete *Result struct
// (e.g. job.Result) would either lose per-handler typing or force
// every handler to dump its result into one fat struct. Go generics
// let us keep the envelope shape canonical while letting each
// handler keep its own typed payload.
//
// Wire-format note: EncodingJSON uses the *T constraints encoded
// directly (T's own tags drive marshalling), so adding fields to T
// is a purely additive wire change for that handler's consumers.
// The envelope itself is locked: Data + Artifacts, both `json:"-"`
// ONLY if explicitly omitted by handler (rare).
//
// Sender-side invariant: when Data is non-nil and Artifacts is
// non-nil, the runner reads Artifacts (via job.Decode-style
// lookup, but here it is already strongly typed) to drive the
// upload cycle. The dual-type discipline (Local Manifest with
// LocalPath, Remote Manifest with RemoteAssetID + no LocalPath)
// still applies at the upload boundary per the C5 spec locked at
// artifact_manifest.go::ToRemote.
package job

// ExecutionResult is the canonical dual-shape envelope for handler
// results that carry both a typed Result payload and an
// ArtifactManifest sidecar for the Sender-side upload cycle.
//
// Type parameter T is the caller-facing typed result. Common
// examples already wired through this envelope:
//
//   - DocumentResult (the document application types)
//     — the canonical first adopter of the typed Result contract.
//
// Future adopters (voiceover, lessons, books, etc.) MUST migrate
// their result maps to ExecutionResult[T] when their handler is
// touched next; do NOT add new ad-hoc map[string]any result shapes.
type ExecutionResult[T any] struct {
	// Data is the typed handler result. Marshalled according to T's
	// own JSON tags. omitempty is NOT applied — a zero T still
	// marshals to `{}` so the wire shape is stable.
	Data T `json:"data"`

	// Artifacts is the Sender-side sidecar. The runner reads this
	// to drive the upload cycle (each Artifact → Publisher.Publish
	// with Filename/MIMEType/SizeBytes from the manifest). nil-safe
	// in the runner today (Decode-style lookup also exists); set
	// the field explicitly when the handler produces files.
	//
	// Pointer (rather than value) so the JSON encoding can omit the
	// field entirely for handlers that have NO sidecar (legacy
	// result-map handlers, async acks, etc.).
	Artifacts *ArtifactManifest `json:"artifacts,omitempty"`
}
