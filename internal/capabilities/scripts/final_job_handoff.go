// Package scriptgeneration — final_job_handoff.go: the certified rendered-clip
// handoff identities (video name + run suffix), runtime/audio asset appends
// and Drive link/id helpers for the final job payload.
//
// Split out of final_job_payload.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package scriptgeneration

import (
	"fmt"
	"net/url"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// ── Certified rendered-clip handoff ───────────────────────────────────
//
// finalJobVideoName derives the Master submission identity from the run, never
// from the caller's title/project. Two runs of the same topic share a title
// (OutputName defaults to the title), and an identical video_name made the
// second submission collide on the Master's replace_overlap guard (HTTP 422).
// A run-scoped suffix keeps the readable title prefix while guaranteeing the
// identity is unique per run and stable across retries of the same run.
func finalJobVideoName(req GenerateRequest, runID string) string {
	base := firstFinalJobValue(req.OutputName, req.Title, "final-job")
	suffix := finalJobIdentitySuffix(runID)
	if suffix == "" {
		return base
	}
	return base + " [" + suffix + "]"
}

// finalJobIdentitySuffix reduces the run id to a deterministic, filesystem- and
// URL-safe token. It keeps the trailing segment (the run's unique uuid tail)
// and drops anything that would need escaping in an identity field.
func finalJobIdentitySuffix(runID string) string {
	id := strings.TrimSpace(runID)
	if id == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if len(suffix) > 16 {
		suffix = suffix[len(suffix)-16:]
	}
	return suffix
}

func firstFinalJobValue(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return "final-job"
}

func appendFinalJobRuntimeAsset(dst *[]any, seen map[string]bool, role string, ref map[string]any) {
	if ref == nil {
		return
	}
	id := strings.TrimSpace(fmt.Sprint(ref["drive_file_id"]))
	if id == "" {
		id = strings.TrimSpace(fmt.Sprint(ref["asset_id"]))
	}
	if id == "" || seen[role+":"+id] {
		return
	}
	seen[role+":"+id] = true
	item := make(map[string]any, len(ref)+1)
	for key, value := range ref {
		// Keep worker-local cache paths out of the remote FINALIZE contract.
		if key == "local_path" {
			continue
		}
		item[key] = value
	}
	if driveID := strings.TrimSpace(fmt.Sprint(ref["drive_file_id"])); driveID != "" {
		item["url"] = driveFileWebLink(driveID)
	}
	item["role"] = role
	*dst = append(*dst, item)
}

func finalJobAudioAsset(audio FinalAudioReference) (map[string]any, error) {
	driveID := driveFileIDFromLink(audio.DriveLink)
	if driveID == "" || len(strings.TrimSpace(audio.FinalAudioSHA256)) != 64 || audio.SizeBytes <= 0 || audio.DurationMS <= 0 || !audio.FinalMix || !audio.CopyEligible {
		return nil, fmt.Errorf("final_job requires a published, certified FINAL_AUDIO_COPY mix with Drive file ID, SHA-256, size, and duration")
	}
	if !strings.EqualFold(strings.TrimSpace(audio.Codec), "aac") || !strings.EqualFold(strings.TrimSpace(audio.Profile), "LC") || audio.SampleRate != 48000 || audio.Channels != 2 || !strings.EqualFold(strings.TrimSpace(audio.ChannelLayout), "stereo") {
		return nil, fmt.Errorf("final_job final audio is not canonical AAC-LC 48 kHz stereo")
	}
	return map[string]any{
		"asset_id": audio.AssetID, "drive_file_id": driveID,
		"url": driveFileWebLink(driveID), "sha256": strings.TrimSpace(audio.FinalAudioSHA256),
		"size_bytes": audio.SizeBytes, "duration_ms": audio.DurationMS,
		"codec": audio.Codec, "profile": audio.Profile, "sample_rate": audio.SampleRate,
		"channels": audio.Channels, "channel_layout": audio.ChannelLayout,
		"strategy": "FINAL_AUDIO_COPY", "copy_eligible": audio.CopyEligible,
		"audio_contract_version": audio.AudioContractVersion, "audio_plan_version": audio.AudioPlanVersion,
		"audio_plan_sha256": audio.PlanSHA256, "final_mix": audio.FinalMix,
	}, nil
}

func driveFileWebLink(driveID string) string {
	// The Master resolver accepts the canonical deferred-Drive locator, which
	// carries the exact Drive file ID without making the 77 fetch the asset.
	return "velox-drive://" + strings.TrimSpace(driveID)
}

func driveFileIDFromLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if part == "d" && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
		if part == "file" && i+2 < len(parts) && parts[i+1] == "d" && parts[i+2] != "" {
			return parts[i+2]
		}
	}
	return ""
}

// ── merged from final_job_overlay_batches.go ─────────────────────────────────────

