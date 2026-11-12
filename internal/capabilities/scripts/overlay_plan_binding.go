package scriptgeneration

// overlay_plan_binding.go owns the IDENTITY LINK between the overlay plan and
// the render that was produced from it (Kaufmann post-mortem, interventions A
// and C).
//
// The defect it closes: the render path used to normalize the plan on a private
// snapshot while the durable result kept the un-normalized plan. The renderer
// and the manifest then described one plan, the checkpoint another, and
// `pendingOverlayPlans` reused an `OverlayRender` without ever asking which
// plan version produced it. A retry therefore served pixels that no longer
// matched the plan, and the final-job handoff only discovered the divergence at
// payload time.
//
// The repair is one identity: every plan has a deterministic digest, every
// render reference records the digest it was produced from, and every reuse /
// resume / handoff decision compares the two. The digest deliberately excludes
// run identity, timestamps and Drive routing so a legitimate retry of the same
// content keeps the same identity.

import (
	"encoding/json"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strconv"
	"strings"
)

// overlayPlanDigestVersion tags the digest shape so a future field set cannot
// silently re-use identities minted by this one.
const overlayPlanDigestVersion = "overlay-plan-render/v1"

// overlayPlanDigestItem is one item's render-relevant identity. It intentionally
// carries BOTH the editorial identity (id, scene, kind) and the visual identity
// (motion, preset, timing, render key, asset digests): a change in either must
// produce a different digest, because either can change the pixels.
type overlayPlanDigestItem struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind,omitempty"`
	SceneID        string   `json:"scene_id,omitempty"`
	TemplateID     string   `json:"template_id,omitempty"`
	PresetID       string   `json:"preset_id,omitempty"`
	ImagePresetID  string   `json:"image_preset_id,omitempty"`
	MotionID       string   `json:"motion_id,omitempty"`
	CameraMotionID string   `json:"camera_motion_id,omitempty"`
	Text           string   `json:"text,omitempty"`
	EntityID       string   `json:"entity_id,omitempty"`
	EntityCaption  string   `json:"entity_caption,omitempty"`
	StartUS        int64    `json:"start_us"`
	EndUS          int64    `json:"end_us"`
	Assets         []string `json:"assets,omitempty"`
	// ContentKey is ComputeRenderKey recomputed from the item's own content. It
	// covers the visual facts this document does not enumerate (params, image
	// layers, style, frame, map, sound effect, caption motion), so two plans
	// can never share a digest while rendering different pixels.
	ContentKey string `json:"content_key,omitempty"`
}

// overlayPlanDigestLayer is the content identity of a plan-level media layer
// (the source video or the full-canvas background). Locators are excluded:
// Path is a workspace address and LocalPath is producer-only.
type overlayPlanDigestLayer struct {
	Kind    string   `json:"kind,omitempty"`
	AssetID string   `json:"asset_id,omitempty"`
	SHA256  string   `json:"sha256,omitempty"`
	Assets  []string `json:"assets,omitempty"`
	Color   []string `json:"color,omitempty"`
	Fit     string   `json:"fit,omitempty"`
	Opacity string   `json:"opacity,omitempty"`
	Loop    bool     `json:"loop,omitempty"`
	Style   string   `json:"style,omitempty"`
}

// overlayPlanDigestPayload is the canonical digest document.
type overlayPlanDigestPayload struct {
	Version                string                  `json:"version"`
	SchemaVersion          string                  `json:"schema_version"`
	Width                  int                     `json:"width"`
	Height                 int                     `json:"height"`
	FPSNum                 int                     `json:"fps_num"`
	FPSDen                 int                     `json:"fps_den"`
	DurationUS             int64                   `json:"duration_us"`
	EntityVariationSeed    string                  `json:"entity_variation_seed,omitempty"`
	ForegroundScalePercent int                     `json:"foreground_scale_percent,omitempty"`
	RendererVersion        string                  `json:"renderer_version,omitempty"`
	OutputProfileID        string                  `json:"output_profile_id,omitempty"`
	MediaContract          string                  `json:"media_contract,omitempty"`
	Source                 *overlayPlanDigestLayer `json:"source,omitempty"`
	Background             *overlayPlanDigestLayer `json:"background,omitempty"`
	AnimationPolicies      string                  `json:"animation_policies,omitempty"`
	Items                  []overlayPlanDigestItem `json:"items"`
}

