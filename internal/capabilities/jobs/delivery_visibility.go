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

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// RemoteFinalJobRequested is the operator-facing state of a job that
// flagged final_job but has no durable remote_final_job receipt yet.
const RemoteFinalJobRequested = "requested"

// DeliveryVisibility is the flattened routing projection surfaced on
// enqueue responses, job snapshots and the queue view.
type DeliveryVisibility struct {
	ChannelID            string `json:"channel_id,omitempty"`
	FinalJob             bool   `json:"final_job"`
	DriveFolderID        string `json:"drive_folder_id,omitempty"`
	DriveSubfolderName   string `json:"drive_subfolder_name,omitempty"`
	RemoteFinalJobID     string `json:"remote_final_job_id,omitempty"`
	RemoteFinalJobStatus string `json:"remote_final_job_status,omitempty"`
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
		}
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
	}
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
