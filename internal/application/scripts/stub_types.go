// Package scripts — stub_types.go provides zero-value types and stub
// functions that api/script/flow.go still forwards to after PR2 2b/c
// (June 2026). Every type here was previously defined in flow_types.go
// or flow_helpers.go and consumed realtime/association packages now
// removed from the repo. Production wiring returns empty/nil for every
// one; the stubs prevent compilation failures in the API layer without
// dragging back the removed infrastructure packages.
//
// When the API flow.go layer is eventually decommissioned (Wave 16+),
// this file can be deleted whole.

package scripts

import "context"

// ── Type stubs ───────────────────────────────────────────────────────

// AssetSearchTarget was a struct for asset search targeting parameters.
type AssetSearchTarget = interface{}

// ScriptAssetSuggestion was a clip suggestion with score + metadata.
type ScriptAssetSuggestion = interface{}

// ScriptPhraseClipSuggestion was a phrase→clip match with metadata.
type ScriptPhraseClipSuggestion = interface{}

// ScriptDriveFolderSuggestion was a recommended Drive folder.
type ScriptDriveFolderSuggestion = interface{}

// ScriptArtlistClipSuggestion was an Artlist clip recommendation.
type ScriptArtlistClipSuggestion = interface{}

// ScriptEntityImage was an entity→image association.
type ScriptEntityImage = interface{}

// ── EntityScriptExtractor ────────────────────────────────────────────

// EntityScriptExtractor extracts entities from script segments.
// Defined here because flow_types.go was removed in a rebase refactor
// (PR2 2b/c, June 2026). The single method signature is the
// production-contract with ollama.
type EntityScriptExtractor interface {
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, limit int, model string) (interface{}, error)
}

// ── Stub functions (all return empty/nil) ────────────────────────────

func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []AssetSearchTarget, limit int) []ScriptAssetSuggestion {
	return nil
}

func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	return nil
}

func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptPhraseClipSuggestion {
	return nil
}

func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptAssetSuggestion {
	return nil
}

func EnrichSpecialNamesWithImages(ctx context.Context, svc ClipServices, specialNames []string) []ScriptEntityImage {
	return nil
}

func ResolveRecommendedDriveFolder(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	return nil
}
