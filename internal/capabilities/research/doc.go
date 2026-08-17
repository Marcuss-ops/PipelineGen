// Package research owns the technology-independent contracts for
// source-backed research used by ranking and narrative generation.
//
// The package contains no web, database, LLM, or provider code. Concrete
// adapters belong in internal/platform; orchestration belongs in internal/app
// or a capability-specific application seam. EvidencePack is the immutable
// handoff between parallel subject research and downstream ranking.
package research
