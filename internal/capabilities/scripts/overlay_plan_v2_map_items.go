package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// overlay_plan_v2_map_items.go — lowering of the Rust-selected map items onto
// certified plates: chain presets, plate resolution, pin timing and the
// presentation/style choices the canvas requested.

// materializeEditorialMapItems lowers Rust-selected grounded places through
// the existing certified map-plate resolver. Rust owns which place to show
// and when; this Producer boundary attaches the licensed local/dynamic map
// source needed by the renderer.
// applyChainSegmentPresets mirrors the semantic_role → preset_id decision onto
// every chain segment. Rust coalesces standalone overlays into one chain and
// carries each segment's kind/template, but the preset decision belongs to
// this resolver (ADR-029). Without it RenderingGen lowers the text segments
// with no preset, emits a partial style (fill/stroke only, no font/font_size)
// and the Chronon daemon rejects the whole render plan.
func applyChainSegmentPresets(plan *capmodel.OverlayPlan) {
	if plan == nil {
		return
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		firstPreset := ""
		capmodel.UpdateChainSegmentMaps(item.Params, func(segments []map[string]any) bool {
			changed := false
			for _, segment := range segments {
				preset := strings.TrimSpace(chainSegmentString(segment, "preset_id"))
				if preset == "" {
					templateID := strings.TrimSpace(chainSegmentString(segment, "template_id"))
					if templateID == "" {
						continue
					}
					resolved, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(templateID)
					if !ok {
						continue
					}
					preset = resolved
					segment["preset_id"] = resolved
					changed = true
				}
				if firstPreset == "" {
					firstPreset = preset
				}
			}
			return changed
		})
		// The chain item mirrors its first segment for consumers that do not
		// read chain_segments (motion/preview paths).
		if strings.TrimSpace(item.PresetID) == "" && firstPreset != "" {
			item.PresetID = firstPreset
		}
	}
}

func chainSegmentString(segment map[string]any, key string) string {
	if value, ok := segment[key].(string); ok {
		return value
	}
	return ""
}

