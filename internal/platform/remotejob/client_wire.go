package remotejob

import (
	"encoding/json"
	"strings"
	"time"
)

func unwrap(in jobEnvelope) jobEnvelope {
	var wrapped jobEnvelope
	if len(in.Job) > 0 && json.Unmarshal(in.Job, &wrapped) == nil && wrapped.Status != "" {
		return withOuterTiming(wrapped, in)
	}
	if json.Unmarshal(in.Result, &wrapped) == nil && wrapped.Status != "" {
		return withOuterTiming(wrapped, in)
	}
	return in
}

// withOuterTiming keeps the envelope's phase boundaries when the nested job
// object does not repeat them: older Masters wrap status in job/result while
// still exposing the timestamps at the top level.
func withOuterTiming(inner, outer jobEnvelope) jobEnvelope {
	if inner.CreatedAt == "" {
		inner.CreatedAt = outer.CreatedAt
	}
	if inner.StartedAt == "" {
		inner.StartedAt = outer.StartedAt
	}
	if inner.CompletedAt == "" {
		inner.CompletedAt = outer.CompletedAt
	}
	if len(inner.WorkerTiming) == 0 {
		inner.WorkerTiming = outer.WorkerTiming
	}
	return inner
}

// workerTimingFrom decodes the relayed worker report. A malformed or absent
// block is "not reported": the breakdown is observability and must never fail
// a render the Master already completed.
func workerTimingFrom(raw json.RawMessage) WorkerTiming {
	if len(raw) == 0 {
		return WorkerTiming{}
	}
	var wire workerTimingWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return WorkerTiming{}
	}
	return WorkerTiming{
		LeasedAt:   parseMasterTime(wire.LeasedAt),
		StartedAt:  parseMasterTime(wire.StartedAt),
		FinishedAt: parseMasterTime(wire.FinishedAt),
		Download:   nonNegativeMS(wire.DownloadMS),
		Decode:     nonNegativeMS(wire.DecodeMS),
		Compose:    nonNegativeMS(wire.ComposeMS),
		Encode:     nonNegativeMS(wire.EncodeMS),
		Mux:        nonNegativeMS(wire.MuxMS),
		Upload:     nonNegativeMS(wire.UploadMS),
		Frames:     wire.Frames,
		EncodeFPS:  wire.EncodeFPS,
	}
}

// nonNegativeMS refuses a negative worker phase: a worker clock cannot produce
// a negative duration, so such a value is a malformed report, not data.
func nonNegativeMS(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// parseMasterTime reads a Master phase boundary. A malformed or absent value is
// "not reported", never an error: the timing breakdown is observability and
// must not fail a render that otherwise succeeded.
func parseMasterTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// timingFromState derives the Master-side phases, refusing negative intervals
// (clock skew) instead of reporting a nonsensical duration.
func timingFromState(state jobEnvelope) Timing {
	created := parseMasterTime(state.CreatedAt)
	started := parseMasterTime(state.StartedAt)
	completed := parseMasterTime(state.CompletedAt)
	timing := Timing{CreatedAt: created, StartedAt: started, CompletedAt: completed}
	if !created.IsZero() && !started.IsZero() && started.After(created) {
		timing.QueueWait = started.Sub(created)
	}
	if !started.IsZero() && !completed.IsZero() && completed.After(started) {
		timing.Execution = completed.Sub(started)
	}
	timing.Worker = workerTimingFrom(state.WorkerTiming)
	return timing
}

func resultFrom(jobID string, state jobEnvelope) Result {
	var artifact struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal(state.Artifact, &artifact)
	if artifact.URL == "" {
		_ = json.Unmarshal(state.Result, &artifact)
	}
	if artifact.URL == "" {
		artifact.URL = state.ArtifactURL
	}
	if artifact.SHA256 == "" {
		artifact.SHA256 = state.ArtifactSHA256
	}
	return Result{JobID: jobID, Status: state.Status, WorkerID: state.WorkerID, ArtifactURL: artifact.URL, SHA256: artifact.SHA256, Timing: timingFromState(state)}
}
