// Package templates — google_doc_template.go
//
// Azione 12 (P1, CUTOVER-COMPLETE-WITH-ARTIFACTS, July 2026):
// render_scene function for Google Doc scene assembly from finalized assets.
//
// render_scene takes a set of *asset.Asset inputs and produces a
// rendered scene description suitable for insertion into a Google Doc.
// It looks up asset_locations for the canonical webViewLink so the
// Doc contains live Drive links — NOT local filesystem paths.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// "Google Doc rendering for finalized assets" fact. Manual
// concatenation of Drive links is forbidden (audit-pin).
//
// godlike/07 typed-error contract: assets that are not finalized
// (LifecycleState != ACTIVE) are rejected with ErrImageNotFinalized.
package templates

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Typed sentinels ────────────────────────────────────────────────────

// ErrImageNotFinalized is returned when render_scene encounters an
// asset whose LifecycleState is not ACTIVE. Only finalized (published
// and indexed) assets can appear in a Google Doc — rendering an
// asset that is still STAGING, PROCESSING, or in a deletion state
// would produce a dead link.
var ErrImageNotFinalized = errors.New("google doc template: image not finalized (LifecycleState must be ACTIVE)")

// ── Scene rendering ────────────────────────────────────────────────────

// Scene represents a single rendered scene for Google Doc insertion.
type Scene struct {
	// Title is the scene title (derived from asset Name or Metadata).
	Title string

	// Description is the scene narrative text.
	Description string

	// ImageURL is the canonical Drive webViewLink for the scene image.
	// Sourced from asset_locations; NOT a local filesystem path.
	ImageURL string

	// AssetID is the canonical asset identity (for audit/correlation).
	AssetID string
}

// SceneRenderer resolves asset_locations webViewLinks for finalized
// assets. The concrete implementation (wired in composition root)
// performs the DB lookup against the asset_locations table.
//
// Returns ("", ErrImageNotFinalized) if the asset is not found or
// the lookup fails — the caller can errors.Is against the sentinel
// to distinguish "asset not finalized" from "DB lookup failed."
type SceneRenderer interface {
	// WebViewLink returns the canonical Drive webViewLink for the
	// given asset ID. Sourced from asset_locations with
	// location_kind='drive'. Returns ("", error) on lookup failure
	// so the caller can surface the error rather than silently
	// producing a dead link (godlike/07 no-fake-availability).
	WebViewLink(assetID string) (string, error)
}

// RenderScene assembles a Scene from the given asset.Asset inputs.
//
// Rules (godlike/07):
//   - Every asset MUST have LifecycleState == StateActive.
//     Non-ACTIVE assets are rejected with ErrImageNotFinalized.
//   - The rendered ImageURL is sourced from the SceneRenderer's
//     asset_locations lookup — manual Drive-link concatenation is
//     forbidden (godlike/06 SSOT audit-pin).
//   - The rendered output NEVER contains local filesystem paths.
//   - At least one asset is required (empty slice → error).
//
// Returns the rendered Scene on success, or a typed error on failure.
func RenderScene(assets []*asset.Asset, renderer SceneRenderer) (*Scene, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("google doc template: at least one asset is required")
	}

	// Validate all assets are finalized BEFORE rendering.
	for _, a := range assets {
		if a == nil {
			return nil, fmt.Errorf("%w: nil asset in input slice", ErrImageNotFinalized)
		}
		if a.LifecycleState != asset.StateActive {
			return nil, fmt.Errorf("%w: asset %s has LifecycleState=%s",
				ErrImageNotFinalized, a.ID, a.LifecycleState)
		}
	}

	// Build the scene from the primary asset (first in slice).
	primary := assets[0]

	scene := &Scene{
		AssetID: primary.ID,
		Title:   resolveSceneTitle(primary),
	}

	// Look up the canonical webViewLink from asset_locations.
	if renderer != nil {
		link, err := renderer.WebViewLink(primary.ID)
		if err != nil {
			return nil, fmt.Errorf("google doc template: webViewLink lookup for asset %s: %w", primary.ID, err)
		}
		scene.ImageURL = link
	}
	// If renderer is nil (test-only path), ImageURL stays empty.
	// Production code MUST wire a real SceneRenderer.

	// Build the description from all assets' metadata.
	var descParts []string
	for _, a := range assets {
		if desc := a.GetMetadataString("scene_description"); desc != "" {
			descParts = append(descParts, desc)
		}
		if narrative := a.GetMetadataString("narrative"); narrative != "" {
			descParts = append(descParts, narrative)
		}
	}
	scene.Description = strings.Join(descParts, "\n\n")
	if scene.Description == "" && primary.Name != "" {
		// Fallback: use the primary asset's name + search terms.
		scene.Description = fmt.Sprintf("Scene: %s\nKeywords: %s",
			primary.Name,
			strings.Join(primary.SearchTerms, ", "))
	}
	if scene.Description == "" {
		scene.Description = "(untitled scene)"
	}

	return scene, nil
}

// resolveSceneTitle derives the scene title from asset metadata.
// Precedence: scene_title metadata → asset Name → asset Filename.
func resolveSceneTitle(a *asset.Asset) string {
	if title := a.GetMetadataString("scene_title"); title != "" {
		return title
	}
	if a.Name != "" {
		return a.Name
	}
	return a.Filename
}

// ── Batch rendering ────────────────────────────────────────────────────

// RenderScenes assembles multiple scenes from asset groups.
// Each group (sub-slice of assets) produces one Scene.
// All assets must be finalized (LifecycleState == ACTIVE).
//
// Returns the rendered scenes on success, or a typed error on failure.
func RenderScenes(groups [][]*asset.Asset, renderer SceneRenderer) ([]*Scene, error) {
	if len(groups) == 0 {
		return nil, fmt.Errorf("google doc template: at least one asset group is required")
	}

	scenes := make([]*Scene, 0, len(groups))
	for i, group := range groups {
		scene, err := RenderScene(group, renderer)
		if err != nil {
			return nil, fmt.Errorf("google doc template: scene group %d: %w", i, err)
		}
		scenes = append(scenes, scene)
	}
	return scenes, nil
}
