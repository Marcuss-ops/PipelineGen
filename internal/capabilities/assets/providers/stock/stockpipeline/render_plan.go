package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var stockTransitionIDs = []string{"fadeblack", "fadewhite", "flash", "blur", "gray", "colorred", "colorblue", "colorgreen", "coloryellow", "colorpurple", "colororange", "colorpink", "negate", "vignette", "fastblur"}

func ResolveRenderPlan(req RenderRequest) (RenderRequest, error) {
	resolved := req
	resolved.Transitions = append([]RenderTransition(nil), req.Transitions...)
	resolved.EffectPaths = append([]RenderEffectPath(nil), req.EffectPaths...)
	if !req.NoTransitions && len(resolved.Transitions) == 0 && req.TransitionEvery > 0 {
		for index := range req.InputPaths {
			if (index+1)%req.TransitionEvery == 0 {
				resolved.Transitions = append(resolved.Transitions, RenderTransition{ClipIndex: index, Segment: "end", ID: stockTransitionIDs[((index+1)/req.TransitionEvery-1)%len(stockTransitionIDs)]})
			}
			if index > 0 && index%req.TransitionEvery == 0 {
				resolved.Transitions = append(resolved.Transitions, RenderTransition{ClipIndex: index, Segment: "start", ID: stockTransitionIDs[(index/req.TransitionEvery-1)%len(stockTransitionIDs)]})
			}
		}
	}
	for _, transition := range resolved.Transitions {
		if transition.ID == "" || (transition.Segment != "start" && transition.Segment != "end") || transition.ClipIndex < 0 || transition.ClipIndex >= len(req.InputPaths) {
			return RenderRequest{}, fmt.Errorf("invalid resolved transition assignment")
		}
	}
	if len(resolved.Transitions) == 0 {
		resolved.NoTransitions = true
	}

	effectTargets := req.EffectEvery > 0 && len(req.InputPaths) > 0
	if effectTargets {
		effectTargets = false
		for index := range req.InputPaths {
			if (index+1)%req.EffectEvery == 0 {
				effectTargets = true
				break
			}
		}
	}
	if !req.NoEffects && len(resolved.EffectPaths) == 0 && effectTargets {
		if req.EffectsDir == "" {
			return RenderRequest{}, fmt.Errorf("effect selection requires a configured effects directory")
		}
		effectPath, err := selectStockEffect(req.EffectsDir, req.EffectIndexHint)
		if err != nil {
			return RenderRequest{}, err
		}
		if effectPath == "" {
			return RenderRequest{}, fmt.Errorf("effect selection found no .mp4 assets in %q", req.EffectsDir)
		}
		for index := range req.InputPaths {
			if (index+1)%req.EffectEvery == 0 {
				resolved.EffectPaths = append(resolved.EffectPaths, RenderEffectPath{ClipIndex: index, Path: effectPath})
			}
		}
	}
	for _, effect := range resolved.EffectPaths {
		if effect.Path == "" || effect.ClipIndex < 0 || effect.ClipIndex >= len(req.InputPaths) {
			return RenderRequest{}, fmt.Errorf("invalid resolved effect path assignment")
		}
	}
	if len(resolved.EffectPaths) == 0 {
		resolved.NoEffects = true
	}
	return resolved, nil
}

func selectStockEffect(dir string, hint int) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read stock effects directory %q: %w", dir, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", nil
	}
	index := hint % len(paths)
	if index < 0 {
		index += len(paths)
	}
	return paths[index], nil
}