type finalJobOverlayEntry struct {
	item      capoverlay.OverlayItem
	renderID  string
	planIndex int
}

// finalJobOverlayEntries projects per-item and fused references into the
// single replace-window list consumed by the remote master. A batch is one
// replace artifact; its member IDs remain available in the persisted render
// reference and are never emitted as duplicate replacements.
func finalJobOverlayEntries(result *GenerateResult, consumed map[string]struct{}) ([]finalJobOverlayEntry, map[string]RenderArtifact, error) {
	if result == nil || result.OverlayPlan == nil || result.OverlayRender == nil {
		return nil, nil, fmt.Errorf("final_job overlay references are unavailable")
	}
	itemsByID := make(map[string]capoverlay.OverlayItem, len(result.OverlayPlan.Items))
	indexByID := make(map[string]int, len(result.OverlayPlan.Items))
	for i, item := range result.OverlayPlan.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, nil, fmt.Errorf("final_job overlay plan has an item without an ID")
		}
		itemsByID[id], indexByID[id] = item, i
	}
	artifacts := make(map[string]RenderArtifact, len(result.OverlayRender.Items)+len(result.OverlayRender.Batches))
	for _, rendered := range result.OverlayRender.Items {
		if rendered.Artifact != nil && strings.TrimSpace(rendered.ItemID) != "" {
			artifacts[strings.TrimSpace(rendered.ItemID)] = *rendered.Artifact
		}
	}
	itemReferences := make(map[string]OverlayItemRenderReference, len(result.OverlayRender.Items))
	for _, rendered := range result.OverlayRender.Items {
		id := strings.TrimSpace(rendered.ItemID)
		if id == "" {
			continue
		}
		if _, duplicate := itemReferences[id]; duplicate {
			return nil, nil, fmt.Errorf("final_job has duplicate overlay render reference %q", id)
		}
		itemReferences[id] = rendered
	}
	batchByItem := make(map[string]OverlayBatchRenderReference)
	for _, batch := range result.OverlayRender.Batches {
		batchID := strings.TrimSpace(batch.BatchID)
		if batchID == "" || strings.TrimSpace(batch.SceneID) == "" || len(batch.ItemIDs) < 2 ||
			batch.EndUS <= batch.StartUS || batch.Artifact == nil ||
			strings.TrimSpace(batch.JobID) == "" || batch.Status != RenderQueueStateCompleted {
			return nil, nil, fmt.Errorf("final_job has an incomplete fused overlay batch %q", batchID)
		}
		if _, exists := artifacts[batchID]; exists {
			return nil, nil, fmt.Errorf("final_job has duplicate render identity %q", batchID)
		}
		artifacts[batchID] = *batch.Artifact
		work := overlayBatchWork{id: batchID, sceneID: batch.SceneID, startUS: batch.StartUS, endUS: batch.EndUS}
		lastPlanIndex := -1
		for _, rawID := range batch.ItemIDs {
			id := strings.TrimSpace(rawID)
			item, exists := itemsByID[id]
			if !exists {
				return nil, nil, fmt.Errorf("final_job batch %q references unknown overlay %q", batchID, id)
			}
			if item.SceneID != batch.SceneID {
				return nil, nil, fmt.Errorf("final_job batch %q crosses scene boundary at overlay %q", batchID, id)
			}
			planIndex := indexByID[id]
			if planIndex <= lastPlanIndex {
				return nil, nil, fmt.Errorf("final_job batch %q members are not in parent-plan order", batchID)
			}
			lastPlanIndex = planIndex
			candidate := overlayItemCandidate{index: planIndex, source: item}
			itemStartUS, itemEndUS := overlayCandidateRenderWindow(*result.OverlayPlan, candidate)
			if itemStartUS < batch.StartUS || itemEndUS > batch.EndUS {
				return nil, nil, fmt.Errorf("final_job batch %q window [%d,%d) does not cover member %q window [%d,%d)", batchID, batch.StartUS, batch.EndUS, id, itemStartUS, itemEndUS)
			}
			work.candidates = append(work.candidates, candidate)
			if _, duplicate := batchByItem[id]; duplicate {
				return nil, nil, fmt.Errorf("final_job overlay %q belongs to multiple batches", id)
			}
			batchByItem[id] = batch
		}
		for _, memberID := range batch.ItemIDs {
			id := strings.TrimSpace(memberID)
			rendered, exists := itemReferences[id]
			if !exists {
				return nil, nil, fmt.Errorf("final_job batch %q has no semantic render reference for member %q", batchID, id)
			}
			if strings.TrimSpace(rendered.BatchID) != batchID {
				return nil, nil, fmt.Errorf("final_job member %q references batch %q, want %q", id, strings.TrimSpace(rendered.BatchID), batchID)
			}
			if rendered.Artifact != nil {
				return nil, nil, fmt.Errorf("final_job batch member %q also has an individual artifact", id)
			}
			if strings.TrimSpace(rendered.JobID) != strings.TrimSpace(batch.JobID) {
				return nil, nil, fmt.Errorf("final_job batch member %q job %q does not match batch job %q", id, strings.TrimSpace(rendered.JobID), strings.TrimSpace(batch.JobID))
			}
			if rendered.Status != batch.Status {
				return nil, nil, fmt.Errorf("final_job batch member %q status %q does not match batch status %q", id, rendered.Status, batch.Status)
			}
		}
		if expectedID := overlayBatchIdentity(*result.OverlayPlan, work); expectedID != batchID {
			return nil, nil, fmt.Errorf("final_job batch identity %q does not match its parent plan membership/timing", batchID)
		}
	}
	for itemID, rendered := range itemReferences {
		batchID := strings.TrimSpace(rendered.BatchID)
		if batchID == "" {
			continue
		}
		batch, exists := batchByItem[itemID]
		if !exists || strings.TrimSpace(batch.BatchID) != batchID {
			return nil, nil, fmt.Errorf("final_job overlay reference %q points to unknown batch %q", itemID, batchID)
		}
	}

	entries := make([]finalJobOverlayEntry, 0, len(itemsByID))
	seenBatch := make(map[string]struct{}, len(result.OverlayRender.Batches))
	for i, item := range result.OverlayPlan.Items {
		id := strings.TrimSpace(item.ID)
		batch, batched := batchByItem[id]
		if !batched {
			if _, skip := consumed[id]; !skip {
				entries = append(entries, finalJobOverlayEntry{item: item, renderID: id, planIndex: i})
			}
			continue
		}
		batchID := strings.TrimSpace(batch.BatchID)
		if _, done := seenBatch[batchID]; done {
			continue
		}
		seenBatch[batchID] = struct{}{}
		consumedCount := 0
		firstIndex := i
		for _, member := range batch.ItemIDs {
			memberID := strings.TrimSpace(member)
			if _, skip := consumed[memberID]; skip {
				consumedCount++
			}
			if memberIndex, ok := indexByID[memberID]; ok && memberIndex < firstIndex {
				firstIndex = memberIndex
			}
		}
		if consumedCount > 0 {
			// The phrase-only fallback uses the whole certified batch artifact as
			// the scene clip. In that case every member is already present in the
			// clip, so none may also be handed to the Master as a replace overlay.
			phraseConsumed := false
			for _, member := range batch.ItemIDs {
				memberID := strings.TrimSpace(member)
				if _, skip := consumed[memberID]; !skip {
					continue
				}
				memberItem := itemsByID[memberID]
				if memberItem.Kind == "text_phrase" && memberItem.SceneID == batch.SceneID {
					phraseConsumed = true
				}
			}
			if !phraseConsumed {
				return nil, nil, fmt.Errorf("final_job cannot partially consume overlay batch %q", batchID)
			}
			continue
		}
		startMS, endMS := batch.StartUS/1000, (batch.EndUS+999)/1000
		composite := capoverlay.OverlayItem{
			ID: batchID, SceneID: batch.SceneID, Kind: "composite_batch",
			TemplateID: "COMPOSITE_BATCH", StartUS: batch.StartUS,
			DurationUS: batch.EndUS - batch.StartUS,
			StartMs:    startMS, EndMs: endMS,
		}
		entries = append(entries, finalJobOverlayEntry{item: composite, renderID: batchID, planIndex: firstIndex})
	}
	return entries, artifacts, nil
}

