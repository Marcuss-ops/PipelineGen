// Package asset — evidence.go defines the canonical EvidenceDocument
// contract and the SINGLE evidence-resolution precedence consumed by
// script grounding, SemanticDocumentComposer and search/indexing.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// evidence precedence. The order is:
//
//  1. transcript
//  2. semantic_summary
//  3. visual_summary
//  4. summary
//  5. description
//     none → NOT_GROUNDABLE (fail closed)
//
// No other layer may re-implement this order (Qdrant, script, search
// each had their own drift-prone copy before this convergence). The
// resolver is a pure function over an EvidenceInput candidate bag so
// every producer (script reads *Asset metadata, indexing reads
// IndexedMetadata) constructs the same input shape and gets the same
// deterministic answer.
//
// godlike/07 fail-closed: an asset with no resolvable evidence yields
// an EvidenceDocument with empty Text and empty SourceType — callers
// use Groundable() (or errors.Is(err, ErrEvidenceNotGroundable)) to
// surface the not-groundable state instead of silently substituting a
// placeholder.
package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// EvidenceTextSource names the canonical source tier that produced the
// resolved evidence text. The closed enumeration is pinned by
// TestCanonicalEvidenceTextSourceValues.
type EvidenceTextSource string

const (
	// EvidenceTranscript — the resolved (TextTrack) transcript.
	EvidenceTranscript EvidenceTextSource = "transcript"
	// EvidenceSemanticSummary — metadata_json["semantic_summary"].
	EvidenceSemanticSummary EvidenceTextSource = "semantic_summary"
	// EvidenceVisualSummary — metadata_json["visual_summary"] (or the
	// VLM visual-summary caption).
	EvidenceVisualSummary EvidenceTextSource = "visual_summary"
	// EvidenceSummary — metadata_json["summary"] / clip_summary.
	EvidenceSummary EvidenceTextSource = "summary"
	// EvidenceDescription — metadata_json["description"].
	EvidenceDescription EvidenceTextSource = "description"
)

// CanonicalEvidenceTextSourceValues returns the closed enumeration of
// EvidenceTextSource values in precedence order (transcript first).
func CanonicalEvidenceTextSourceValues() []EvidenceTextSource {
	return []EvidenceTextSource{
		EvidenceTranscript,
		EvidenceSemanticSummary,
		EvidenceVisualSummary,
		EvidenceSummary,
		EvidenceDescription,
	}
}

// ErrEvidenceNotGroundable is the fail-closed sentinel for assets with
// no resolvable evidence text across any canonical tier.
var ErrEvidenceNotGroundable = errors.New("asset: evidence not groundable")

// EvidenceDocument is the single resolved evidence surface consumed by
// script grounding, SemanticDocumentComposer and search/indexing.
//
// Text is the winning evidence text (empty when not groundable);
// SourceType records which tier won; SourceHash is the SHA-256
// fingerprint of Text so provenance survives independently of the
// producing pipeline; Language is the resolved language when known.
type EvidenceDocument struct {
	AssetID    string             `json:"asset_id,omitempty"`
	Text       string             `json:"text,omitempty"`
	SourceType EvidenceTextSource `json:"source_type,omitempty"`
	Language   string             `json:"language,omitempty"`
	SourceHash string             `json:"source_hash,omitempty"`
}

// Groundable reports whether the document carries a resolved evidence
// text (non-empty SourceType and non-empty Text). It is the canonical
// NOT_GROUNDABLE check.
func (d EvidenceDocument) Groundable() bool {
	return d.SourceType != "" && strings.TrimSpace(d.Text) != ""
}

// EvidenceInput is the transport-agnostic candidate bag ResolveEvidence
// reads. Script resolution builds it from *Asset metadata (plus the
// already-resolved TextTrack transcript); indexing builds it from
// IndexedMetadata. Any empty field is simply skipped by the resolver.
type EvidenceInput struct {
	AssetID         string
	Transcript      string
	SemanticSummary string
	VisualSummary   string
	Summary         string
	Description     string
	Language        string
}

// ResolveEvidence applies the canonical single evidence precedence. The
// first non-empty (after TrimSpace) tier wins; an empty bag yields a
// NOT_GROUNDABLE EvidenceDocument with empty Text and SourceType.
//
// Determinism: the same EvidenceInput always yields the same
// EvidenceDocument (Text, SourceType and SourceHash are pure functions
// of the input). SourceHash is SHA-256 over the winning text.
func ResolveEvidence(in EvidenceInput) EvidenceDocument {
	notGroundable := EvidenceDocument{
		AssetID:  in.AssetID,
		Language: strings.TrimSpace(in.Language),
	}

	var (
		source EvidenceTextSource
		text   string
	)
	switch {
	case strings.TrimSpace(in.Transcript) != "":
		source, text = EvidenceTranscript, strings.TrimSpace(in.Transcript)
	case strings.TrimSpace(in.SemanticSummary) != "":
		source, text = EvidenceSemanticSummary, strings.TrimSpace(in.SemanticSummary)
	case strings.TrimSpace(in.VisualSummary) != "":
		source, text = EvidenceVisualSummary, strings.TrimSpace(in.VisualSummary)
	case strings.TrimSpace(in.Summary) != "":
		source, text = EvidenceSummary, strings.TrimSpace(in.Summary)
	case strings.TrimSpace(in.Description) != "":
		source, text = EvidenceDescription, strings.TrimSpace(in.Description)
	default:
		return notGroundable
	}

	return EvidenceDocument{
		AssetID:    in.AssetID,
		Text:       text,
		SourceType: source,
		Language:   strings.TrimSpace(in.Language),
		SourceHash: hashEvidenceText(text),
	}
}

// hashEvidenceText returns the SHA-256 hex fingerprint of the exact
// evidence text (the SourceHash provenance value).
func hashEvidenceText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
