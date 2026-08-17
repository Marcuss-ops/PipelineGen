// Package job — artifact_manifest.go (Creator Blocco 2.1, July 2026).
//
// ArtifactManifest is the canonical serialisable representation of
// the artefacts produced by a worker job. It lives in domain/job
// because it is a wire-format contract between the Creator worker
// and the Sender broker — every job handler that produces files
// MUST embed a manifest under the __artifact_manifest key.
//
// Lifecycle:
//  1. Handler produces files in the job workspace.
//  2. Handler builds an ArtifactManifest (one Artifact per file).
//  3. Handler embeds the manifest in the result map
//     (key "__artifact_manifest", value serialised JSON).
//  4. The worker runner (internal/application/jobs/worker/runner.go)
//     calls Decode() to extract the manifest, Validate() to check
//     required-artefact invariants, then uploads each artefact.
//  5. After upload, WithRemoteLocations() replaces local paths with
//     remote references so the Sender never sees local filesystem paths.
package job

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SchemaVersionArtifactManifestV1 is the canonical schema version
// for the initial manifest format.
const SchemaVersionArtifactManifestV1 = "pipelinegen.artifacts.v1"

// ManifestKey is the map key under which the serialised manifest is
// stored in a handler's result map.
const ManifestKey = "__artifact_manifest"

// ── Known artefact kinds ─────────────────────────────────────────────
// These are the canonical kind strings used by the Creator postprocessors.
// A kind describes the semantic role of the artefact; it's used by the
// Sender to decide routing (e.g. voiceover → TTS review queue,
// script_json → Document builder).
const (
	ArtifactKindScriptJSON       = "script_json"
	ArtifactKindScriptText       = "script_text"
	ArtifactKindScenes           = "scenes"
	ArtifactKindMetadata         = "metadata"
	ArtifactKindEntities         = "entities"
	ArtifactKindVoiceover        = "voiceover"
	ArtifactKindFinalAudio       = "final_audio"
	ArtifactKindImage            = "image"
	ArtifactKindClipBindings     = "clip_bindings"
	ArtifactKindArtifactManifest = "artifact_manifest"
	// ArtifactKindPDF = "pdf" (P0 Commit 10, July 2026) extends the
	// canonical kind set for the document.generate handler. The kind
	// string is the contract that the Sender-side upload cycle
	// (internal/application/jobs/worker/runner.go::uploadManifest)
	// switches on; adding a new kind is a wire-format extension and
	// is documented here so the next reader can find the originating
	// handler at internal/api/assets/document/document_handler.go.
	ArtifactKindPDF = "pdf"
	// ArtifactKindMarkdown = "markdown" (P0 Commit 12, July 2026)
	// extends the canonical kind set for the script.generate
	// §8.4 multi-artifact envelope. Reserved as the document-markdown
	// OPTIONAL slot per the §8.4 spec — the markdown twin of the
	// generated document is gated on a future markdown emission
	// pipeline (out of scope for C12); the kind constant is added
	// NOW so the C12 handler emission code can name the slot at
	// compile time without a string literal, matching how
	// ArtifactKindPDF was introduced in C10.
	ArtifactKindMarkdown = "markdown"
	// ArtifactKindOverlay = "overlay" extends the canonical kind set
	// for the RenderingGen overlay handler
	// (internal/app/overlays/handlers.go). Overlays are the
	// declarative PipelineGen↔RenderingGen contract: PipelineGen
	// decides what should appear and when, RenderingGen materializes
	// that decision. The kind string is the wire-format contract that
	// the Sender-side routing (destinationForArtifactKind in
	// internal/application/jobs/worker_artifact_manifest.go) switches
	// on; the constant is added so the handler can name the slot at
	// compile time without a string literal, matching how
	// ArtifactKindPDF and ArtifactKindMarkdown were introduced.
	ArtifactKindOverlay = "overlay"
)