// OverlayPlanRenderDigest is the deterministic identity of a renderable plan.
//
// What it covers: schema, canvas, frame rate, plan duration, the output profile
// and media contract, and the ORDERED item list with each item's editorial
// identity, content text, canonical timing, motion facts, render key and asset
// content addresses.
//
// What it deliberately excludes: PlanID/VideoID/ProjectID-lookalike run
// identity, Drive routing (DriveJobID/DriveFolderID/ResultJobID), the runtime
// Require* publication flags and any execution timestamp. Two runs that must
// produce the same pixels therefore agree, which is what makes reuse legitimate.
//
// An empty plan has no digest ("") because there is no render to bind.
func OverlayPlanRenderDigest(plan *capmodel.OverlayPlan) string {
	doc := overlayPlanDigestDocument(plan)
	if doc == nil {
		return ""
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		// A plan that cannot be marshalled has no certifiable identity; the
		// caller treats "" as "cannot prove", never as a match.
		return ""
	}
	return digest.SHA256Bytes(raw)
}

// overlayPlanDigestDocument is the canonical digest document. It is factored
// out so the identity can be inspected (and so tests can compare two plans'
// documents field by field) without recomputing the hash.
func overlayPlanDigestDocument(plan *capmodel.OverlayPlan) *overlayPlanDigestPayload {
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}
	payload := overlayPlanDigestPayload{
		Version:                overlayPlanDigestVersion,
		SchemaVersion:          strings.TrimSpace(plan.SchemaVersion),
		Width:                  plan.Width,
		Height:                 plan.Height,
		FPSNum:                 plan.FPSNum,
		FPSDen:                 plan.FPSDen,
		DurationUS:             int64(plan.DurationMS) * 1000,
		EntityVariationSeed:    strings.TrimSpace(plan.EntityVariationSeed),
		ForegroundScalePercent: plan.ForegroundScalePercent,
		RendererVersion:        strings.TrimSpace(plan.RendererVersion),
		OutputProfileID:        strings.TrimSpace(plan.OutputProfileID),
		MediaContract:          strings.TrimSpace(plan.MediaContract),
		Source:                 overlayDigestSource(plan.Source),
		Background:             overlayDigestBackground(plan.Background),
		AnimationPolicies:      canonicalJSON(plan.AnimationPolicies),
		Items:                  make([]overlayPlanDigestItem, 0, len(plan.Items)),
	}
	for _, item := range plan.Items {
		// The cached RenderKey is not hashed verbatim (a projection may carry it
		// or not), but its CONTENT is: ComputeRenderKey recomputes the key from
		// the item's own pixels, so the digest is transport-independent and still
		// covers params, layers, style, frame, map, sound effect and caption
		// motion — the facts this document does not enumerate.
		payload.Items = append(payload.Items, overlayPlanDigestItem{
			ID:             strings.TrimSpace(item.ID),
			Kind:           strings.TrimSpace(item.Kind),
			SceneID:        strings.TrimSpace(item.SceneID),
			TemplateID:     strings.TrimSpace(item.TemplateID),
			PresetID:       strings.TrimSpace(item.PresetID),
			ImagePresetID:  strings.TrimSpace(item.ImagePresetID),
			MotionID:       strings.TrimSpace(item.MotionID),
			CameraMotionID: strings.TrimSpace(item.CameraMotionID),
			Text:           item.Text,
			EntityID:       strings.TrimSpace(item.EntityID),
			EntityCaption:  strings.TrimSpace(item.EntityCaption),
			StartUS:        item.StartUSValue(),
			EndUS:          item.EndUSValue(),
			Assets:         overlayDigestAssetHashes(item.AssetRefs),
			ContentKey:     capmodel.ComputeRenderKey(*plan, item),
		})
	}
	return &payload
}

// overlayDigestSource names the source video by content, not by locator.
func overlayDigestSource(source *capmodel.OverlaySource) *overlayPlanDigestLayer {
	if source == nil {
		return nil
	}
	return &overlayPlanDigestLayer{
		AssetID: strings.TrimSpace(source.AssetID),
		SHA256:  strings.ToLower(strings.TrimSpace(source.SHA256)),
	}
}

