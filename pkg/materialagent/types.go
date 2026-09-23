// Package materialagent is the autonomous material layer that runs on a REMOTE
// computer: it turns a semantic MaterialRequest ("Tesla factory b-roll, 3
// clips, 5-12s") into real, verified material by orchestrating the Master's
// runtime endpoints through pkg/veloxclient.
//
// Division of responsibility (deliberate):
//   - the Master is the SSOT: catalog, locations, job registry, receipts. It
//     answers "what exists and where", never "what should the video use".
//   - this package DECIDES. It searches the catalog first, falls back to live
//     discovery / stock acquisition only when the catalog cannot satisfy the
//     request, scores candidates, materializes the winners, and registers what
//     it produced.
//
// Every endpoint is reached through veloxclient, so no path, header or retry
// policy is re-implemented here. Adding a source = implementing Resolver and
// registering it; nothing else changes.
package materialagent

import (
	"context"
	"encoding/json"
	"errors"
)

// MaterialRequest is the semantic request one scene/entity makes. It carries
// intent, not endpoints: which sources to try and in what order is policy
// ({@link Policy}), not caller boilerplate.
type MaterialRequest struct {
	// RequestID correlates the resolution across logs/retries (e.g.
	// "mat_scene_17"). Optional but recommended for idempotency keys.
	RequestID string
	// ProjectID groups the material of one video (quota/cleanup scope).
	ProjectID string
	// SceneID is the scene/beat this material belongs to.
	SceneID string
	// Description is the natural-language need ("Elon Musk walking through a
	// Tesla factory"). It becomes the catalog query AND the discovery query.
	Description string
	// MaterialType is the desired media family ("video", "image", "audio").
	MaterialType string
	// SemanticRole is the usage intent ("supporting", "primary", "broll").
	SemanticRole string
	// Count is how many usable items the scene needs (default 1).
	Count int
	// Constraints bound the acceptable material.
	Constraints Constraints
	// Sources overrides the policy order for this request (empty = policy).
	Sources []string
	// Destination places materialized clips in the Master's Drive tree.
	Destination *Destination
	// Segments, when set, force EXPLICIT clip extraction (start/end) instead of
	// letting the Master derive the important parts from the transcript. Leave
	// nil to use selection.mode=important.
	Segments []ClipSegment
}

// Constraints bound a candidate's acceptability.
type Constraints struct {
	DurationMinSeconds float64
	DurationMaxSeconds float64
	AspectRatio        string
	MinQuality         string
	// MinWidth is the pixel-width floor used by the scorer (0 = unset).
	MinWidth int
	// RequireCaptions makes the agent drop shortlist candidates that are known
	// (or probed and found) to expose no caption track. It is the gate an
	// interview/transcript harvest sets. Material whose captions cannot be
	// established (probe error, or the probe budget is spent) fails closed
	// too: a transcript-dependent request cannot be served by material we
	// cannot verify.
	RequireCaptions bool
}

// Destination is the Drive placement for materialized clips. It maps onto the
// canonical /api/clips/process `destination` object (top-level group/folder
// fields are rejected by the server).
type Destination struct {
	Group           string
	FolderID        string
	FolderPath      string
	SubfolderName   string
	CreateSubfolder bool
}

// ClipSegment is one explicit extraction window. Start/End are "HH:MM:SS" or
// "MM:SS" strings, matching the canonical ExtractRequest.Segment.
type ClipSegment struct {
	Start string
	End   string
	Name  string
}

// Candidate is one piece of material a resolver found. It is a projection, not
// a domain entity: fields the resolver cannot answer stay empty rather than
// guessed.
type Candidate struct {
	// Resolver is the resolver that produced the candidate.
	Resolver string
	// AssetID is the Master's catalog id, when the candidate is already
	// registered (catalog hits). Empty for not-yet-materialized discovery hits.
	AssetID string
	// Source is the physical provenance ("youtube", "stock", "local", ...).
	Source string
	// SourceRef is the provider-native reference (YouTube video id, ...).
	SourceRef string
	MediaType string
	Title     string
	Name      string
	// SourceURL is a provider page URL (never a temporary download URL).
	SourceURL string
	// DriveFileID / ContentHash identify the durable bytes, when known.
	DriveFileID string
	ContentHash string

	DurationSeconds float64
	Width           int
	Height          int

	HasDrive bool
	HasLocal bool

	// CaptionsKnown reports whether HasCaptions is authoritative. It is false
	// when the source cannot answer (catalog rows, stock) or when the server
	// predates the caption probe; a caller must NOT read unknown as "none".
	CaptionsKnown bool
	// HasCaptions is meaningful only when CaptionsKnown: whether the media
	// exposes at least one caption/subtitle track.
	HasCaptions bool

	// Relevance is the resolver's normalised [0,1] relevance (catalog score or
	// topic-similarity). The scorer weighs it; it is not the final score.
	Relevance float64
	// Raw preserves the resolver's own payload for auditing/extension.
	Raw json.RawMessage
}

// Material is a materialized (or already-durable) item the caller can use.
type Material struct {
	Candidate
	// JobID is the job that produced the material (empty when the candidate was
	// already registered and needed no work).
	JobID string
	// Registered reports whether the material is present in the Master's
	// catalog (a catalog hit, or a job that completed with a registered asset).
	Registered bool
}

// Resolver is one material source. Search is read-only discovery; Materialize
// performs the acquisition (extract/stock/…) and returns the usable material.
// Implementations MUST be safe for concurrent use.
type Resolver interface {
	// Name is the resolver's stable key (policy order uses it).
	Name() string
	// Search returns candidates for the request, best-effort. An empty slice is
	// a legitimate "no material here" answer.
	Search(ctx context.Context, req MaterialRequest) ([]Candidate, error)
	// Materialize acquires c. It returns the usable Material or an error.
	Materialize(ctx context.Context, c Candidate, req MaterialRequest) (*Material, error)
}

// ErrNoMaterial is returned (joined with per-resolver errors) when no resolver
// could satisfy the request. It is a NORMAL outcome for a scene whose material
// simply does not exist yet — callers should treat it as "needs operator
// attention", not as a bug.
var ErrNoMaterial = errors.New("materialagent: no resolver produced material for the request")

// ErrNoCaptions classifies the fail-closed caption gate: a candidate was
// rejected because it exposes no caption track (or its captions could not be
// established under RequireCaptions). It is joined into the ErrNoMaterial
// chain so a caller can distinguish "no material" from "material that cannot
// be transcribed".
var ErrNoCaptions = errors.New("materialagent: candidate has no usable captions")
