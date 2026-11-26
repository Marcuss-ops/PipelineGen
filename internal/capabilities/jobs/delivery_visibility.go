// Package jobs — delivery_visibility.go projects the delivery/routing
// facts carried by a job's persisted payload and result into explicit
// response fields (Batch Brasil visibility item, docs/stato/refactored.md
// §6):
//
//   - channel_id            — items[].channel_id (payload root fallback)
//   - final_job             — root/items final_job flag
//   - drive_folder_id       — items[].output.render.drive_folder_id
//   - drive_subfolder_name  — items[].output.render.drive_subfolder_name
//   - remote_final_job_id   — remote_final_job receipt job_id (result)
//   - remote_final_job_status — receipt status, or "requested" while the
//     handoff is flagged but no receipt exists yet
//
// Before this projection those facts were readable ONLY by digging into
// the raw payload/result JSON — the enqueue response and the queue view
// never surfaced them. Extraction is strictly read-side: no schema
// change, no new persisted state, and no value is ever invented (empty
// stays empty; "requested" is only derived from the explicit final_job
// flag, never from inference).
package jobs

import (
	"encoding/json"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// RemoteFinalJobRequested is the operator-facing state of a job that
// flagged final_job but has no durable remote_final_job receipt yet.
const RemoteFinalJobRequested = "requested"

// Render/delivery states. "Rendered" means the deliverable bytes exist and
// carry a certified locator (the object store); it never implies that Drive
// has a copy. Delivery is a separate, asynchronous fact: it stays "pending"
// until the outbox consumer confirms a drive_file_id, and only then becomes
// "published". A view must never report delivered from a render alone.
const (
	RenderStatusRendered      = "rendered"
	DrivePublicationPending   = "pending"
	DrivePublicationPublished = "published"
)

// DeliveryVisibility is the flattened routing projection surfaced on
// enqueue responses, job snapshots and the queue view.
type DeliveryVisibility struct {
	ChannelID            string `json:"channel_id,omitempty"`
	FinalJob             bool   `json:"final_job"`
	DriveFolderID        string `json:"drive_folder_id,omitempty"`
	DriveSubfolderName   string `json:"drive_subfolder_name,omitempty"`
	RemoteFinalJobID     string `json:"remote_final_job_id,omitempty"`
	RemoteFinalJobStatus string `json:"remote_final_job_status,omitempty"`
	// RemoteFinalJobTiming is the Master's measured breakdown (queue,
	// execution, handoff, wait, poll lag, artifact download) copied verbatim
	// from the durable receipt. It is surfaced on /full so operators do not
	// have to dig into result.remote_final_job.timing.
	RemoteFinalJobTiming map[string]any `json:"remote_final_job_timing,omitempty"`

	// RenderStatus answers "does the deliverable exist?": "rendered" when a
	// certified render locator is present, empty when the result proves no
	// render (never invented).
	RenderStatus string `json:"render_status,omitempty"`
	// DrivePublication answers "has delivery completed?": "pending" while the
	// asynchronous outbox has no confirmed file id, "published" once it has.
	DrivePublication string `json:"drive_publication,omitempty"`
	// DriveFileID is the confirmed Drive identity of the final deliverable.
	// Empty until the delivery outbox reports it; never a placeholder.
	DriveFileID string `json:"drive_file_id,omitempty"`
}

// ExtractDeliveryVisibility derives the routing projection from a job's
// persisted payload and result JSON. Tolerant by design: unknown or
// malformed JSON yields zero values, never an error — the visibility
// projection must never break a status read.
func ExtractDeliveryVisibility(payload, result json.RawMessage) DeliveryVisibility {
	var vis DeliveryVisibility

	var root map[string]any
	if err := json.Unmarshal(payload, &root); err == nil {
		vis.FinalJob = boolAt(root, "final_job")
		vis.ChannelID = stringAt(root, "channel_id")
		if items, ok := root["items"].([]any); ok {
			for _, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if vis.ChannelID == "" {
					vis.ChannelID = stringAt(item, "channel_id")
				}
				if boolAt(item, "final_job") {
					vis.FinalJob = true
				}
				out, ok := item["output"].(map[string]any)
				if !ok {
					continue
				}
				if vis.DriveFolderID == "" {
					vis.DriveFolderID = stringAt(out, "drive_folder_id")
				}
				if vis.DriveSubfolderName == "" {
					vis.DriveSubfolderName = stringAt(out, "drive_subfolder_name")
				}
				if render, ok := out["render"].(map[string]any); ok {
					if vis.DriveFolderID == "" {
						vis.DriveFolderID = stringAt(render, "drive_folder_id")
					}
					if vis.DriveSubfolderName == "" {
						vis.DriveSubfolderName = stringAt(render, "drive_subfolder_name")
					}
				}
			}
		}
	}

	var res map[string]any
	if err := json.Unmarshal(result, &res); err == nil {
		if receipt, ok := remoteFinalJobReceipt(res); ok {
			vis.RemoteFinalJobID = stringAt(receipt, "job_id")
			vis.RemoteFinalJobStatus = stringAt(receipt, "status")
			if timing, ok := receipt["timing"].(map[string]any); ok {
				vis.RemoteFinalJobTiming = timing
			}
		}
		projectRenderDelivery(&vis, res)
	}
	if vis.FinalJob && vis.RemoteFinalJobStatus == "" {
		// Honest request-time state: the handoff was explicitly flagged
		// and no durable receipt exists yet.
		vis.RemoteFinalJobStatus = RemoteFinalJobRequested
	}
	return vis
}

