# V3 Monitor

This directory documents the experimental V3 channel-monitor flow.

File layout:
- `../v3_monitor.go` keeps the orchestration entrypoint and loop.
- `../v3_monitor_ai.go` owns transcript analysis, Gemma classification, and Ollama health checks.
- `../v3_monitor_io.go` owns clip download and upload-path stubs.
- `../v3_monitor_state.go` owns persistence helpers and queue stubs.

The active production monitor remains `../monitor.go`.