func rejectSplitChartGroupsUnlessBatched(items []capoverlay.OverlayItem, batches []OverlayBatchRenderReference) error {
	batchByItem := make(map[string]string)
	for _, batch := range batches {
		for _, itemID := range batch.ItemIDs {
			batchByItem[strings.TrimSpace(itemID)] = strings.TrimSpace(batch.BatchID)
		}
	}
	groupByID := make(map[string][]string)
	for _, item := range items {
		if item.Params == nil || item.Params["chart_card"] != true && item.Params["chart_bar"] != true {
			continue
		}
		group, _ := item.Params["chart_group_id"].(string)
		group = strings.TrimSpace(group)
		if group == "" {
			return fmt.Errorf("final_job chart item %q has no chart_group_id; charts must be composable as one artifact", item.ID)
		}
		groupByID[group] = append(groupByID[group], batchByItem[strings.TrimSpace(item.ID)])
	}
	for group, members := range groupByID {
		if len(members) <= 1 {
			continue
		}
		batchID := members[0]
		if batchID == "" {
			return fmt.Errorf("final_job chart group %q has %d replace overlays and is not fused into one certified artifact", group, len(members))
		}
		for _, other := range members[1:] {
			if other != batchID {
				return fmt.Errorf("final_job chart group %q spans multiple render batches", group)
			}
		}
	}
	return nil
}