// ArtifactManifest is the top-level container for a job's output artefacts.
type ArtifactManifest struct {
	// SchemaVersion identifies the format (SchemaVersionArtifactManifestV1).
	SchemaVersion string `json:"schema_version"`

	// WorkflowID is the parent workflow correlation ID.
	WorkflowID string `json:"workflow_id"`

	// JobID is the job that produced these artefacts.
	JobID string `json:"job_id"`

	// Artifacts is the ordered list of produced artefacts.
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact describes a single file produced by a job handler.
type Artifact struct {
	// ID is a stable, unique identifier within the manifest
	// (convention: "<job_id>:<kind>" or "<job_id>:<kind>:<locale>").
	ID string `json:"id"`

	// Kind is the semantic role (see ArtifactKind* constants).
	Kind string `json:"kind"`

	// Path is the absolute (or workspace-relative) path to the file
	// on the worker's local filesystem. Set to empty after upload
	// (replaced by RemoteAssetID).
	Path string `json:"path"`

	// Filename is the leaf name for the artefact when transferred
	// (e.g. "script.json", "voiceover-it.mp3").
	Filename string `json:"filename"`

	// MIMEType is the IANA media type (e.g. "application/json",
	// "audio/mpeg", "image/png").
	MIMEType string `json:"mime_type"`

	// SizeBytes is the on-disk size in bytes. Populated by the handler
	// or the runner before upload.
	SizeBytes int64 `json:"size_bytes"`

	// SHA256 is the hex-encoded SHA-256 digest of the file contents.
	// Populated by the runner before upload.
	SHA256 string `json:"sha256"`

	// Required signals that the job MUST fail if this artefact is
	// missing or cannot be uploaded. Non-required (best-effort)
	// artefacts are silently dropped on upload failure.
	Required bool `json:"required"`

	// DriveGroup and DriveLanguage carry optional logical routing metadata
	// for the canonical delivery publisher. They are deliberately hints,
	// never folder IDs: the publisher/resolver remains the only owner of
	// Drive folder topology.
	DriveGroup    string `json:"drive_group,omitempty"`
	DriveLanguage string `json:"drive_language,omitempty"`

	// RemoteFileID, RemoteWebViewLink, and RemoteDownloadLink are
	// populated by producers that already completed publication. They
	// are additive manifest fields used by capability-specific result
	// projections; the worker-side upload contract continues to use
	// Path + Required and ignores these fields.
	RemoteFileID       string `json:"remote_file_id,omitempty"`
	RemoteWebViewLink  string `json:"remote_web_view_link,omitempty"`
	RemoteDownloadLink string `json:"remote_download_link,omitempty"`

	// DriveFileID and DriveLink are the canonical Drive-published
	// identifiers, populated AFTER the Drive publisher runs (they mirror
	// finalization.AssetLocation.FileID / WebViewLink). They are empty on
	// the worker-emitted manifest (RenderingGen cannot know the Drive file
	// ID before publication) and get enriched on the Sender side once the
	// canonical publisher resolves the file under drive_subpath. They are
	// additive manifest fields; the worker-side upload contract continues
	// to use Path + Required and ignores these fields.
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`

	ArtifactMetadata map[string]any `json:"artifact_metadata,omitempty"`
}

// Validate checks the manifest invariants. Returns nil if the manifest
// is well-formed, or a descriptive error covering the first violation.
//
// Invariants:
//   - SchemaVersion must be non-empty.
//   - At least one Artifact must be present.
//   - Every Required artefact must have a non-empty Path.
//   - Every artefact with a non-empty Path must have a non-empty Filename.
//
// FASE 1 close-out typed-error contract (July 2026): every error
// return wraps ONE OR MORE typed sentinels so callers can probe
// errors.Is without string-matching. The mapping is:
//
//   - schema_version empty / zero artefacts / id empty / kind empty /
//     path-set-but-filename-empty → ErrArtifactManifestInvalid
//     (the general "manifest shape rejected" sentinel).
//   - required + empty path → both ErrArtifactManifestInvalid AND
//     ErrRequiredArtifactMissing wrapped via the Go 1.20+ dual-%w
//     form so callers can errors.Is against either name. The
//     operator-readable substring "required but path is empty"
//     is preserved for back-compat with existing strings.Contains
//     assertions in canonical tests.
//
// godlike/06 SSOT: the canonical ErrArtifactManifestInvalid +
// ErrRequiredArtifactMissing pointers are owned by domain/job
// (this package) and domain/finalization respectively. The
// dual-wrap here lets producer-side + publisher-side call sites
// share a single errors.Is probe.
func (m *ArtifactManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: manifest is nil", ErrArtifactManifestInvalid)
	}
	if strings.TrimSpace(m.SchemaVersion) == "" {
		return fmt.Errorf("%w: schema_version is empty", ErrArtifactManifestInvalid)
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("%w: manifest has zero artifacts", ErrArtifactManifestInvalid)
	}

	for i, a := range m.Artifacts {
		if strings.TrimSpace(a.ID) == "" {
			return fmt.Errorf("%w: artifact[%d]: id is empty", ErrArtifactManifestInvalid, i)
		}
		if strings.TrimSpace(a.Kind) == "" {
			return fmt.Errorf("%w: artifact[%d] (%s): kind is empty", ErrArtifactManifestInvalid, i, a.ID)
		}
		if a.Required && strings.TrimSpace(a.Path) == "" {
			// Single-wrap by design (ErrRequiredArtifactMissing
			// only); the worker emits dual-%w (ErrArtifactManifestInvalid
			// + this inner err) — do NOT dual-wrap here or
			// downstream errors.Is chain traversal breaks.
			return fmt.Errorf("%w: artifact[%d] (%s): required but path is empty", ErrRequiredArtifactMissing, i, a.ID)
		}
		if strings.TrimSpace(a.Path) != "" && strings.TrimSpace(a.Filename) == "" {
			return fmt.Errorf("%w: artifact[%d] (%s): path set but filename is empty", ErrArtifactManifestInvalid, i, a.ID)
		}
	}

	return nil
}

// RequiredArtifacts returns the subset of artefacts marked Required.
func (m *ArtifactManifest) RequiredArtifacts() []Artifact {
	if m == nil {
		return nil
	}
	var out []Artifact
	for _, a := range m.Artifacts {
		if a.Required {
			out = append(out, a)
		}
	}
	return out
}

// Decode extracts and deserialises an ArtifactManifest from a handler
// result map. Looks for the key ManifestKey ("__artifact_manifest").
//
// Three forms are accepted:
//  1. The value is already a *ArtifactManifest — returned directly.
//  2. The value is a json.RawMessage / []byte / string — unmarshalled.
//  3. The value is a map[string]any — re-serialised to JSON then unmarshalled
//     (covers the case where the handler embedded a literal map).
//
// Returns nil, nil when the key is absent (no manifest → legacy path).
// Returns an error only when the key is present but malformed.
func Decode(result map[string]any) (*ArtifactManifest, error) {
	if result == nil {
		return nil, nil
	}

	raw, ok := result[ManifestKey]
	if !ok {
		return nil, nil
	}

	// Case 1: already the correct type.
	if m, ok := raw.(*ArtifactManifest); ok {
		return m, nil
	}

	// Case 2: JSON-encoded bytes or string.
	var jsonBytes []byte
	switch v := raw.(type) {
	case json.RawMessage:
		jsonBytes = v
	case []byte:
		jsonBytes = v
	case string:
		jsonBytes = []byte(v)
	case map[string]any:
		// Case 3: literal map — re-serialise.
		var err error
		jsonBytes, err = json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("artifact manifest: failed to marshal map: %w", err)
		}
	default:
		return nil, fmt.Errorf("artifact manifest: unexpected type %T under key %q", raw, ManifestKey)
	}

	var m ArtifactManifest
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		return nil, fmt.Errorf("artifact manifest: unmarshal: %w", err)
	}
	return &m, nil
}

// ── Upload-safe types ──────────────────────────────────────────────

// RemoteAsset is the result of uploading a single artefact to the Sender.
// The RemoteAssetID is the identifier the Sender uses to serve the file
// to downstream consumers.
type RemoteAsset struct {
	RemoteAssetID string `json:"remote_asset_id"`
	SHA256        string `json:"sha256"`
}

// RemoteAssetIDAdapter is the typed adapter the ToRemote canonical
// conversion accepts. The C5 spec keeps RemoteAsset as the SSOT
// (the existing `map[string]RemoteAsset` pattern in
// internal/application/jobs/worker/runner.go::uploadManifest) and
// surfaces it through RemoteAssetIDAdapter as a type alias — same
// identity, but the alias gives the Sender-side call sites a
// canonical vocabulary that documents the adapter contract ("any
// value satisfying RemoteAssetIDAdapter can be projected to a
// RemoteArtifact" rather than the lower-level "value struct").
//
// Future divergence (e.g. adding a ContentURL field) can split the
// alias into a defined type without breaking the C5 call signature.
type RemoteAssetIDAdapter = RemoteAsset

// UploadedManifest is the Sender-safe representation of a job's artefacts
// after all uploads have completed. It contains no local filesystem paths
// — every artefact references a RemoteAssetID that the Sender can resolve.
type UploadedManifest struct {
	SchemaVersion string             `json:"schema_version"`
	WorkflowID    string             `json:"workflow_id"`
	JobID         string             `json:"job_id"`
	Artifacts     []UploadedArtifact `json:"artifacts"`
}

// RemoteArtifactManifest is the C5 canonical name for the
// Sender-safe manifest (dual-type vocabulary per P0 §4 dual-type
// discipline). The type alias preserves identity with UploadedManifest
// so existing callers (legacy WithRemoteLocations tests, the runner)
// keep compiling unchanged while new callers exercise the canonical
// ToRemote contract. When the legacy alias path is removed in C9,
// this becomes a defined type.
type RemoteArtifactManifest = UploadedManifest

// ── Upload status constants ──────────────────────────────────────

const (
	StatusReady   = "ready"
	StatusSkipped = "skipped"
)

// ArtifactRequirement mirrors the finalizer-side typed enum without
// importing the higher-level package into the job manifest contract.
// The numeric values are intentionally aligned with
// internal/domain/finalization.ArtifactRequirement so the JSON payload
// unmarshals cleanly into the canonical finalizer request.
type ArtifactRequirement int

const (
	ArtifactRequirementInvalid ArtifactRequirement = iota
	ArtifactRequirementRequired
	ArtifactRequirementOptional
)

// UploadedArtifact is a single artefact in the Sender-safe manifest.
// Path and SizeBytes are intentionally omitted — the Sender resolves
// the file via RemoteAssetID.
type UploadedArtifact struct {
	ID            string              `json:"id"`
	Kind          string              `json:"kind"`
	Filename      string              `json:"filename"`
	MIMEType      string              `json:"mime_type"`
	SHA256        string              `json:"sha256"`
	Requirement   ArtifactRequirement `json:"requirement"`
	RemoteAssetID string              `json:"remote_asset_id"`
	Status        string              `json:"status"` // StatusReady | StatusSkipped
	// ArtifactMetadata is the contract projection needed by remote
	// consumers such as Velox. It survives local-path removal.
	ArtifactMetadata map[string]any `json:"artifact_metadata,omitempty"`
}

// RemoteArtifact is the C5 canonical name for the Sender-safe
// per-artefact envelope. Alias of UploadedArtifact (see
// RemoteArtifactManifest rationale).
type RemoteArtifact = UploadedArtifact

// ── Sentinel errors (P0 Commit 5) ──────────────────────────────────

// ErrRemoteSchemaVersionUnsupported is returned by ToRemote when the
// input ArtifactManifest.SchemaVersion is anything other than the
// canonical V1 schema. The Sender protocol is locked to V1 today;
// emitting any other version would silently regress the wire-format
// contract. Sentinels are exported so callers can probe via
// errors.Is(err, ErrRemoteSchemaVersionUnsupported).
var ErrRemoteSchemaVersionUnsupported = errors.New("artifact manifest: remote schema_version is not V1")

// LocalPath is the canonical accessor for an Artifact's on-disk
// filesystem path. Kept as an accessor method on the Artifact value
// (NOT a field rename) so the existing json:"path" JSON tag is
// preserved — no wire-format regression for handler-emitted
// manifests, and the Sender-safe types simply omit the LocalPath
// accessor (their fields are position-based RemoteAssetID + Status),
// enforcing the C5 invariant "Sender NEVER sees LocalPath" at the
// type level for the dual types.
func (a Artifact) LocalPath() string {
	return a.Path
}

// ToRemote is the canonical emit-side converter from local
// ArtifactManifest to Sender-safe RemoteArtifactManifest. The dual
// type vocabulary (Local ArtifactManifest + Remote RemoteArtifactManifest)
// is locked here: ToRemote strips every LocalPath reference (the
// remote type's field set has no LocalPath / Path field), validates
// the canonical V1 schema_version (rejects anything else before
// emit), and rejects any Required artefact missing from the
// `uploaded` map BEFORE producing the RemoteArtifactManifest
// (per the C5 spec: "Required missing rejected before emit").
//
// Validation order (matters for the error attribution test suite):
//  1. nil-receiver guard
//  2. SchemaVersion must equal SchemaVersionArtifactManifestV1
//  3. Required artefacts must all be present in `uploaded`
//
// Then the type-stripped RemoteArtifactManifest is built and returned.
//
// Non-required artefacts not in `uploaded` receive Status="skipped"
// (best-effort, matches the existing WithRemoteLocations semantics).
//
// FASE 1 close-out typed-error contract (July 2026): the
// required-missingFromUploaded error wraps BOTH typed sentinels
// (Go 1.20+ dual-%w form) so callers can probe either name:
//
//   - job.ErrRequiredArtifactMissing (the specific sentinel per
//     FASE 1 spec "manca un required artifact → bloccare
//     SUCCEEDED").
//   - job.ErrArtifactManifestInvalid (the general shape sentinel,
//     for callers that want a single catch-all probe).
//
// The operator-readable substrings ("required but was not
// uploaded", the artifact ID) are preserved for the existing
// strings.Contains assertions.
func (m *ArtifactManifest) ToRemote(uploaded map[string]RemoteAssetIDAdapter) (*RemoteArtifactManifest, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: manifest is nil", ErrArtifactManifestInvalid)
	}

	// (1) SchemaVersion is locked to V1 on the Sender side. Any
	// other value (including "" or "v2") is rejected BEFORE emit
	// so a V2 manifest cannot silently downgrade the wire-format
	// contract that downstream Sender workers depend on.
	if m.SchemaVersion != SchemaVersionArtifactManifestV1 {
		return nil, fmt.Errorf("%w: got %q, want %q",
			ErrRemoteSchemaVersionUnsupported, m.SchemaVersion, SchemaVersionArtifactManifestV1)
	}

	// (2) Required artefacts: pre-emit check. A missing required
	// entry is a hard failure — the Sender cannot pretend the
	// artefact is "skipped" for a required slot. FASE 1 close-out
	// dual-sentinel wrap.
	for _, a := range m.RequiredArtifacts() {
		if _, ok := uploaded[a.ID]; !ok {
			return nil, fmt.Errorf("%w: artifact %q (%s) is required but was not uploaded",
				ErrRequiredArtifactMissing, a.ID, a.Kind)
		}
	}

	// (3) Build the RemoteArtifactManifest. Note: this type has
	// NO LocalPath / Path field — the omission is the structural
	// enforcement of the C5 invariant "Sender NEVER sees LocalPath".
	result := &RemoteArtifactManifest{
		SchemaVersion: m.SchemaVersion,
		WorkflowID:    m.WorkflowID,
		JobID:         m.JobID,
		Artifacts:     make([]RemoteArtifact, 0, len(m.Artifacts)),
	}

	for _, a := range m.Artifacts {
		remote, ok := uploaded[a.ID]

		ra := RemoteArtifact{
			ID:               a.ID,
			Kind:             a.Kind,
			Filename:         a.Filename,
			MIMEType:         a.MIMEType,
			SHA256:           a.SHA256,
			Requirement:      ArtifactRequirementOptional,
			ArtifactMetadata: a.ArtifactMetadata,
		}
		if a.Required {
			ra.Requirement = ArtifactRequirementRequired
		}

		if ok {
			ra.RemoteAssetID = remote.RemoteAssetID
			ra.Status = StatusReady
		} else {
			ra.Status = StatusSkipped
		}

		result.Artifacts = append(result.Artifacts, ra)
	}

	return result, nil
}

// WithRemoteLocations is the legacy back-compat alias for ToRemote.
// The canonical C5 entry point is ToRemote (semantically equivalent
// except for the explicit V1 SchemaVersion gate which was added in
// C5); WithRemoteLocations delegates to ToRemote so existing callers
// (e.g. legacy unit tests, the canonical pre-C5 uploadManifest path)
// continue to compile. New code MUST use ToRemote.
func (m *ArtifactManifest) WithRemoteLocations(uploaded map[string]RemoteAsset) (*UploadedManifest, error) {
	return m.ToRemote(uploaded)
}

// FileReader is the minimal read-side filesystem port used by
// ComputeSHA256. The adapter (internal/platform/filesystem.OS) injects
// os.Open; the kernel contract never imports os directly.
type FileReader interface {
	Open(path string) (io.ReadCloser, error)
}

// ComputeSHA256 reads the file at path (via the injected FileReader) and
// returns its hex-encoded SHA-256 digest. Used by the runner before upload
// to populate Artifact.SHA256.
func ComputeSHA256(f FileReader, path string) (string, error) {
	file, err := f.Open(path)
	if err != nil {
		return "", fmt.Errorf("sha256: open %s: %w", path, err)
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fmt.Errorf("sha256: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
