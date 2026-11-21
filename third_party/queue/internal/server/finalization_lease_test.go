package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestFinalizeClaimCarriesTheLease pins the wire contract for the parent
// finalization claim: a claim is the ONE response that carries the lease the
// worker must renew, and the finalizer is no exception.
//
// Without the duration the worker's renewal loop never starts (it is guarded by
// Lease > 0), so an assembly that outlives the queue lease is requeued while it
// is still muxing: the parent shows `finalizing` with an expired lease and a
// later Complete/Renew is a 409. The finalizer then has no way to tell a lost
// lease from a slow render.
func TestFinalizeClaimCarriesTheLease(t *testing.T) {
	ts := newServer(t)

	resp := post(t, ts.URL+"/jobs", `{"id":"parent-1","render_plan":{},"assets":[]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit parent: want 201, got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs", `{"id":"parent-1-chunk-0","parent_job_id":"parent-1","chunk_index":0,"frame_range":{"start":0,"end":120},"render_plan":{},"assets":[]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit chunk: want 201, got %d", resp.StatusCode)
	}

	resp = post(t, ts.URL+"/jobs/parent-1/finalize/claim", `{"worker":"finalizer-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finalize claim: want 200, got %d", resp.StatusCode)
	}
	var claimed model.Job
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode finalize claim: %v", err)
	}
	if claimed.State != model.StateFinalizing {
		t.Fatalf("state = %q, want finalizing", claimed.State)
	}
	if claimed.Lease <= 0 {
		t.Fatalf("lease = %s, want the claim lease duration so the worker renews it", claimed.Lease)
	}
	if claimed.LeaseGeneration <= 0 {
		t.Fatalf("lease_generation = %d, want a positive fencing token", claimed.LeaseGeneration)
	}
	if claimed.LeaseUntil.IsZero() || !claimed.LeaseUntil.After(time.Now()) {
		t.Fatalf("lease_until = %s, want a future deadline", claimed.LeaseUntil)
	}
	if remaining := time.Until(claimed.LeaseUntil); math.Abs(float64(remaining-claimed.Lease)) > float64(time.Second) {
		t.Fatalf("lease_until is %s away but lease is %s; the two disagree", remaining, claimed.Lease)
	}

	// The returned generation must actually renew the finalizing lease: that is
	// the only thing standing between a long assembly and a requeue.
	renewBody := `{"worker":"finalizer-1","lease_generation":` + strconv.FormatInt(claimed.LeaseGeneration, 10) + `}`
	resp = post(t, ts.URL+"/jobs/parent-1/renew", renewBody)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("renew with the claimed generation: want 204, got %d", resp.StatusCode)
	}
}
