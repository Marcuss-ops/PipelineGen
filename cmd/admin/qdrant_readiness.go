// Package main qdrant readiness implementation is split across:
//
//   - qdrant_readiness_command.go       CLI parsing and output
//   - qdrant_readiness_report.go        report contract
//   - qdrant_readiness_composition.go   production composition bridge
//   - qdrant_readiness_orchestrator.go  checks orchestration and probes
//
// All files remain in package main, so the command surface, report JSON,
// check registry, and helper contracts are unchanged.
package main