// JobDeliveryVisibility is the convenience accessor for hydrated Job
// rows (Get / List / GetFull hydrate payload + result).
func JobDeliveryVisibility(j *job.Job) DeliveryVisibility {
	if j == nil {
		return DeliveryVisibility{}
	}
	return ExtractDeliveryVisibility(j.Payload, j.Result)
}

// deliveryFields flattens the projection onto a response map with a
// STABLE key set (every key always present) so client parsing never
// depends on payload shape.
func (v DeliveryVisibility) deliveryFields() map[string]any {
	return map[string]any{
		"channel_id":              v.ChannelID,
		"final_job":               v.FinalJob,
		"drive_folder_id":         v.DriveFolderID,
		"drive_subfolder_name":    v.DriveSubfolderName,
		"remote_final_job_id":     v.RemoteFinalJobID,
		"remote_final_job_status": v.RemoteFinalJobStatus,
		"remote_final_job_timing": v.RemoteFinalJobTiming,
		"render_status":           v.RenderStatus,
		"drive_publication":       v.DrivePublication,
		"drive_file_id":           v.DriveFileID,
	}
}

// projectRenderDelivery separates the render from the delivery. The render
// side is read from the durable result (remote receipt, local final video or
// a certified overlay artifact). The delivery side is read from the
// drive_deliveries receipt the asynchronous outbox writes after a real
// upload. Both are evidence-only: an absent value stays absent instead of
// being inferred from the job status.
func projectRenderDelivery(vis *DeliveryVisibility, result map[string]any) {
	if vis == nil || result == nil {
		return
	}
	rendered := false
	driveID := ""
	for _, scope := range deliveryScopes(result) {
		if renderProvesDeliverable(scope, vis.FinalJob) {
			rendered = true
		}
		if driveID == "" {
			if id, _ := scopeDriveDelivery(scope, !vis.FinalJob); id != "" {
				driveID = id
			}
		}
	}
	if driveID != "" {
		// A confirmed upload is itself proof that certified bytes existed.
		rendered = true
	}
	if !rendered {
		return
	}
	vis.RenderStatus = RenderStatusRendered
	if driveID == "" {
		vis.DrivePublication = DrivePublicationPending
		return
	}
	vis.DrivePublication = DrivePublicationPublished
	vis.DriveFileID = driveID
}

// renderProvesDeliverable reports whether one result scope carries a
// certified deliverable locator. Status alone is not enough: a RUNNING
// receipt names no bytes. A final job's deliverable is the master video, so
// its overlay intermediates are not render evidence for the job; an ordinary
// overlay-only run is proven by its certified overlay artifact.
func renderProvesDeliverable(scope map[string]any, finalJob bool) bool {
	if receipt, ok := scope["remote_final_job"].(map[string]any); ok && receipt != nil {
		if strings.EqualFold(stringAt(receipt, "status"), "SUCCEEDED") && stringAt(receipt, "sha256") != "" {
			return true
		}
	}
	// The terminal script result projects the verified download as
	// remote_final_video (kernel/script domain shape); the live run checkpoint
	// still carries remote_final_job (capability contract shape). Both are
	// evidence, neither is delivery.
	if remote, ok := scope["remote_final_video"].(map[string]any); ok && remote != nil {
		if stringAt(remote, "sha256") != "" {
			return true
		}
	}
	if local, ok := scope["local_final_video"].(map[string]any); ok && local != nil {
		if stringAt(local, "SHA256") != "" || stringAt(local, "sha256") != "" {
			return true
		}
	}
	return !finalJob && certifiedOverlayArtifact(scope)
}

