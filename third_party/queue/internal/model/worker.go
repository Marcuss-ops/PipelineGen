package model

import "github.com/Marcuss-ops/RenderingGen/queue/client"

// WorkerStatus is the lifecycle status of a rendering worker. It is an ALIAS
// of the public wire contract type (queue/client), like every other type on
// this boundary: the worker registers through the same declaration the queue
// service validates and stores, so the vocabulary cannot drift.
type WorkerStatus = client.WorkerStatus

const (
	WorkerStatusUnknown  = client.WorkerStatusUnknown
	WorkerStatusReady    = client.WorkerStatusReady
	WorkerStatusBusy     = client.WorkerStatusBusy
	WorkerStatusDraining = client.WorkerStatusDraining
	WorkerStatusOffline  = client.WorkerStatusOffline
)

// Worker is a registered rendering worker: the registry row for the
// GPU/Chronon worker. LastHeartbeatAt doubles as its liveness ledger so a
// worker that stops heartbeating can be drained, and it is what the derived
// Liveness field (see liveness.go) is computed from. It is an ALIAS of the
// public wire type.
type Worker = client.Worker

// WorkerLiveness and its vocabulary are declared in liveness.go, next to the
// only function that computes them.

// WorkerHealth is the aggregate worker-health snapshot used for autoscaling
// and monitoring. It is an ALIAS of the public wire type, and it is DERIVED
// here (SummarizeWorkerHealth) from the rows the store returns plus the
// heartbeat-age boundaries — the same derivation that annotates each worker on
// GET /workers, so the aggregate cannot contradict the per-worker view.
type WorkerHealth = client.WorkerHealth
