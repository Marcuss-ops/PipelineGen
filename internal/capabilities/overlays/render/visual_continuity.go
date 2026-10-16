package render

// Package overlays — visual_continuity.go owns the Visual Continuity
// Manager: a planner over the WHOLE scene sequence, not a per-sentence
// detector.
//
// Continuity differs from anti-repetition: MediaMemory already penalizes
// asset and source reuse (the shared usage history, consumed here through
// plain asset-id lists — no second ranking engine). This manager keeps one
// stable visual identity per entity (preferred asset, style family, accent)
// so a company seen in scene 2 and again in scene 9 stays recognizable,
// while the concrete asset MAY rotate to avoid hammering the same image.
// Deterministic: same sequence + assets + history → same identities.

import (
	"sort"
	"strings"
)

// ContinuityState is the per-entity project state.
type ContinuityState struct {
	EntityID       string `json:"entity_id"`
	PreferredAsset string `json:"preferred_asset_id"`
	StyleFamily    string `json:"style_family"`
	AccentToken    string `json:"accent_token"`
	LastSceneID    string `json:"last_scene_id"`
	SeenScenes     int    `json:"seen_scenes"`
}

// ContinuityInput is one entity appearance in narration order.
type ContinuityInput struct {
	EntityID string
	SceneID  string
	Assets   []string // resolved asset ids, caller preference order
	Style    string   // resolved style family for this appearance
	Accent   string   // resolved accent token for this appearance
}

// ResolveContinuity folds the full appearance sequence into stable states.
// Rules (deterministic, in order):
//  1. First appearance pins identity: preferred asset = first resolved
//     asset, style/accent = first resolved values.
//  2. Later appearances keep identity (style/accent stay pinned) but rotate
//     the preferred asset when the caller offers alternatives ordered
//     differently (e.g. MediaMemory demoted the reused asset): the first
//     offered asset that differs from the pinned one wins, so the same
//     image is not hammered while the entity stays recognizable.
//  3. Empty asset lists never clear a pinned identity.
func ResolveContinuity(appearances []ContinuityInput) map[string]ContinuityState {
	states := map[string]ContinuityState{}
	for _, a := range appearances {
		id := strings.TrimSpace(a.EntityID)
		if id == "" {
			continue
		}
		st, ok := states[id]
		if !ok {
			pref := ""
			if len(a.Assets) > 0 {
				pref = a.Assets[0]
			}
			states[id] = ContinuityState{
				EntityID: id, PreferredAsset: pref,
				StyleFamily: a.Style, AccentToken: a.Accent,
				LastSceneID: a.SceneID, SeenScenes: 1,
			}
			continue
		}
		if len(a.Assets) > 0 {
			rotated := ""
			for _, asset := range a.Assets {
				if asset != st.PreferredAsset {
					rotated = asset
					break
				}
			}
			if rotated != "" {
				st.PreferredAsset = rotated
			}
		}
		st.LastSceneID = a.SceneID
		st.SeenScenes++
		states[id] = st
	}
	return states
}

// ContinuityStatesSorted returns states in entity-id order for canonical
// persistence.
func ContinuityStatesSorted(states map[string]ContinuityState) []ContinuityState {
	out := make([]ContinuityState, 0, len(states))
	for _, st := range states {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out
}
