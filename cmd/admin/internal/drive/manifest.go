// cmd/admin/manifest.go — JSON manifest types and validator for the
// index-drive-clip admin command (Sprint 2.2).
//
// The manifest format replaces the hardcoded per-animal data previously
// embedded in cmd/admin/index_beluga_clip.go and
// cmd/admin/index_drive_fish_clip.go (both retired in Sprint 2.2).
// Operational fixtures live under cmd/admin/manifests/*.json and are
// decoded by loadIndexClipManifest.

package drive

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// indexClipManifest is the JSON shape read from --manifest <path>.
//
// Every field is strictly typed so a typo in a fixture is caught by
// the JSON decoder (DisallowUnknownFields) and by Validate(). No
// silent defaults are applied: missing required fields fail closed.
type indexClipManifest struct {
	// DriveFileID is the Google Drive file ID of the source clip.
	// May be overridden by --drive-file-id on the CLI for re-index
	// reuse of the same manifest against a different upload.
	DriveFileID string `json:"drive_file_id"`

	// Name is the human-readable title stored on the Asset.
	Name string `json:"name"`

	// Description is the primary long-form search text (first paragraph).
	Description string `json:"description"`

	// DescriptionAlt is the secondary localized description (often the
	// English/Italian variant). Optional: when empty the SearchText
	// is assembled without it.
	DescriptionAlt string `json:"description_alt,omitempty"`

	// Tags is the asset's tag list and is also appended to SearchText.
	// Must be non-empty.
	Tags []string `json:"tags"`

	// Source is the asset.Source() enum value (e.g. "clip_drive",
	// "ai_generated").
	Source string `json:"source"`

	// Category is the asset.Category() value AND the local storage
	// subdirectory unless LocalSubdir overrides it.
	Category string `json:"category"`

	// Group is the asset.Group() value (e.g. "funny_animals",
	// "topfive_fish").
	Group string `json:"group"`

	// LocalSubdir overrides the local storage subdirectory. If empty,
	// the command defaults to Category.
	LocalSubdir string `json:"local_subdir,omitempty"`

	// DefaultFilename is used when Drive returns an empty filename.
	// If empty, the command fails closed (no silent default).
	DefaultFilename string `json:"default_filename,omitempty"`

	// DurationFallbackSeconds is used when ffprobe fails.
	// If <= 0, the command fails closed (no silent false success).
	// Sprint 2.2 deliberately preserves Beluga's silent 9s fallback by
	// setting this to 9 in cmd/admin/manifests/beluga.json, but the
	// default behaviour for new fixtures is fail-closed.
	DurationFallbackSeconds int `json:"duration_fallback_seconds,omitempty"`

	// Metadata is a free-form map of asset.SetMetadataString entries.
	// Common keys: content_type, subject, visual_summary, timeline_json,
	// hook, audio_policy, sound_design_plan. mime_type is filled at
	// runtime from the Drive meta and MUST NOT be set in the manifest.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate enforces the invariants that index-drive-clip requires.
// Any failure here MUST fail closed — never silently default.
func (m *indexClipManifest) Validate() error {
	if m.DriveFileID == "" {
		return errors.New("manifest: drive_file_id is required")
	}
	if m.Name == "" {
		return errors.New("manifest: name is required")
	}
	if m.Description == "" {
		return errors.New("manifest: description is required")
	}
	if len(m.Tags) == 0 {
		return errors.New("manifest: tags must be non-empty")
	}
	if m.Source == "" {
		return errors.New("manifest: source is required")
	}
	if m.Category == "" && m.LocalSubdir == "" {
		return errors.New("manifest: either category or local_subdir must be set")
	}
	if m.Group == "" {
		return errors.New("manifest: group is required")
	}
	if m.DurationFallbackSeconds < 0 {
		return errors.New("manifest: duration_fallback_seconds must be >= 0")
	}
	return nil
}

// loadIndexClipManifest reads, decodes (strict: rejects unknown
// fields), and validates the manifest at path.
func loadIndexClipManifest(path string) (*indexClipManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var m indexClipManifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	return &m, nil
}
