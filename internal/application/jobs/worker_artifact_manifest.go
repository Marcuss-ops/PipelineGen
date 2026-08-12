// Package jobs — worker_artifact_manifest.go (worker_execution_result.go
// sub-section split, July 2026).
//
// Owns the artifact-manifest handling: extractStagedArtifacts (handler
// result → broker StagedArtifacts JSON conversion) and its
// destinationForArtifactKind helper.
//
// The FASE 1 ordering pin (decode → nil-check → empty-envelope →
// validate → process) is regression-locked by 4 tests:
//
//	TestExtractStagedArtifacts_EmptyArtifactsList,
//	TestExtractStagedArtifacts_DecodeFailure_TypedSentinel,
//	TestExtractStagedArtifacts_ValidateFailure_TypedSentinel,
//	TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing.
//
// Any reorder silently lets a malformed manifest reach SUCCEEDED — DO
// NOT touch the if-cascade byte-for-byte.
//
// Mechanical split from worker_execution_result.go. Zero behavior
// change.
package jobs

import (
	"encoding/json"
	"fmt"
	"strings"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// extractStagedArtifacts reads the __artifact_manifest from the handler
// result map and converts it to the JSON wire format consumed by
// CompleteWithArtifactsCommand.StagedArtifacts.
//
// FASE 1 (close-out, July 2026) — typed-error contract (audit 2026-07-03
// P0 #4 closure + FASE 1 spec closure):
//
// The function returns (json.RawMessage, error) so every manifest-shape
// failure surfaces through a typed sentinel — NO silent `[]` fallback.
// The caller (finalizeJob) fails the job on any typed error so an
// artifact-producing job whose manifest cannot be honoured can NEVER
// reach SUCCEEDED. Three typed-sentinel modes (godlike/06 SSOT):
//
//  1. job.ErrArtifactManifestMissing — manifest key absent
//     (no __artifact_manifest in handler result). Per FASE 1 spec
//     "il manifest è assente, ... bloccare ... la transizione a
//     SUCCEEDED dei job type ProducesArtifacts=true".
//
//  2. job.ErrArtifactManifestInvalid — manifest cannot be decoded
//     (JSON error, unexpected type) OR fails shape validation
//     (empty schema_version, zero artifacts, empty id/kind,
//     path-set-but-filename-empty, etc). The wrapped error chain
//     carries the sub-mode in the message.
//
//  3. finalization.ErrRequiredArtifactMissing — required artifact has
//     empty path (via the Validate trivial-bit chain wrapping). Callers
//     probe errors.Is against either typed sentinel.
//
// Empty-but-valid manifests (manifest.Artifacts == 0 against a nil-error
// Validate) still return json.RawMessage("[]") — a handler that
// legitimately produces zero files with a well-formed empty envelope
// is allowed per the audit's existing audit-trail path; the empty
// envelope is a VALID artifact manifest per schema, not a missing one.
//
// The mapping from job.Artifact → finalization.PublishedArtifact:
//
//	id          → artifact_id
//	kind        → kind
//	filename    → filename
//	mime_type   → mime_type
//	size_bytes  → size_bytes
//	sha256      → sha256
//	local_path  → (discarded — the Sender never sees local paths)
//	required    → requirement (bool → ArtifactRequirement enum)
//	source      → source (PR-SOURCE-FIX: derived from jobType prefix)
//
// ── CANONICAL ORDERING PIN (FASE 1 close-out, byte-for-byte) ───────────
//
// The if-cascade below MUST stay in this exact order. A future
// contributor reflexively reordering the branches can silently break
// the FASE 1 spec invariants:
//
//  1. Decode   — typed ErrArtifactManifestInvalid on JSON parse /
//     unexpected-type failure (dual-%w wrap chains the
//     inner json error alongside the typed sentinel).
//  2. nil-check — typed ErrArtifactManifestMissing on absent
//     __artifact_manifest key (manifest present but
//     empty is NOT "missing" — empty-but-valid is a
//     distinct path; see step 3).
//  3. empty-envelope — return json.RawMessage("[]") on
//     len(Artifacts) == 0 with a non-nil manifest.
//     MUST precede step 4 because ArtifactManifest.Validate()
//     rejects `len(Artifacts) == 0` with
//     ErrArtifactManifestInvalid. If step 4 ran first,
//     the legitimate "handler legitimately produced
//     zero files" path would incorrectly fail.
//  4. Validate — typed ErrArtifactManifestInvalid on shape
//     violations (empty schema_version, empty id/kind,
//     required-but-empty-path, etc). The dual-%w
//     form wraps BOTH the typed sentinel AND the inner
//     Validate error, so errors.Is can probe either
//     job.ErrArtifactManifestInvalid (general) OR
//     job.ErrRequiredArtifactMissing (specific) via
//     chain traversal.
//  5. process   — publish the typed PublishedArtifact slice via
//     json.Marshal; the Marshal failure path wraps
//     ErrArtifactManifestInvalid (dual-%w form).
//
// Regression lock: the inner ordering is enforced by
// TestExtractStagedArtifacts_EmptyArtifactsList (which exercises
// the legitimate empty-envelope path returning `[]`) +
// TestExtractStagedArtifacts_DecodeFailure_TypedSentinel +
// TestExtractStagedArtifacts_ValidateFailure_TypedSentinel +
// TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing
// — all four MUST keep passing or the FASE 1 contract is broken.
func extractStagedArtifacts(result map[string]any, jobType string) (json.RawMessage, error) {
	manifest, err := job.Decode(result)
	if err != nil {
		// Malformed manifest — typed error per FASE 1. Caller fails
		// the job; the broker MUST NOT mark SUCCEEDED for a job
		// whose artifact manifest cannot be decoded. Dual-%w form
		// (Go 1.20+) wraps BOTH the typed sentinel and the inner
		// json/wire error so errors.Is probes for ErrArtifactManifestInvalid
		// OR any sub-mode sentinel ErrRequiredArtifactMissing
		// propagate through the chain.
		return nil, fmt.Errorf("artifact-producing job %q: decode failure: %w: %w", jobType, job.ErrArtifactManifestInvalid, err)
	}
	if manifest == nil {
		// Manifest key absent per FASE 1 spec "manifest è assente".
		// Typed sentinel — caller fails the job; the broker MUST
		// NOT mark SUCCEEDED. This is the close-out fix for the
		// pre-FASE-1 silent-drop `[]` anti-pattern.
		return nil, fmt.Errorf("artifact-producing job %q: %w", jobType, job.ErrArtifactManifestMissing)
	}
	if len(manifest.Artifacts) == 0 {
		// Valid empty envelope (handler legitimately produced zero
		// files). Audit-trail "empty-envelope" path per P0 #4.
		// MUST run BEFORE manifest.Validate() because Validate
		// rejects `len(Artifacts) == 0` with ErrArtifactManifestInvalid
		// — running Validate first would incorrectly fail the
		// legitimate empty-envelope path.
		return json.RawMessage("[]"), nil
	}
	if err := manifest.Validate(); err != nil {
		// Manifest IS present and non-empty but fails shape
		// validation (empty schema_version / empty id / empty
		// kind / required-but-empty-path / etc). Typed sentinel —
		// dual-%w form wraps BOTH the typed sentinel AND the
		// inner Validate error so callers can errors.Is against
		// ErrArtifactManifestInvalid (general) OR
		// ErrRequiredArtifactMissing (specific) by traversing
		// the wrap chain.
		return nil, fmt.Errorf("artifact-producing job %q: validate failure: %w: %w", jobType, job.ErrArtifactManifestInvalid, err)
	}

	staged := make(domainremote.StagedArtifacts, 0, len(manifest.Artifacts))
	// PR-SOURCE-FIX: derive source from job type prefix
	// ("script.generate" → "script", "image.generate.google" → "image", etc.).
	// Hoisted outside the loop — all artifacts in one manifest share the
	// same job type.
	src := ""
	if idx := strings.Index(jobType, "."); idx > 0 {
		src = jobType[:idx]
	}
	for _, a := range manifest.Artifacts {
		staged = append(staged, &domainremote.StagedArtifactReference{
			ArtifactID:    a.ID,
			Destination:   destinationForArtifactKind(a.Kind, src),
			SHA256:        a.SHA256,
			Path:          a.Path,
			Filename:      a.Filename,
			MIMEType:      a.MIMEType,
			SizeBytes:     a.SizeBytes,
			Required:      a.Required,
			DriveGroup:    a.DriveGroup,
			DriveLanguage: a.DriveLanguage,
		})
	}

	raw, marshalErr := json.Marshal(staged)
	if marshalErr != nil {
		// Marshal failure on the PublishedArtifact conversion shape —
		// typed error per FASE 1 (c). Caller fails the job. Dual-%w
		// form preserves both the typed sentinel and the underlying
		// json.Marshal error for caller-side errors.Is probing.
		return nil, fmt.Errorf("artifact-producing job %q: PublishedArtifact marshal: %w: %w", jobType, job.ErrArtifactManifestInvalid, marshalErr)
	}
	return json.RawMessage(raw), nil
}

func destinationForArtifactKind(kind, source string) string {
	switch kind {
	case job.ArtifactKindScriptJSON, job.ArtifactKindScriptText, job.ArtifactKindScenes,
		job.ArtifactKindMetadata, job.ArtifactKindEntities, job.ArtifactKindClipBindings:
		return "script"
	case job.ArtifactKindVoiceover:
		return "voiceover"
	case job.ArtifactKindImage:
		return "image"
	case job.ArtifactKindPDF, job.ArtifactKindMarkdown:
		return "document"
	default:
		if source == "youtube" {
			return "youtube_clip"
		}
		return "document"
	}
}