// certifiedOverlayArtifact recognizes the object-store locator of an overlay
// render (URL + digest). It is the render evidence for an overlay-only run.
func certifiedOverlayArtifact(scope map[string]any) bool {
	render, ok := scope["overlay_render"].(map[string]any)
	if !ok || render == nil {
		return false
	}
	if artifact, ok := render["artifact"].(map[string]any); ok && artifact != nil {
		if stringAt(artifact, "sha256") != "" && (stringAt(artifact, "url") != "" || stringAt(artifact, "storage_key") != "") {
			return true
		}
	}
	if items, ok := render["items"].([]any); ok {
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if artifact, ok := item["artifact"].(map[string]any); ok && artifact != nil {
				if stringAt(artifact, "sha256") != "" && (stringAt(artifact, "url") != "" || stringAt(artifact, "storage_key") != "") {
					return true
				}
			}
		}
	}
	return false
}

// scopeDriveDelivery reads one confirmed Drive identity from a result scope:
// the asynchronous receipt (drive_deliveries) first, then — only when the
// overlay links are the deliverable (ordinary runs) — the legacy overlay_links
// projection. A final job's delivery is its master video, never a sidecar.
func scopeDriveDelivery(scope map[string]any, allowOverlayLinks bool) (string, string) {
	anyID, anyLink := "", ""
	if deliveries, ok := scope["drive_deliveries"].(map[string]any); ok {
		for _, raw := range deliveries {
			delivery, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := stringAt(delivery, "drive_file_id")
			if id == "" {
				continue
			}
			// The final video is the deliverable the render status describes;
			// its receipt wins over any sidecar (script/audio) delivery.
			if stringAt(delivery, "kind") == "video" {
				return id, stringAt(delivery, "drive_link")
			}
			if anyID == "" {
				anyID, anyLink = id, stringAt(delivery, "drive_link")
			}
		}
	}
	if !allowOverlayLinks {
		// A final job is delivered only when the VIDEO receipt lands; a
		// script/audio sidecar upload must not mask a pending master.
		return "", ""
	}
	if anyID != "" {
		return anyID, anyLink
	}
	if links, ok := scope["overlay_links"].([]any); ok {
		for _, raw := range links {
			link, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if id := stringAt(link, "drive_file_id"); id != "" {
				return id, stringAt(link, "drive_link")
			}
		}
	}
	return "", ""
}

// deliveryScopes enumerates every result scope that may hold render/delivery
// evidence: the result root, its output/result wrappers and each envelope
// item's own result.
func deliveryScopes(root map[string]any) []map[string]any {
	scopes := resultScopes(root)
	items, _ := root["items"].([]any)
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if result, ok := item["result"].(map[string]any); ok && result != nil {
			scopes = append(scopes, result)
		}
	}
	return scopes
}

// jobQueueView projects one Job row as the queue-view element: the
// canonical job JSON keys (unchanged, additive-only wire change) plus
// the explicit delivery fields at top level.
func jobQueueView(j *job.Job) map[string]any {
	view := map[string]any{}
	if raw, err := json.Marshal(j); err == nil {
		_ = json.Unmarshal(raw, &view)
	}
	for k, v := range JobDeliveryVisibility(j).deliveryFields() {
		view[k] = v
	}
	return view
}

// remoteFinalJobReceipt locates the durable remote_final_job receipt in
// a result document. GenerateResult may sit at the result root or under
// the {"output": {...}} wrapper written by the durable run ledger; both
// scopes are checked, then the terminal-receipt history.
func remoteFinalJobReceipt(root map[string]any) (map[string]any, bool) {
	for _, scope := range resultScopes(root) {
		if receipt, ok := scope["remote_final_job"].(map[string]any); ok && receipt != nil {
			return receipt, true
		}
	}
	for _, scope := range resultScopes(root) {
		history, ok := scope["remote_final_job_history"].([]any)
		if !ok || len(history) == 0 {
			continue
		}
		if receipt, ok := history[len(history)-1].(map[string]any); ok && receipt != nil {
			return receipt, true
		}
	}
	return nil, false
}

func resultScopes(root map[string]any) []map[string]any {
	scopes := []map[string]any{root}
	for _, key := range []string{"output", "result"} {
		if nested, ok := root[key].(map[string]any); ok && nested != nil {
			scopes = append(scopes, nested)
		}
	}
	return scopes
}

func stringAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func boolAt(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}
