// Package monitor — ChannelMonitor: periodic YouTube channel monitoring.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): scheduler.go was
// split into 6 focused files per the god-object action plan topology:
//
//   - monitor.go           — ChannelMonitor struct + NewChannelMonitor + channelSemWidth
//   - scheduler_loop.go    — Start + runSchedulerCycle (ticker + ClaimDue)
//   - channel_runner.go    — checkDueChannels + safeCheckChannel (semaforo + timeout + panic recovery)
//   - outbox_drainer.go    — startOutboxDrainer + drainOutboxOnce + dispatchOutboxEntry
//   - channel_validation.go — validateChannelConfig + validateJSONArray
//   - check_outcome.go     — recordCheckOutcome + nextCheckTime + parseCheckInterval + extractChannelHandle
//
// Other production files (unchanged by this split):
//   - ports.go         — Pattern 0 port surface + DTO types + CompositionDeps
//   - discovery.go     — cheap per-video discovery + filter chain + ledger dedupe
//   - analyzer.go      — AI gate (TranscriptProvider + VideoAnalyzer ports)
//   - enqueue.go       — durable-job emission (JobEnqueuer port)
//   - policy.go        — MonitorRuntimePolicy
//
// The scheduler never touches os/exec, OllamaClient, or VTT regex
// directly — those concerns cross the package boundary through typed ports.
package assets