// overlayDigestBackground names the full-canvas background layer, which draws
// below every item and therefore changes the pixels of every frame.
func overlayDigestBackground(background *capmodel.OverlayBackground) *overlayPlanDigestLayer {
	if background == nil {
		return nil
	}
	layer := &overlayPlanDigestLayer{
		Kind:   strings.TrimSpace(background.Kind),
		Assets: overlayDigestAssetHashes(background.AssetRefs),
		Fit:    strings.TrimSpace(background.Fit),
		Loop:   background.Loop,
		Style:  canonicalJSON(background.Style),
	}
	for _, channel := range background.Color {
		layer.Color = append(layer.Color, strconv.FormatFloat(channel, 'g', -1, 64))
	}
	if background.Opacity != nil {
		layer.Opacity = strconv.FormatFloat(*background.Opacity, 'g', -1, 64)
	}
	return layer
}

// overlayDigestAssetHashes is the content identity of a layer's assets: the
// SHA-256 when the producer knows it, else the content-addressed asset id.
func overlayDigestAssetHashes(refs []capmodel.OverlayAssetRef) []string {
	if len(refs) == 0 {
		return nil
	}
	assets := make([]string, 0, len(refs))
	for _, ref := range refs {
		sha := strings.ToLower(strings.TrimSpace(ref.SHA256))
		if sha == "" {
			sha = strings.TrimSpace(ref.AssetID)
		}
		assets = append(assets, sha)
	}
	return assets
}

// canonicalJSON renders a free-form value in the form a checkpoint round-trip
// produces (generic values, sorted keys). Typed values — a []ChainSegment in
// params, say — otherwise marshal in Go field order in memory and in sorted key
// order after decoding, which would make the plan identity depend on transport.
func canonicalJSON(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return string(raw)
	}
	canonical, err := json.Marshal(generic)
	if err != nil {
		return string(raw)
	}
	return string(canonical)
}

// cloneOverlayPlan returns a copy the caller may normalize without touching the
// argument. Slices and maps are copied; strings are immutable, and every
// producer normalization step REPLACES a field instead of mutating through a
// shared pointer, so a shallow copy of the remaining scalars is sufficient.
func cloneOverlayPlan(plan *capmodel.OverlayPlan) *capmodel.OverlayPlan {
	if plan == nil {
		return nil
	}
	frozen := *plan
	frozen.Items = append([]capmodel.OverlayItem(nil), plan.Items...)
	for i := range frozen.Items {
		source := plan.Items[i]
		if source.AssetRefs != nil {
			frozen.Items[i].AssetRefs = append([]capmodel.OverlayAssetRef(nil), source.AssetRefs...)
		}
		if source.ImageLayers != nil {
			frozen.Items[i].ImageLayers = append([]capmodel.OverlayImageLayer(nil), source.ImageLayers...)
		}
		if source.Params != nil {
			params := make(map[string]any, len(source.Params))
			for key, value := range source.Params {
				params[key] = value
			}
			frozen.Items[i].Params = params
		}
	}
	return &frozen
}

// FrozenFinalJobOverlayPlan returns a new plan with the final-job placement
// policy applied and every affected RenderKey recomputed. It NEVER mutates the
// argument (intervention A: "a function that returns a new validated plan").
//
// finalJob=false returns a plain copy, so a non-final run binds to the identity
// of the plan it actually rendered without gaining placement rules it never had.
func FrozenFinalJobOverlayPlan(plan *capmodel.OverlayPlan, result *GenerateResult, finalJob bool) (*capmodel.OverlayPlan, error) {
	if plan == nil {
		return nil, nil
	}
	frozen := cloneOverlayPlan(plan)
	if finalJob {
		if err := applyFinalJobOverlayWindowPolicy(frozen, result); err != nil {
			return nil, err
		}
	}
	// Recompute only the keys the policy reset. Recomputing every key would
	// churn identities for items the policy never touched.
	for i := range frozen.Items {
		if strings.TrimSpace(frozen.Items[i].RenderKey) == "" {
			frozen.Items[i].RenderKey = capmodel.ComputeRenderKey(*frozen, frozen.Items[i])
		}
	}
	// The policy changes placement (and may reset motion), and RenderKey is
	// excluded from FingerprintValue. The old fingerprint would no longer
	// describe this document, so Validate would reject the frozen plan and the
	// semantic bundle would carry a stale identity. Recompute it here, after
	// every field change and every RenderKey reset.
	frozen.Fingerprint = frozen.FingerprintValue()
	return frozen, nil
}