func materializeEditorialMapItems(plan *capmodel.OverlayPlan, plates capabilityoverlay.PlateResolver, canvas OverlayCanvasSpec, seed string) error {
	if plan == nil {
		return nil
	}
	applyChainSegmentPresets(plan)
	grouping, _ := mapGroupingForCanvas(canvas)
	ordinal := 0
	originalLen := len(plan.Items)
	dropped := make(map[int]struct{})
	var appended []capmodel.OverlayItem
	for i := 0; i < originalLen; i++ {
		item := &plan.Items[i]
		if strings.TrimSpace(item.PresetID) == "" {
			if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(strings.TrimSpace(item.TemplateID)); ok {
				item.PresetID = preset
			}
		}
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "map") {
			continue
		}
		if item.Map == nil {
			return fmt.Errorf("overlay plan: Rust selected map %q without a grounded map declaration", item.ID)
		}
		if _, _, _, _, _, ok := item.Map.SemanticCandidate(); !ok {
			// An already materialized map (a frozen/legacy plan or a response
			// that includes the full renderer block) needs no plate selection,
			// but its local paths are runtime-only and therefore do not survive
			// the wire/checkpoint round-trip. Reattach them from the certified
			// plate before the seal validates the raster.
			if strings.EqualFold(strings.TrimSpace(item.Map.Provider), "local") && len(item.Map.Pins) > 0 {
				attachMapAssetLocalPaths(item, plates)
				continue
			}
			return fmt.Errorf("overlay plan: Rust map %q has no semantic place candidate", item.ID)
		}
		// Membership, spoken order, the pin cap and the readable floor are the
		// Rust planner's decisions; this loop only attaches a certified plate.
		// It never re-groups, so a rendered map can no longer be matched to a
		// different Rust item (the swapped-label defect).
		pins, err := mapGroupPins(item)
		if err != nil {
			return err
		}
		// The requested cartographic level is a hard scope gate: a country pin
		// inside a city plate is noise, not a map.
		if grouping.Level != "" {
			kept := make([]capabilityoverlay.MapCandidate, 0, len(pins))
			for _, pin := range pins {
				if capabilityoverlay.ScopeFitsMapLevel(pin.Scope, grouping.Level) {
					kept = append(kept, pin)
				}
			}
			pins = kept
			if len(pins) == 0 {
				dropped[i] = struct{}{}
				continue
			}
		}
		sceneID := strings.TrimSpace(item.SceneID)
		materialized := capabilityoverlay.MaterializeMapPins(
			sceneID, pins, plates, canvas.Width, canvas.Height, ordinal, 0, seed, grouping,
		)
		if len(materialized) == 0 {
			return fmt.Errorf("overlay plan: Rust-selected map %q has no certified local map plate", item.ID)
		}
		ordinal += len(materialized)
		applyMaterializedMap(item, materialized[0])
		// A group no single plate covers is split by plate, never dropped: the
		// extra shots keep their own pins and are appended as derived items.
		for _, extra := range materialized[1:] {
			if strings.TrimSpace(extra.PresetID) == "" {
				if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(strings.TrimSpace(extra.TemplateID)); ok {
					extra.PresetID = preset
				}
			}
			appended = append(appended, extra)
		}
	}
	if len(dropped) > 0 {
		kept := plan.Items[:0]
		for i := 0; i < originalLen; i++ {
			if _, remove := dropped[i]; remove {
				continue
			}
			kept = append(kept, plan.Items[i])
		}
		plan.Items = kept
	}
	plan.Items = append(plan.Items, appended...)
	if canvas.MapPresentation != nil {
		if err := applyRequestedMapPresentation(plan, canvas.MapPresentation); err != nil {
			return err
		}
	} else if err := applyRequestedMapAnimation(plan, grouping.Animation); err != nil {
		return err
	}
	return nil
}

// mapGroupPins resolves the pins of one Rust map item. The group is
// authoritative; only an item that declares no group at all falls back to its
// single semantic place.
func mapGroupPins(item *capmodel.OverlayItem) ([]capabilityoverlay.MapCandidate, error) {
	pins, err := capabilityoverlay.MapGroupPinsFromParams(item.Params)
	if err != nil {
		return nil, fmt.Errorf("overlay plan: Rust map %q: %w", item.ID, err)
	}
	if len(pins) > 0 {
		return pins, nil
	}
	entityID, label, scope, latitude, longitude, ok := item.Map.SemanticCandidate()
	if !ok {
		return nil, fmt.Errorf("overlay plan: Rust map %q has no semantic place candidate", item.ID)
	}
	durationUS := item.DurationUS
	if durationUS <= 0 {
		durationUS = (item.EndMs - item.StartMs) * 1000
	}
	candidate, valid := capabilityoverlay.NewMapCandidate(entityID, label, latitude, longitude, item.StartUS, durationUS, 1, scope)
	if !valid {
		return nil, fmt.Errorf("overlay plan: Rust map %q contains an invalid grounded candidate", item.ID)
	}
	return []capabilityoverlay.MapCandidate{candidate}, nil
}

// mapGroupingForCanvas resolves the unified map_presentation contract into the
// planner's grouping policy. A nil presentation keeps the historical behavior.
func mapGroupingForCanvas(canvas OverlayCanvasSpec) (capabilityoverlay.MapGroupConfig, bool) {
	presentation := canvas.MapPresentation
	if presentation == nil {
		return capabilityoverlay.MapGroupConfig{}, false
	}
	return capabilityoverlay.MapGroupConfig{
		MaxPinsPerGroup:     presentation.MaxPinsPerGroup,
		NoNarrativeGrouping: !presentation.GroupNearbyPlaces,
		NoRoutes:            !presentation.AllowRoute,
		Level:               presentation.Level,
		Animation:           presentation.Animation,
	}, true
}

