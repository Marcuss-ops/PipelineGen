# FASE B clip pipeline smoke — real-mode report

## Execution context

- Date: 2026-07-04T13:02:50+00:00
- Server binary: bin/pipelinegen (built fresh via `go build -o bin/pipelinegen ./cmd/server`)
- Server mode: `--mode all` (HTTP + worker in-process)
- VELOX_PORT: 8080
- Canonical phase: PR-WORKER-RUNNER-INPROCESS-MIGRATION landed at 407ab6d7 + ci.yml::fase-b-clip-smoke at 407ab6d7
- Token: VELOX_ADMIN_TOKEN (length 64, value redacted per godlike/07)
- Database: data/media/media.db.sqlite (4313088 bytes)
- Health check: ✗ server failed /health wait
- Server log: server.log tail

```text
{"level":"info","timestamp":"2026-07-04T13:01:50.117Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-7","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.117Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-2","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.117Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-16","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.117Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-5","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-12","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-9","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-13","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-8","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-8"}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-6"}
{"level":"info","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-10","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-16"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-10"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-9"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-12"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-3"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-11"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/scanner.go:69","msg":"stopping lease scanner"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-2"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-1"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"voiceover/orphan_sweeper.go:248","msg":"orphan-sweeper: ctx cancelled, exiting","error":"context canceled"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:289","msg":"worker started","worker_id":"YOutube_2400619_worker-15","base_poll_every":2,"max_backoff":60,"consecutive_empty_threshold":3}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-15"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-14"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-13"}
{"level":"fatal","timestamp":"2026-07-04T13:01:50.118Z","logger":"server","caller":"server/main.go:109","msg":"server failed","error":"server listen error: listen tcp 127.0.0.1:8080: bind: address already in use","stacktrace":"main.main\n\t/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/cmd/server/main.go:109\nruntime.main\n\t/usr/local/go/src/runtime/proc.go:285"}
{"level":"warn","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker_metrics.go:37","msg":"metrics refresh failed (immediate tick)","error":"queue depth query: context canceled"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-7"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-4"}
{"level":"info","timestamp":"2026-07-04T13:01:50.119Z","logger":"server","caller":"jobs/worker.go:304","msg":"worker stopped (ctx before first claim)","worker_id":"YOutube_2400619_worker-5"}
```

**Status: BLOCKED** — server did not become healthy; smoke test did not run.
