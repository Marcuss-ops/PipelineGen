package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestSubmitWithNotBeforeDefersClaimUntilDue pins the wire-to-claim contract of
// the deferred-scheduling field: a job submitted with a future not_before is
// stored immediately (it is readable by id, so nothing is silently dropped) but
// is NOT handed to a worker, and it does not stall a job that is already due.
//
// This is the RenderingGen-side counterpart of the PipelineGen scheduling lane:
// the render queue must be able to accept a whole day of work up front without
// rendering any of it early.
func TestSubmitWithNotBeforeDefersClaimUntilDue(t *testing.T) {
	ts := newServer(t)

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	resp := post(t, ts.URL+"/jobs", `{"id":"deferred","not_before":"`+future+`","render_plan":{"o":1}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deferred submit: want 201, got %d", resp.StatusCode)
	}

	// Stored, not dropped.
	getResp, err := http.Get(ts.URL + "/jobs/deferred")
	if err != nil {
		t.Fatalf("get deferred: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get deferred: want 200, got %d", getResp.StatusCode)
	}

	// Not claimable before its due time.
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("claim before due: want 204, got %d", resp.StatusCode)
	}

	// A job that is due NOW is still claimable: deferral of one job must not
	// block the queue behind it.
	resp = post(t, ts.URL+"/jobs", `{"id":"due","render_plan":{"o":2}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("due submit: want 201, got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim due job: want 200, got %d", resp.StatusCode)
	}
	var claimed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.ID != "due" {
		t.Fatalf("claimed %q, want the due job (the deferred job must keep waiting)", claimed.ID)
	}

	// The deferred job is still pending after the due job was handed out.
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w2"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second claim: want 204 (only the deferred job is left), got %d", resp.StatusCode)
	}
}

// TestSubmitWithoutNotBeforeIsImmediatelyClaimable is the backward-compatibility
// pin: every pre-existing producer omits the field, and those jobs must remain
// claimable at once (an absent not_before is not "never").
func TestSubmitWithoutNotBeforeIsImmediatelyClaimable(t *testing.T) {
	ts := newServer(t)

	resp := post(t, ts.URL+"/jobs", `{"id":"plain","render_plan":{"o":1}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", resp.StatusCode)
	}
	var claimed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.ID != "plain" {
		t.Fatalf("claimed %q, want plain", claimed.ID)
	}
}