// FreezeFinalJobOverlayPlans replaces the result's plans with their frozen
// versions. It is the ONLY place that normalizes: the render path no longer
// touches the plan, so the persisted plan, the renderer input and the manifest
// all describe the same document.
//
// Idempotent by construction: every placement rule computes an absolute window
// and the 3D motion rule assigns a fixed preset, so re-running it (a resumed
// run, or the plan branch followed by the direct render path) reproduces the
// same plan and therefore the same digest.
func FreezeFinalJobOverlayPlans(result *GenerateResult, finalJob bool) error {
	if result == nil {
		return nil
	}
	if result.OverlayPlan != nil {
		frozen, err := FrozenFinalJobOverlayPlan(result.OverlayPlan, result, finalJob)
		if err != nil {
			return err
		}
		result.OverlayPlan = frozen
	}
	for language, plan := range result.LocalizedOverlayPlans {
		if plan == nil {
			continue
		}
		frozen, err := FrozenFinalJobOverlayPlan(plan, result, finalJob)
		if err != nil {
			return fmt.Errorf("%s overlay plan: %w", language, err)
		}
		result.LocalizedOverlayPlans[language] = frozen
	}
	return nil
}

// overlayRenderBindsPlan reports whether a persisted reference provably belongs
// to this plan version. It is the single reuse predicate.
func overlayRenderBindsPlan(plan *capmodel.OverlayPlan, ref *RenderReference) bool {
	return ValidateOverlayPlanRenderBinding(plan, ref) == nil
}

// ValidateOverlayPlanRenderBinding is the shared identity gate for cache,
// resume and handoff (intervention C). It answers ONE question: does this
// render represent exactly this frozen plan?
//
// A reference with a digest must match it. A reference without one (a
// checkpoint written before digest binding existed) is only reused when its
// full content still certifies the current plan; otherwise it is reported as
// incompatible so the caller re-renders the overlay instead of shipping the
// wrong video.
//
// It never mutates either argument and never repairs silently: repair belongs to
// RepairFinalJobOverlayCheckpoint, which archives the incompatible reference.
func ValidateOverlayPlanRenderBinding(plan *capmodel.OverlayPlan, ref *RenderReference) error {
	if ref == nil {
		return nil
	}
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}
	if strings.TrimSpace(ref.PlanDigest) == "" {
		// No identity stamp (a checkpoint written before digest binding): the
		// only proof available is the content itself, so require full coverage.
		if err := validateOverlayRenderCoverage(plan, ref); err != nil {
			return fmt.Errorf("overlay render carries no plan digest and does not certify the current plan: %w", err)
		}
		return nil
	}
	if err := validateOverlayPlanRenderDigest(plan, ref); err != nil {
		return err
	}
	// A matching digest IS the proof: it is a hash over the exact item set,
	// content, timing, motion and asset identities this plan declares. Adding a
	// coverage requirement on top would re-derive the same fact from weaker data
	// and would reject legitimate receipts that carry no manifest (the per-item
	// and batch lanes, and a resumed run whose reference was stamped by an
	// earlier attempt of the same binary).
	return nil
}

// validateOverlayPlanRenderDigest is the strict identity half of the gate: it
// reports only whether the reference names THIS plan version. Callers that
// already fail closed on missing coverage (the final-job handoff and the phrase
// clip) use it so a legacy, digest-less reference keeps its precise downstream
// rejection instead of being re-reported as an identity failure.
func validateOverlayPlanRenderDigest(plan *capmodel.OverlayPlan, ref *RenderReference) error {
	if ref == nil {
		return nil
	}
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}
	want := OverlayPlanRenderDigest(plan)
	if want == "" {
		return fmt.Errorf("overlay plan has no certifiable render identity")
	}
	got := strings.ToLower(strings.TrimSpace(ref.PlanDigest))
	if got == "" {
		return fmt.Errorf("overlay render carries no plan digest")
	}
	if got != want {
		return fmt.Errorf("overlay render was produced from plan %s but the current plan is %s", got, want)
	}
	// A matching digest IS the proof: it is a hash over the exact item set,
	// content, timing, motion and asset identities this plan declares. Adding a
	// coverage requirement on top would re-derive the same fact from weaker data
	// and would reject legitimate receipts that carry no manifest (the per-item
	// and batch lanes, and a resumed run whose reference was stamped by an
	// earlier attempt of the same binary).
	return nil
}

