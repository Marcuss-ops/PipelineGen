// Package jobs (test) — delivery_visibility_test.go pins the read-side
// routing projection (Batch Brasil visibility item): the extraction is
// grounded in the REAL payload/result shapes observed in the production
// jobs DB (items[].channel_id, root final_job,
// items[].output.render.drive_folder_id / drive_subfolder_name,
// remote_final_job receipt under {"output": {...}}).
package jobs

import (
	"encoding/json"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// realShapePayload mirrors the observed production generate payload:
// root final_job, per-item channel_id, output.render drive destination.
const realShapePayload = `{
  "version": 2,
  "preset": "custom",
  "final_job": true,
  "items": [
    {
      "id": "milton-final",
      "channel_id": "crime",
      "output": {
        "render": {
          "drive_folder_id": "1AbC_folderId",
          "drive_subfolder_name": "Milton final job"
        }
      }
    }
  ]
}`

const receiptResult = `{
  "output": {
    "remote_final_job": {
      "job_id": "job_remote_42",
      "status": "SUCCEEDED",
      "worker_id": "worker-51",
      "sha256": "deadbeef"
    }
  }
}`

func TestExtractDeliveryVisibility_RealPayloadShape(t *testing.T) {
	t.Parallel()

	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), nil)
	if vis.ChannelID != "crime" {
		t.Fatalf("ChannelID = %q, want %q (items[].channel_id)", vis.ChannelID, "crime")
	}
	if !vis.FinalJob {
		t.Fatalf("FinalJob = false, want true (root final_job flag)")
	}
	if vis.DriveFolderID != "1AbC_folderId" {
		t.Fatalf("DriveFolderID = %q, want %q (output.render.drive_folder_id)", vis.DriveFolderID, "1AbC_folderId")
	}
	if vis.DriveSubfolderName != "Milton final job" {
		t.Fatalf("DriveSubfolderName = %q, want %q", vis.DriveSubfolderName, "Milton final job")
	}
	if vis.RemoteFinalJobStatus != RemoteFinalJobRequested {
		t.Fatalf("RemoteFinalJobStatus = %q, want %q (flagged, no receipt yet)",
			vis.RemoteFinalJobStatus, RemoteFinalJobRequested)
	}
	if vis.RemoteFinalJobID != "" {
		t.Fatalf("RemoteFinalJobID = %q, want empty (no receipt yet — never invented)", vis.RemoteFinalJobID)
	}
}

func TestExtractDeliveryVisibility_ReceiptUnderOutputWrapper(t *testing.T) {
	t.Parallel()

	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), json.RawMessage(receiptResult))
	if vis.RemoteFinalJobID != "job_remote_42" {
		t.Fatalf("RemoteFinalJobID = %q, want %q", vis.RemoteFinalJobID, "job_remote_42")
	}
	if vis.RemoteFinalJobStatus != "SUCCEEDED" {
		t.Fatalf("RemoteFinalJobStatus = %q, want %q (receipt status wins over the requested placeholder)",
			vis.RemoteFinalJobStatus, "SUCCEEDED")
	}
}

func TestExtractDeliveryVisibility_ReceiptAtResultRootAndHistoryFallback(t *testing.T) {
	t.Parallel()

	rootReceipt := json.RawMessage(`{"remote_final_job":{"job_id":"job_root","status":"RUNNING"}}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), rootReceipt)
	if vis.RemoteFinalJobID != "job_root" || vis.RemoteFinalJobStatus != "RUNNING" {
		t.Fatalf("root receipt not read: %+v", vis)
	}

	history := json.RawMessage(`{"output":{"remote_final_job_history":[{"job_id":"job_old","status":"FAILED"},{"job_id":"job_new","status":"SUCCEEDED"}]}}`)
	vis = ExtractDeliveryVisibility(json.RawMessage(realShapePayload), history)
	if vis.RemoteFinalJobID != "job_new" || vis.RemoteFinalJobStatus != "SUCCEEDED" {
		t.Fatalf("last terminal history receipt not read: %+v", vis)
	}
}

func TestExtractDeliveryVisibility_NoFabricationOnEmptyInput(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{``, `null`, `not-json`, `{}`, `{"items":[]}`} {
		vis := ExtractDeliveryVisibility(json.RawMessage(payload), nil)
		if vis != (DeliveryVisibility{}) {
			t.Fatalf("payload %q: unexpected non-zero projection %+v", payload, vis)
		}
	}
}

func TestExtractDeliveryVisibility_ItemFinalJobFlag(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{"items":[{"channel_id":"br","final_job":true}]}`)
	vis := ExtractDeliveryVisibility(payload, nil)
	if !vis.FinalJob {
		t.Fatalf("FinalJob = false, want true (items[].final_job flag)")
	}
	if vis.ChannelID != "br" {
		t.Fatalf("ChannelID = %q, want %q", vis.ChannelID, "br")
	}
}

func TestJobDeliveryFieldsStableKeySet(t *testing.T) {
	t.Parallel()

	fields := (DeliveryVisibility{}).deliveryFields()
	want := []string{
		"channel_id", "final_job", "drive_folder_id", "drive_subfolder_name",
		"remote_final_job_id", "remote_final_job_status",
	}
	if len(fields) != len(want) {
		t.Fatalf("deliveryFields key count = %d, want %d: %v", len(fields), len(want), fields)
	}
	for _, k := range want {
		if _, ok := fields[k]; !ok {
			t.Fatalf("deliveryFields missing stable key %q", k)
		}
	}
}

func TestJobQueueViewKeepsJobKeysAndAddsDeliveryFields(t *testing.T) {
	t.Parallel()

	j := &job.Job{
		ID:      "job-1",
		Type:    "script.generate",
		Status:  job.StatusQueued,
		Payload: json.RawMessage(realShapePayload),
	}
	view := jobQueueView(j)
	if view["id"] != "job-1" || view["type"] != "script.generate" {
		t.Fatalf("canonical job keys lost: id=%v type=%v", view["id"], view["type"])
	}
	if view["channel_id"] != "crime" {
		t.Fatalf("channel_id not surfaced in queue view: %v", view["channel_id"])
	}
	if view["final_job"] != true {
		t.Fatalf("final_job not surfaced in queue view: %v", view["final_job"])
	}
	if view["drive_subfolder_name"] != "Milton final job" {
		t.Fatalf("drive_subfolder_name not surfaced in queue view: %v", view["drive_subfolder_name"])
	}
}
