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
      "sha256": "deadbeef",
      "timing": {
        "queue_ms": 12000,
        "execution_ms": 240000,
        "wait_ms": 243000,
        "handoff_ms": 3500,
        "poll_lag_ms": 1800,
        "artifact_download_ms": 257400
      }
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
	if vis.RemoteFinalJobTiming["artifact_download_ms"] != float64(257400) {
		t.Fatalf("RemoteFinalJobTiming = %+v, want the receipt timing block verbatim", vis.RemoteFinalJobTiming)
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
		if vis.ChannelID != "" || vis.FinalJob || vis.DriveFolderID != "" ||
			vis.DriveSubfolderName != "" || vis.RemoteFinalJobID != "" ||
			vis.RemoteFinalJobStatus != "" || len(vis.RemoteFinalJobTiming) != 0 ||
			vis.RenderStatus != "" || vis.DrivePublication != "" || vis.DriveFileID != "" {
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
		"remote_final_job_id", "remote_final_job_status", "remote_final_job_timing",
		"render_status", "drive_publication", "drive_file_id",
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

// TestExtractDeliveryVisibility_RenderedIsNotDelivered pins the DEMOL-03
// split: a SUCCEEDED render receipt with a certified digest makes the job
// rendered, but with no Drive delivery receipt it stays drive_publication
// pending and exposes no file id.
func TestExtractDeliveryVisibility_RenderedIsNotDelivered(t *testing.T) {
	t.Parallel()

	result := json.RawMessage(`{
		"version": 2,
		"ok": true,
		"items": [{"item_id": "item-1", "result": {
			"remote_final_job": {"job_id": "master-1", "status": "SUCCEEDED", "sha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
			"overlay_render": {"job_id": "overlay-1", "status": "COMPLETED", "artifact": {
				"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"storage_key": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"url": "https://objects.example/objects/aaaa"
			}}
		}}],
		"summary": {"total": 1, "succeeded": 1, "failed": 0}
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), result)
	if vis.RenderStatus != RenderStatusRendered {
		t.Fatalf("RenderStatus = %q, want %q", vis.RenderStatus, RenderStatusRendered)
	}
	if vis.DrivePublication != DrivePublicationPending {
		t.Fatalf("DrivePublication = %q, want %q (no confirmed upload yet)", vis.DrivePublication, DrivePublicationPending)
	}
	if vis.DriveFileID != "" {
		t.Fatalf("DriveFileID = %q, want empty until the outbox confirms it", vis.DriveFileID)
	}
}

// TestExtractDeliveryVisibility_OverlayOnlyRunIsRendered pins the non-final
// lane: a certified overlay artifact is the deliverable, so it renders and
// still awaits its (optional) Drive copy.
func TestExtractDeliveryVisibility_OverlayOnlyRunIsRendered(t *testing.T) {
	t.Parallel()

	result := json.RawMessage(`{
		"result": {"overlay_render": {"job_id": "overlay-1", "status": "COMPLETED", "artifact": {
			"sha256": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			"storage_key": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			"url": "https://objects.example/objects/eeee"
		}}}
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(`{}`), result)
	if vis.RenderStatus != RenderStatusRendered || vis.DrivePublication != DrivePublicationPending {
		t.Fatalf("overlay-only projection = %q/%q, want rendered/pending", vis.RenderStatus, vis.DrivePublication)
	}
}

// TestExtractDeliveryVisibility_TerminalRemoteFinalVideoIsRendered pins the
// real terminal shape: the domain result projects the verified master as
// remote_final_video, not remote_final_job. It must still count as rendered
// and stay pending until the delivery receipt lands.
func TestExtractDeliveryVisibility_TerminalRemoteFinalVideoIsRendered(t *testing.T) {
	t.Parallel()

	result := json.RawMessage(`{
		"version": 2, "ok": true,
		"items": [{"item_id": "item-1", "result": {
			"remote_final_video": {"job_id": "master-1", "sha256": "9999999999999999999999999999999999999999999999999999999999999999", "artifact_url": "https://objects.example/objects/9999"}
		}}]
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), result)
	if vis.RenderStatus != RenderStatusRendered {
		t.Fatalf("RenderStatus = %q, want rendered from remote_final_video", vis.RenderStatus)
	}
	if vis.DrivePublication != DrivePublicationPending {
		t.Fatalf("DrivePublication = %q, want pending", vis.DrivePublication)
	}
}

// TestExtractDeliveryVisibility_SidecarDeliveryDoesNotMaskTheFinalVideo pins
// that a confirmed script/audio sidecar upload is not the final job's
// delivery: the master video itself must be confirmed before /full reports
// published.
func TestExtractDeliveryVisibility_SidecarDeliveryDoesNotMaskTheFinalVideo(t *testing.T) {
	t.Parallel()

	result := json.RawMessage(`{
		"result": {
			"remote_final_job": {"job_id": "master-1", "status": "SUCCEEDED", "sha256": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
			"drive_deliveries": {
				"job-1:script_json": {"artifact_id": "job-1:script_json", "kind": "script", "drive_file_id": "drive-script-1", "delivery_status": "completed"}
			}
		}
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), result)
	if vis.RenderStatus != RenderStatusRendered {
		t.Fatalf("RenderStatus = %q, want rendered", vis.RenderStatus)
	}
	if vis.DrivePublication != DrivePublicationPending || vis.DriveFileID != "" {
		t.Fatalf("delivery = %q/%q, want pending with no file id (sidecar is not the video)", vis.DrivePublication, vis.DriveFileID)
	}
}

// TestExtractDeliveryVisibility_DeliveredNeedsTheOutboxReceipt pins that an
// empty status on a receipt is not a render: only a SUCCEEDED receipt with a
// certified digest (or the delivery receipt itself) proves the bytes exist.
func TestExtractDeliveryVisibility_DeliveredNeedsTheOutboxReceipt(t *testing.T) {
	t.Parallel()

	noBytes := json.RawMessage(`{"result":{"remote_final_job":{"job_id":"master-1","status":"SUCCEEDED"}}}`)
	if vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), noBytes); vis.RenderStatus != "" {
		t.Fatalf("RenderStatus = %q, want empty without a certified digest: %+v", vis.RenderStatus, vis)
	}

	delivered := json.RawMessage(`{
		"result": {
			"remote_final_job": {"job_id": "master-1", "status": "SUCCEEDED", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			"drive_deliveries": {
				"job-1:final_video": {"artifact_id": "job-1:final_video", "kind": "video", "drive_file_id": "drive-video-1", "drive_link": "https://drive/file/video-1", "delivery_status": "completed"}
			}
		}
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), delivered)
	if vis.RenderStatus != RenderStatusRendered {
		t.Fatalf("RenderStatus = %q, want %q", vis.RenderStatus, RenderStatusRendered)
	}
	if vis.DrivePublication != DrivePublicationPublished || vis.DriveFileID != "drive-video-1" {
		t.Fatalf("delivery projection = %q/%q, want %q/drive-video-1", vis.DrivePublication, vis.DriveFileID, DrivePublicationPublished)
	}
}

// TestExtractDeliveryVisibility_NeverInventsDeliveryFromARender keeps the
// pending state explicit when the render exists but the item-scoped receipt
// carries no file id.
func TestExtractDeliveryVisibility_NeverInventsDeliveryFromARender(t *testing.T) {
	t.Parallel()

	result := json.RawMessage(`{
		"items": [{"item_id": "item-1", "result": {
			"remote_final_job": {"job_id": "master-1", "status": "SUCCEEDED", "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
			"drive_deliveries": {"job-1:final_video": {"artifact_id": "job-1:final_video", "kind": "video", "drive_file_id": "", "delivery_status": "pending"}}
		}}]
	}`)
	vis := ExtractDeliveryVisibility(json.RawMessage(realShapePayload), result)
	if vis.DrivePublication != DrivePublicationPending || vis.DriveFileID != "" {
		t.Fatalf("delivery projection = %q/%q, want pending with no file id", vis.DrivePublication, vis.DriveFileID)
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
