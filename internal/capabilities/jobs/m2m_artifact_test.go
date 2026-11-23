package jobs

import (
	"strings"
	"testing"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestArtifactReceiptFromResultFindsNestedReceipt(t *testing.T) {
	raw := []byte(`{"ok":true,"result":{"remote_final_job":{"job_id":"master-1","status":"SUCCEEDED","artifact_url":"https://master/final.mp4","sha256":"` + testSHA + `"}}}`)
	receipt, err := artifactReceiptFromResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.JobID != "master-1" || receipt.SHA256 != testSHA {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestArtifactReceiptRejectsPendingRender(t *testing.T) {
	raw := []byte(`{"remote_final_job":{"status":"RUNNING","artifact_url":"https://master/final.mp4","sha256":"` + testSHA + `"}}`)
	if _, err := artifactReceiptFromResult(raw); err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("err = %v, want not complete", err)
	}
}

func TestArtifactReceiptRejectsMissingHash(t *testing.T) {
	raw := []byte(`{"remote_final_job":{"status":"SUCCEEDED","artifact_url":"https://master/final.mp4"}}`)
	if _, err := artifactReceiptFromResult(raw); err == nil {
		t.Fatal("expected an error for a receipt without sha256")
	}
}

func TestArtifactReceiptRejectsNonFinalPayload(t *testing.T) {
	raw := []byte(`{"image_url":"https://master/image.png","sha256":"` + testSHA + `"}`)
	if _, err := artifactReceiptFromResult(raw); err == nil {
		t.Fatal("expected an error when no final-render receipt exists")
	}
}