// applyRequestedMapAnimation makes map_presentation.animation the single owner
// of the composition when the caller named one. The planner still seeds a
// compatible default for random/auto, but an explicit certified id is applied
// to every map item and admitted against that item's real pins (count, scope,
// territory and area-glow data). An incompatible explicit request fails here,
// not inside the renderer after the expensive plate work.
func applyRequestedMapAnimation(plan *capmodel.OverlayPlan, requested string) error {
	requested = strings.TrimSpace(requested)
	if plan == nil || requested == "" || strings.EqualFold(requested, "random") || strings.EqualFold(requested, "auto") {
		return nil
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "map") || item.Map == nil {
			continue
		}
		pins := make([]map[string]any, 0, len(item.Map.Pins))
		for _, pin := range item.Map.Pins {
			pins = append(pins, map[string]any{
				"id": pin.ID, "label": pin.Label, "scope": pin.Scope,
				"latitude": pin.Latitude, "longitude": pin.Longitude,
			})
		}
		input := capabilityoverlay.MapCompositionInput{
			PinCount:             len(pins),
			Pins:                 pins,
			DurationUS:           item.DurationUS,
			Strategy:             mapStringParam(item.Params, "map_group_strategy"),
			AreaGlowRadiusKM:     item.Map.AreaGlowRadiusKM,
			CountryFeature:       mapStringParam(item.Params, "country_feature"),
			TimelineLabel:        mapStringParam(item.Params, "timeline_label"),
			MountainOverlayAsset: mapStringParam(item.Params, "mountain_overlay_asset"),
		}
		if input.MountainOverlayAsset == "" {
			input.MountainOverlayAsset = capabilityoverlay.MapMountainOverlayDefault()
		}
		if len(item.Map.Pins) > 0 {
			pin := item.Map.Pins[0]
			input.TerritoryScope = pin.Scope
			if feature, ok := capabilityoverlay.ResolveMapTerritoryFeature(
				capabilityoverlay.MapTerritoryCatalogDir(), pin.Latitude, pin.Longitude,
			); ok {
				input.TerritoryFeature = feature
				if input.CountryFeature == "" {
					input.CountryFeature = "Italy"
				}
			}
		}
		if err := capabilityoverlay.AdmitMapComposition(requested, input, capabilityoverlay.MapCompositionCertified()); err != nil {
			return fmt.Errorf("map_presentation.animation %q cannot be applied to map %q: %w", requested, item.ID, err)
		}
		if item.Params == nil {
			item.Params = make(map[string]any, 1)
		}
		item.Params["map_animation"] = requested
	}
	return nil
}

// applyRequestedMapPresentation applies the two independent runtime choices
// to every materialized map. The pools are carried on each map item because
// the dynamic renderer resolves style after the Rust planner seals the plan.
// A preset, when present, wins over the resolved defaults: it is expanded on
// its own copy so the caller's request stays the record of intent.
func applyRequestedMapPresentation(plan *capmodel.OverlayPlan, presentation *scriptpkg.MapPresentation) error {
	if plan == nil || presentation == nil {
		return nil
	}
	resolved := *presentation
	resolved.AnimationPool = append([]string(nil), presentation.AnimationPool...)
	resolved.StylePool = append([]string(nil), presentation.StylePool...)
	if err := expandMapPresetPresentation(&resolved); err != nil {
		return err
	}
	animation := strings.TrimSpace(resolved.Animation)
	if animation == "" || strings.EqualFold(animation, "auto") {
		animation = "random"
	}
	style := strings.TrimSpace(resolved.Style)
	for i := range plan.Items {
		item := &plan.Items[i]
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "map") || item.Map == nil {
			continue
		}
		if item.Params == nil {
			item.Params = make(map[string]any, 5)
		}
		if resolved.Preset != "" {
			item.Params["map_preset"] = resolved.Preset
		}
		item.Params["map_animation"] = animation
		item.Params["map_composition_pool"] = append([]string(nil), resolved.AnimationPool...)
		item.Params["map_style_pool"] = append([]string(nil), resolved.StylePool...)
		item.Params["map_type"] = style
		if resolved.CountryFeature != "" {
			item.Params["country_feature"] = resolved.CountryFeature
		}
		if resolved.TimelineLabel != "" {
			item.Params["timeline_label"] = resolved.TimelineLabel
		}
		if resolved.MountainOverlayAsset != "" {
			item.Params["mountain_overlay_asset"] = resolved.MountainOverlayAsset
		}
	}
	if !strings.EqualFold(animation, "random") {
		if err := applyRequestedMapAnimation(plan, animation); err != nil {
			return err
		}
	}
	return nil
}

