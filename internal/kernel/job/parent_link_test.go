package job

import (
	"encoding/json"
	"testing"
)

func TestParentLinkPayloadRoundTrip(t *testing.T) {
	payload := json.RawMessage(`{"item":"one","parent_job_id":"parent-job","keep":true}`)
	link := ParentLinkFromPayload(payload)
	if link.ParentJobID != "parent-job" || link.ParentRunID != "" {
		t.Fatalf("link = %#v", link)
	}

	updated := InjectParentLink(payload, ParentLink{ParentJobID: "parent-job-2", ParentRunID: "parent-run"})
	var got map[string]any
	if err := json.Unmarshal(updated, &got); err != nil {
		t.Fatalf("unmarshal updated payload: %v", err)
	}
	if got["item"] != "one" || got["keep"] != true || got["parent_job_id"] != "parent-job-2" || got["parent_run_id"] != "parent-run" {
		t.Fatalf("updated payload = %#v", got)
	}
}

func TestParentLinkPayloadIgnoresNonObject(t *testing.T) {
	payload := []byte(`[1,2,3]`)
	if got := InjectParentLink(payload, ParentLink{ParentJobID: "p"}); string(got) != string(payload) {
		t.Fatalf("non-object payload changed: %q", got)
	}
	if got := ParentLinkFromPayload(payload); got != (ParentLink{}) {
		t.Fatalf("non-object link = %#v", got)
	}
}
