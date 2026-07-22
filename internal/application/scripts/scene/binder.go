// Package scene — binder.go: scene-asset binder.
//
// SceneAssetBinder is the canonical per-scene asset binder. It knows
// only: scene_id, requirements, candidate assets, and binding policy.
// It does NOT know scene text, kind, title, index, or prose fallback.
//
// godlike/06 SSOT (one canonical owner per fact): the per-scene
// binding logic lives ONLY here for both clip and stock binding.
//
// godlike/07 NO-FAKE-AVAILABILITY: every binding mutation is
// observable — BindClips and BindStock return explicit binding maps
// that callers apply to their scene objects. The result carries
// Changed so callers can short-circuit correctly.
package scene

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// AssetRequirements is the per-scene requirements envelope.
// It is intentionally empty today; future fields (e.g. origin policy,
// style preset) will be added here without changing the binder API.
type AssetRequirements struct{}

// ClipCandidate is a candidate clip asset for binding.
type ClipCandidate struct {
	ClipID    string
	DriveLink string
	StartMs   int64
	EndMs     int64
}

// StockCandidate is a candidate stock asset for binding.
type StockCandidate struct {
	AssetID   string
	Name      string
	Source    string
	DriveLink string
	Score     float64
}

// ClipBindingPolicy controls how clip candidates are bound to scenes.
type ClipBindingPolicy struct {
	// MaxMatches limits the number of candidates bound per scene.
	// Zero means bind all candidates up to len(Candidates).
	MaxMatches int
}

// StockBindingPolicy controls how stock candidates are bound to scenes.
type StockBindingPolicy struct {
	// MaxMatches limits the number of candidates bound per scene.
	// Zero means bind all candidates up to len(Candidates).
	MaxMatches int
	// FallbackToClip, when true, allows stock binding to fall back
	// to the scene's clip drive link when no stock candidate matches.
	FallbackToClip bool
	// FallbackDriveLink is the clip drive link used when
	// FallbackToClip is true and no stock candidate matches.
	FallbackDriveLink string
}

// ClipBindingRequest is the canonical input to BindClips.
type ClipBindingRequest struct {
	SceneID      string
	Requirements AssetRequirements
	Candidates   []ClipCandidate
	Policy       ClipBindingPolicy
}

// StockBindingRequest is the canonical input to BindStock.
type StockBindingRequest struct {
	SceneID      string
	Requirements AssetRequirements
	Candidates   []StockCandidate
	Policy       StockBindingPolicy
}

// BindClipsResult is the scene-package typed return for BindClips.
type BindClipsResult struct {
	// Changed is true whenever any scene binding was assigned.
	Changed bool
	// Bindings maps scene_id -> ClipBinding.
	Bindings map[string]*script.ClipBinding
}

// BindStockResult is the scene-package typed return for BindStock.
type BindStockResult struct {
	// Changed is true whenever any scene had its Stock binding
	// assigned (real hit OR fallback to clip).
	Changed bool
	// Bindings maps scene_id -> StockBinding.
	Bindings map[string]*script.StockBinding
}

// SceneAssetBinder is the canonical per-scene asset binder shared
// by ClipBindingsProcessor and the legacy stock binding helper.
//
// The struct holds only the logger. All binding inputs are passed
// per-call, so the binder remains a pure function of scene_id,
// requirements, candidate assets, and binding policy.
type SceneAssetBinder struct {
	log *zap.Logger
}

// NewSceneAssetBinder returns a SceneAssetBinder with the supplied
// logger.
func NewSceneAssetBinder(log *zap.Logger) *SceneAssetBinder {
	return &SceneAssetBinder{log: log}
}

// BindClips binds clip candidates to scenes 1:1 in candidate order.
// It returns a map of scene_id -> ClipBinding. Extra scenes beyond
// the candidate count receive no binding.
func (b *SceneAssetBinder) BindClips(reqs []ClipBindingRequest) BindClipsResult {
	if len(reqs) == 0 {
		return BindClipsResult{}
	}

	bindings := make(map[string]*script.ClipBinding)
	changed := false

	for _, req := range reqs {
		candidates := req.Candidates
		if req.Policy.MaxMatches > 0 && req.Policy.MaxMatches < len(candidates) {
			candidates = candidates[:req.Policy.MaxMatches]
		}

		for _, c := range candidates {
			if c.ClipID == "" {
				continue
			}
			bindings[req.SceneID] = &script.ClipBinding{
				ClipID:     c.ClipID,
				DriveLink:  c.DriveLink,
				StartMs:    c.StartMs,
				EndMs:      c.EndMs,
				DurationMs: script.ClipDurationMs(c.StartMs, c.EndMs),
			}
			changed = true
			break // one clip per scene, first candidate wins
		}
	}

	if b.log != nil {
		b.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(reqs)),
			zap.Int("clips_bound", len(bindings)))
	}

	return BindClipsResult{
		Changed:  changed,
		Bindings: bindings,
	}
}

// BindStock binds stock candidates to scenes. It returns a map of
// scene_id -> StockBinding. When no candidate matches and
// Policy.FallbackToClip is true, it falls back to ClipDriveLink.
func (b *SceneAssetBinder) BindStock(reqs []StockBindingRequest) BindStockResult {
	if len(reqs) == 0 {
		return BindStockResult{}
	}

	bindings := make(map[string]*script.StockBinding)
	changed := false

	for _, req := range reqs {
		bound := false
		candidates := req.Candidates
		if req.Policy.MaxMatches > 0 && req.Policy.MaxMatches < len(candidates) {
			candidates = candidates[:req.Policy.MaxMatches]
		}

		for _, c := range candidates {
			if c.AssetID == "" {
				continue
			}
			bindings[req.SceneID] = &script.StockBinding{
				AssetID:   c.AssetID,
				Name:      c.Name,
				Source:    c.Source,
				DriveLink: c.DriveLink,
				Score:     c.Score,
				Fallback:  false,
			}
			changed = true
			bound = true
			break // one stock per scene, first candidate wins
		}

		if !bound && req.Policy.FallbackToClip && req.Policy.FallbackDriveLink != "" {
			bindings[req.SceneID] = &script.StockBinding{
				DriveLink: req.Policy.FallbackDriveLink,
				Fallback:  true,
			}
			changed = true
		}
	}

	if b.log != nil {
		b.log.Info("stock binding: processed scenes",
			zap.Int("scenes", len(reqs)),
			zap.Int("stocks_bound", len(bindings)))
	}

	return BindStockResult{
		Changed:  changed,
		Bindings: bindings,
	}
}