// resolveMapPresentationPresetSpec expands a requested preset on the RAW spec,
// before Normalize applies its defaults. Expanding earlier is what makes an
// explicit style/animation a conflict only when it really disagrees: once
// Normalize has filled esri_topo, every preset would look contradicted.
func resolveMapPresentationPresetSpec(spec *scriptpkg.MapPresentationSpec) error {
	if spec == nil {
		return nil
	}
	id := strings.TrimSpace(spec.Preset)
	if id == "" {
		return nil
	}
	preset, ok := capabilityoverlay.LookupMapPreset(id)
	if !ok {
		return fmt.Errorf("map_presentation.preset %q is not a certified map preset", spec.Preset)
	}
	spec.Preset = preset.ID
	if requested := strings.TrimSpace(spec.Animation); requested != "" &&
		!strings.EqualFold(requested, "random") && !strings.EqualFold(requested, "auto") &&
		requested != preset.Animation {
		return fmt.Errorf("map_presentation.preset %q fixes animation %q; remove animation %q or the preset", preset.ID, preset.Animation, spec.Animation)
	}
	if requested := strings.TrimSpace(spec.Style); requested != "" && !strings.EqualFold(requested, "random") {
		if canonicalMapPresentationStyle(requested) != preset.Basemap {
			return fmt.Errorf("map_presentation.preset %q fixes style %q; remove style %q or the preset", preset.ID, preset.Basemap, spec.Style)
		}
	}
	if len(spec.AnimationPool) > 0 && !stringPoolHas(spec.AnimationPool, preset.Animation) {
		return fmt.Errorf("map_presentation.preset %q requires %q in animation_pool", preset.ID, preset.Animation)
	}
	if len(spec.StylePool) > 0 {
		found := false
		for _, candidate := range spec.StylePool {
			if canonicalMapPresentationStyle(candidate) == preset.Basemap {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("map_presentation.preset %q requires %q in style_pool", preset.ID, preset.Basemap)
		}
	}
	spec.Animation = preset.Animation
	spec.Style = preset.Basemap
	return nil
}

// expandMapPresetPresentation re-applies the preset pair on a RESOLVED
// presentation, whose style may already carry Normalize's default rather than
// the caller's intent. Unknown ids fail closed; no preset is a no-op.
func expandMapPresetPresentation(p *scriptpkg.MapPresentation) error {
	if p == nil {
		return nil
	}
	id := strings.TrimSpace(p.Preset)
	if id == "" {
		return nil
	}
	preset, ok := capabilityoverlay.LookupMapPreset(id)
	if !ok {
		return fmt.Errorf("map_presentation.preset %q is not a certified map preset", p.Preset)
	}
	p.Preset = preset.ID
	p.Animation = preset.Animation
	p.Style = preset.Basemap
	return nil
}

// canonicalMapPresentationStyle folds the accepted style aliases onto the
// renderer's basemap ids so a preset can compare intent, not spelling.
func canonicalMapPresentationStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "classic", "chronon_editorial", "chronon", "editorial":
		return "chronon_editorial"
	case "modern_map_1", "modernmap1", "modern", "topo", "terrain", "esri_topo":
		return "esri_topo"
	case "satellite", "esri_sat":
		return "esri_sat"
	}
	return strings.TrimSpace(style)
}

func mapStringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

// applyMaterializedMap copies the resolved renderer map onto the Rust-selected
// item. The item keeps its editorial identity (id, scene, priority) AND its
// window: the Rust planner owns the camera minimum, the readable floor and the
// tail bound now, so this boundary only attaches the certified plate and the
// per-pin schedule. Re-timing the item here would make Go a second owner of the
// floor (the defect the late clamp used to paper over).
func applyMaterializedMap(item *capmodel.OverlayItem, materialized capmodel.OverlayItem) {
	item.Map = materialized.Map
	item.AssetRefs = materialized.AssetRefs
	if item.Params == nil {
		item.Params = make(map[string]any, len(materialized.Params))
	}
	for key, value := range materialized.Params {
		if key != "priority" {
			item.Params[key] = value
		}
	}
	// The materialized schedule is relative to that shot's own start. The item
	// keeps the Rust start, so shift the offsets onto it: a level-gated or
	// plate-dropped pin must not activate earlier than it was spoken.
	if delta := materialized.StartUS - item.StartUS; delta != 0 {
		if timing, ok := capabilityoverlay.MapPinTimingsFromParams(materialized.Params); ok {
			item.Params["map_pin_timing"] = timing.WithOffset(delta).Params()
		}
	}
}

func validateMapPresentationPools(p scriptpkg.MapPresentation) error {
	animations := make(map[string]bool, len(p.AnimationPool))
	for _, id := range p.AnimationPool {
		id = strings.TrimSpace(id)
		if id == "" || !stringPoolHas(capabilityoverlay.MapCompositionRandomPool(), id) {
			return fmt.Errorf("map_presentation.animation_pool contains unsupported runtime animation %q", id)
		}
		if animations[id] {
			return fmt.Errorf("map_presentation.animation_pool repeats %q", id)
		}
		animations[id] = true
	}
	if len(animations) == 0 {
		return fmt.Errorf("map_presentation.animation_pool has no renderer-supported choices")
	}
	if requested := strings.TrimSpace(p.Animation); requested != "" && !strings.EqualFold(requested, "random") && !strings.EqualFold(requested, "auto") && !capabilityoverlay.NativeMapCompositions(requested) {
		return fmt.Errorf("map_presentation.animation %q is not runtime-supported", requested)
	}
	styles := make(map[string]bool, len(p.StylePool))
	for _, id := range p.StylePool {
		id = strings.TrimSpace(id)
		if id == "" || !capabilityoverlay.NativeMapBasemaps()[id] {
			return fmt.Errorf("map_presentation.style_pool contains unsupported runtime basemap %q", id)
		}
		if styles[id] {
			return fmt.Errorf("map_presentation.style_pool repeats %q", id)
		}
		styles[id] = true
	}
	if len(styles) == 0 {
		return fmt.Errorf("map_presentation.style_pool has no renderer-supported choices")
	}
	if requested := strings.TrimSpace(p.Style); requested != "" && !strings.EqualFold(requested, "random") && !capabilityoverlay.NativeMapBasemaps()[requested] {
		return fmt.Errorf("map_presentation.style %q is not runtime-supported", requested)
	}
	if strings.HasPrefix(strings.TrimSpace(p.Animation), "editorial_") {
		if p.Style != "chronon_editorial" && !strings.EqualFold(p.Style, "random") {
			return fmt.Errorf("map_presentation.animation %q requires style=chronon_editorial", p.Animation)
		}
		if !styles["chronon_editorial"] {
			return fmt.Errorf("map_presentation.animation %q requires chronon_editorial in style_pool", p.Animation)
		}
	}
	return nil
}

// stringPoolHas reports whether id is one of the certified pool entries. It is
// deliberately not named containsString: that name belongs to a test helper in
// this package and the two were colliding at compile time.
func stringPoolHas(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
