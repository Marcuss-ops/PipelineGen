// Package job — artifact_manifest.go (Creator Blocco 2.1, July 2026).
//
// ArtifactManifest is the canonical serialisable representation of
// the artefacts produced by a worker job. It lives in domain/job
// because it is a wire-format contract between the Creator worker
// and the Sender broker — every job handler that produces files
// MUST embed a manifest under the __artifact_manifest key.
//
// Lifecycle:
//   1. Handler produces files in the job workspace.
//   2. Handler builds an ArtifactManifest (one Artifact per file).
//   3. Handler embeds the manifest in the result map
//      (key "__artifact_manifest", value serialised JSON).
//   4. The worker runner (internal/application/jobs/worker/runner.go)
//      calls Decode() to extract the manifest, Validate() to check
//      required-artefact invariants, then uploads each artefact.
//   5. After upload, WithRemoteLocations() replaces local paths with
//      remote references so the Sender never sees local filesystem paths.
package job

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	ArtifactKindScriptJSON      = "script_json"
	ArtifactKindScriptText      = "script_text"
	ArtifactKindScenes          = "scenes"
	ArtifactKindMetadata        = "metadata"
	ArtifactKindEntities        = "entities"
	ArtifactKindVoiceover       = "voiceover"
	ArtifactKindImage           = "image"
	ArtifactKindClipBindings    = "clip_bindings"
	ArtifactKindArtifactManifest = "artifact_manifest"
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
}

// Validate checks the manifest invariants. Returns nil if the manifest
// is well-formed, or a descriptive error covering the first violation.
//
// Invariants:
//   - SchemaVersion must be non-empty.
//   - At least one Artifact must be present.
//   - Every Required artefact must have a non-empty Path.
//   - Every artefact with a non-empty Path must have a non-empty Filename.
func (m *ArtifactManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if strings.TrimSpace(m.SchemaVersion) == "" {
		return fmt.Errorf("schema_version is empty")
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("manifest has zero artifacts")
	}

	for i, a := range m.Artifacts {
		if strings.TrimSpace(a.ID) == "" {
			return fmt.Errorf("artifact[%d]: id is empty", i)
		}
		if strings.TrimSpace(a.Kind) == "" {
			return fmt.Errorf("artifact[%d] (%s): kind is empty", i, a.ID)
		}
		if a.Required && strings.TrimSpace(a.Path) == "" {
			return fmt.Errorf("artifact[%d] (%s): required but path is empty", i, a.ID)
		}
		if strings.TrimSpace(a.Path) != "" && strings.TrimSpace(a.Filename) == "" {
			return fmt.Errorf("artifact[%d] (%s): path set but filename is empty", i, a.ID)
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
//   1. The value is already a *ArtifactManifest — returned directly.
//   2. The value is a json.RawMessage / []byte / string — unmarshalled.
//   3. The value is a map[string]any — re-serialised to JSON then unmarshalled
//      (covers the case where the handler embedded a literal map).
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

// UploadedManifest is the Sender-safe representation of a job's artefacts
// after all uploads have completed. It contains no local filesystem paths
// — every artefact references a RemoteAssetID that the Sender can resolve.
type UploadedManifest struct {
	SchemaVersion string             `json:"schema_version"`
	WorkflowID    string             `json:"workflow_id"`
	JobID         string             `json:"job_id"`
	Artifacts     []UploadedArtifact `json:"artifacts"`
}

// ── Upload status constants ──────────────────────────────────────

const (
	StatusReady   = "ready"
	StatusSkipped = "skipped"
)

// UploadedArtifact is a single artefact in the Sender-safe manifest.
// Path and SizeBytes are intentionally omitted — the Sender resolves
// the file via RemoteAssetID.
type UploadedArtifact struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
	MIMEType      string `json:"mime_type"`
	SHA256        string `json:"sha256"`
	RemoteAssetID string `json:"remote_asset_id"`
	Status        string `json:"status"` // StatusReady | StatusSkipped
}

// WithRemoteLocations replaces every Artifact's local Path with the
// corresponding RemoteAsset reference from the uploaded map, keyed by
// Artifact.ID. The returned UploadedManifest contains no local filesystem
// paths and is suitable for serialisation into the job's Complete result.
//
// Required artefacts that are missing from the uploaded map trigger an
// error. Non-required artefacts not in the map receive status "skipped".
func (m *ArtifactManifest) WithRemoteLocations(uploaded map[string]RemoteAsset) (*UploadedManifest, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	result := &UploadedManifest{
		SchemaVersion: m.SchemaVersion,
		WorkflowID:    m.WorkflowID,
		JobID:         m.JobID,
		Artifacts:     make([]UploadedArtifact, 0, len(m.Artifacts)),
	}

	for _, a := range m.Artifacts {
		remote, ok := uploaded[a.ID]
		if a.Required && !ok {
			return nil, fmt.Errorf("artifact %q (%s) is required but was not uploaded", a.ID, a.Kind)
		}

		ua := UploadedArtifact{
			ID:       a.ID,
			Kind:     a.Kind,
			Filename: a.Filename,
			MIMEType: a.MIMEType,
			SHA256:   a.SHA256,
		}

		if ok {
			ua.RemoteAssetID = remote.RemoteAssetID
			ua.Status = StatusReady
		} else {
			ua.Status = StatusSkipped
		}

		result.Artifacts = append(result.Artifacts, ua)
	}

	return result, nil
}

// ComputeSHA256 reads the file at path and returns its hex-encoded
// SHA-256 digest. Used by the runner before upload to populate
// Artifact.SHA256.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("sha256: open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("sha256: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
