package model_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestModelTypesAreWireAliases pins that EVERY type shared with the queue wire
// boundary is an alias of the public contract, not a second declaration that
// merely agrees field-for-field today.
//
// The historical shape declared each of these twice (once in queue/client for
// the wire, once here for persistence) and kept them in step with hand-written
// copies in the server and the worker adapter. A field added on one side was
// silently dropped on the other (the progress projection was lost exactly
// this way). The compiler now enforces the identity; this test makes the
// intent explicit so a future contributor cannot quietly reintroduce a mirror
// struct, and it fails loudly for any type that has not been migrated yet.
func TestModelTypesAreWireAliases(t *testing.T) {
	pairs := []struct {
		name      string
		modelType reflect.Type
		wireType  reflect.Type
	}{
		{"Job", reflect.TypeOf(model.Job{}), reflect.TypeOf(client.Job{})},
		{"Artifact", reflect.TypeOf(model.Artifact{}), reflect.TypeOf(client.Artifact{})},
		{"AssetRef", reflect.TypeOf(model.AssetRef{}), reflect.TypeOf(client.AssetRef{})},
		{"FrameRange", reflect.TypeOf(model.FrameRange{}), reflect.TypeOf(client.FrameRange{})},
		{"Progress", reflect.TypeOf(model.Progress{}), reflect.TypeOf(client.Progress{})},
		{"Stats", reflect.TypeOf(model.Stats{}), reflect.TypeOf(client.Stats{})},
		{"Worker", reflect.TypeOf(model.Worker{}), reflect.TypeOf(client.Worker{})},
		{"WorkerStatus", reflect.TypeOf(model.WorkerStatusUnknown), reflect.TypeOf(client.WorkerStatusUnknown)},
		{"WorkerHealth", reflect.TypeOf(model.WorkerHealth{}), reflect.TypeOf(client.WorkerHealth{})},
		{"State", reflect.TypeOf(model.StatePending), reflect.TypeOf(client.StatePending)},
	}
	for _, p := range pairs {
		if p.modelType != p.wireType {
			t.Errorf("model.%s (%v) must alias client.%s (%v): the wire contract exists once",
				p.name, p.modelType, p.name, p.wireType)
		}
	}
}

// TestJobEnvelopeCarriesProgressAndLease pins the fields the alias migration
// made load-bearing: the GET projection carries progress and a claim response
// carries the lease. If the canonical Job drops either key, a producer or a
// worker silently loses the fact — this is the regression for the exact
// dropped-progress bug the alias fixes.
func TestJobEnvelopeCarriesProgressAndLease(t *testing.T) {
	job := client.Job{
		ID:         "job-1",
		RenderPlan: json.RawMessage(`{}`),
		Progress:   &client.Progress{FramesDone: 485, TotalFrames: 1800, Worker: "w1", LastFrameAt: time.Now().UTC()},
		Lease:      30 * time.Second,
	}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	for _, key := range []string{`"progress"`, `"frames_done"`, `"frames_total"`, `"last_frame_at"`, `"lease"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("job envelope is missing %s: %s", key, raw)
		}
	}

	// The lease is absent when zero (submit/GET bodies never carry it).
	plain, err := json.Marshal(client.Job{ID: "job-2", RenderPlan: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("marshal plain job: %v", err)
	}
	if strings.Contains(string(plain), `"lease"`) {
		t.Errorf("a zero lease must be omitted from a submit/GET body: %s", plain)
	}

	var back client.Job
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal job: %v", err)
	}
	if back.Progress == nil || back.Progress.FramesDone != 485 || back.Progress.TotalFrames != 1800 || back.Progress.Worker != "w1" {
		t.Fatalf("progress did not round-trip: %+v", back.Progress)
	}
	if back.Lease != 30*time.Second {
		t.Fatalf("lease did not round-trip: %v", back.Lease)
	}
}
