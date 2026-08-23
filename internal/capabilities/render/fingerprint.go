// Package render — fingerprint.go owns the canonical scene fingerprint: the
// deterministic SHA-256 identity of one scene's render work unit. It is the
// single identity shared by artifact cache lookup, checkpoint resume and
// replay: any input that can change the rendered pixels must change the
// fingerprint, and any input that cannot must not.
//
// Ownership split:
//
//	Caller            → composes SceneFingerprintInput (hashes come from
//	                    the canonical plans it already owns)
//	render package    → canonicalizes and hashes (this file)
//
// The fingerprint never embeds staging details (local paths, job ids,
// revisions): only content-addressable identity (asset SHA-256, plan hashes)
// plus the semantic scene id and the renderer/policy versions.
package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SceneFingerprintVersion is the schema version of SceneFingerprintInput.
// Bump it when the JSON shape or semantics change so fingerprints computed
// before the change are never silently misread after it.
const SceneFingerprintVersion = 1

var ErrInvalidFingerprint = errors.New("invalid scene fingerprint")

// AssetFingerprint is the content-addressable identity of one source asset
// contributing to a scene render: only the canonical asset id and its byte
// SHA-256. Local paths are staging details and deliberately absent.
type AssetFingerprint struct {
	AssetID string `json:"asset_id"`
	SHA256  string `json:"sha256"`
}

// SceneFingerprintInput is every canonical input that determines a scene
// render. Hash fields must be lowercase 64-hex SHA-256 values produced by
// the canonical plan owners (timeline, audio plan, overlay plan, subtitle
// plan, output profile). OverlayPlanHash and SubtitlePlanHash are optional:
// empty means the scene carries no overlay/subtitle surface.
type SceneFingerprintInput struct {
	Version     int                `json:"version"`
	SceneID     string             `json:"scene_id"`
	VideoAssets []AssetFingerprint `json:"video_assets"`

	TimelineHash     string `json:"timeline_hash"`
	OverlayPlanHash  string `json:"overlay_plan_hash,omitempty"`
	SubtitlePlanHash string `json:"subtitle_plan_hash,omitempty"`
	AudioPlanHash    string `json:"audio_plan_hash"`

	OutputProfileHash string `json:"output_profile_hash"`
	RendererVersion   string `json:"renderer_version"`
}

// ComputeSceneFingerprint canonicalizes the input (asset order is sorted by
// asset id) and returns the deterministic SHA-256 that is the canonical
// identity of the scene render. Validation fails closed: any missing or
// malformed input yields an error, never a silently weakened fingerprint.
func ComputeSceneFingerprint(input SceneFingerprintInput) (string, error) {
	if input.Version != SceneFingerprintVersion {
		return "", fmt.Errorf("%w: unsupported version %d", ErrInvalidFingerprint, input.Version)
	}
	sceneID := strings.TrimSpace(input.SceneID)
	if sceneID == "" {
		return "", fmt.Errorf("%w: scene_id is required", ErrInvalidFingerprint)
	}
	if len(input.VideoAssets) == 0 {
		return "", fmt.Errorf("%w: scene %s requires at least one video asset", ErrInvalidFingerprint, sceneID)
	}
	assets := append([]AssetFingerprint(nil), input.VideoAssets...)
	sort.Slice(assets, func(i, j int) bool { return assets[i].AssetID < assets[j].AssetID })
	seen := make(map[string]struct{}, len(assets))
	for i, asset := range assets {
		if strings.TrimSpace(asset.AssetID) == "" || !isSHA256(asset.SHA256) {
			return "", fmt.Errorf("%w: asset[%d] requires asset_id and SHA256", ErrInvalidFingerprint, i)
		}
		if _, ok := seen[asset.AssetID]; ok {
			return "", fmt.Errorf("%w: duplicate asset %q", ErrInvalidFingerprint, asset.AssetID)
		}
		seen[asset.AssetID] = struct{}{}
	}
	for field, value := range map[string]string{
		"timeline_hash":       input.TimelineHash,
		"audio_plan_hash":     input.AudioPlanHash,
		"output_profile_hash": input.OutputProfileHash,
	} {
		if !isSHA256(value) {
			return "", fmt.Errorf("%w: %s must be a valid SHA256", ErrInvalidFingerprint, field)
		}
	}
	for field, value := range map[string]string{
		"overlay_plan_hash":  input.OverlayPlanHash,
		"subtitle_plan_hash": input.SubtitlePlanHash,
	} {
		if value != "" && !isSHA256(value) {
			return "", fmt.Errorf("%w: %s must be a valid SHA256", ErrInvalidFingerprint, field)
		}
	}
	if strings.TrimSpace(input.RendererVersion) == "" {
		return "", fmt.Errorf("%w: renderer_version is required", ErrInvalidFingerprint)
	}
	canonical := input
	canonical.SceneID = sceneID
	canonical.VideoAssets = assets
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidFingerprint, err)
	}
	return digest.SHA256Bytes(b), nil
}

// AssetsFromManifest projects a render plan manifest into fingerprint form:
// the content-addressable identity (asset id + SHA-256) of every manifest
// entry, with local paths dropped. Order of the input is preserved; the
// fingerprint computation itself sorts by asset id.
func AssetsFromManifest(manifest []AssetManifestEntry) []AssetFingerprint {
	assets := make([]AssetFingerprint, 0, len(manifest))
	for _, entry := range manifest {
		assets = append(assets, AssetFingerprint{AssetID: entry.AssetID, SHA256: entry.SHA256})
	}
	return assets
}