// overlayRenderManifestEntries projects the reference's manifest entries, or
// nil when the reference was produced by the per-item / batch paths.
func overlayRenderManifestEntries(ref *RenderReference) []capmodel.OverlayManifestEntry {
	if ref == nil || ref.Manifest == nil {
		return nil
	}
	return ref.Manifest.Entries
}

// validateOverlayRenderCoverage checks the SEMANTIC coverage of the render
// against the plan, independent of the digest: every renderable plan item must
// be represented by the render, and every manifested item must exist in the
// plan.
//
// The second half is the Kaufmann class: renders carried items the plan no
// longer had, and an ID present in one surface but not the other is proof that
// the two documents diverged.
func validateOverlayRenderCoverage(plan *capmodel.OverlayPlan, ref *RenderReference) error {
	if plan == nil || ref == nil {
		return nil
	}
	manifest := overlayRenderManifestEntries(ref)
	rendered := make(map[string]struct{}, len(ref.Items)+len(manifest))
	for _, item := range ref.Items {
		if id := strings.TrimSpace(item.ItemID); id != "" {
			rendered[id] = struct{}{}
		}
	}
	for _, batch := range ref.Batches {
		for _, id := range batch.ItemIDs {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				rendered[trimmed] = struct{}{}
			}
		}
	}
	byID := make(map[string]capmodel.OverlayItem, len(plan.Items))
	for _, item := range plan.Items {
		byID[strings.TrimSpace(item.ID)] = item
	}
	manifestIDs := make(map[string]struct{}, len(manifest))
	for _, entry := range manifest {
		id := strings.TrimSpace(entry.ItemID)
		if id == "" {
			return fmt.Errorf("overlay manifest has an entry without an item id")
		}
		manifestIDs[id] = struct{}{}
		// Same ID is not the same overlay: a render produced from a previous
		// plan version keeps the IDs and serves the OLD window. The manifest
		// carries the plan-absolute window, so it is the proof the two documents
		// describe the same placement and not merely the same item names.
		if item, ok := byID[id]; ok {
			if want := item.StartUSValue(); entry.InUS != want {
				return fmt.Errorf("overlay manifest entry %q starts at %d us but the plan starts at %d us", id, entry.InUS, want)
			}
			if want := item.EndUSValue(); entry.OutUS != want {
				return fmt.Errorf("overlay manifest entry %q ends at %d us but the plan ends at %d us", id, entry.OutUS, want)
			}
		}
	}
	// A manifest entry that no plan item owns is a stranger: the render
	// describes content this plan does not contain.
	if len(manifest) > 0 {
		strangers := make([]string, 0)
		for id := range manifestIDs {
			if _, ok := byID[id]; ok {
				continue
			}
			if _, ok := finalJobManifestYearItem(capmodel.OverlayManifestEntry{ItemID: id}, plan.Items); ok {
				continue
			}
			strangers = append(strangers, id)
		}
		if len(strangers) > 0 {
			sort.Strings(strangers)
			return fmt.Errorf("overlay render places %d item(s) the plan does not contain: %s", len(strangers), strings.Join(strangers, ","))
		}
	}
	missing := make([]string, 0)
	for _, item := range plan.Items {
		if isOverlayBackgroundTemplate(item.TemplateID) {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return fmt.Errorf("overlay plan has an item without an ID")
		}
		if _, ok := rendered[id]; ok {
			continue
		}
		if _, ok := manifestIDs[id]; ok {
			continue
		}
		if _, ok := finalJobManifestYearItem(capmodel.OverlayManifestEntry{ItemID: id}, plan.Items); ok {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("overlay render does not cover %d plan item(s): %s", len(missing), strings.Join(missing, ","))
	}
	return nil
}
