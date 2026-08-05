// Package observability is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
//
// It owns the canonical cross-capability observability contract shared by
// every job family (script.generate, voiceover.generate, stock.run,
// youtube.clip.extract, transcription, indexing, VidRush, cleanup and
// reconciliation). The contract is:
//
//	Run        — one job execution (created by RunObserver.StartRun)
//	Stage      — one canonical pipeline phase (validate, acquire, ...)
//	Operation  — one external-boundary call (qdrant.search, drive.upload, ...)
//	Artifact   — one produced or reused output
//	Counters   — universal run counters (items, cache, retries, bytes)
//
// Every job creates a Run. Every phase creates a Stage. Every external
// boundary creates an Operation. Every retry creates an Attempt. Every
// output creates an Artifact. Everything is recorded by the same observer
// and serialised into one RunReport JSON document.
//
// Timing invariants:
//
//	queue_wait_ms   — enqueue → processing start (set by the runtime at claim)
//	wall_time_ms    — StartedAt → FinishedAt
//	active_ms       — sum of stage durations (active processing)
//	blocked_ms      — explicit waits reported via AddBlocked (semaphore,
//	                  rate-limit, retry backoff)
//	accumulated_operation_ms — sum of operation durations; under real
//	                  parallelism this exceeds wall_time_ms (parallelism
//	                  diagnostic: 4 parallel 6s downloads ≈ 24s accumulated
//	                  vs ≈ 6s wall)
//
// The package is deliberately dependency-free: standard library only, no
// repository implementation, no Gin, no database/sql, no transport-specific
// type (kernel rules in 02_TARGET_STRUCTURE.md). Prometheus observation and
// SQLite persistence are later-phase adapters; the only extension seam here
// is the Recorder sink.
package observability
