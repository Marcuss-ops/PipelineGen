// Package controlplane defines the canonical Control Plane verification
// contract. It contains no storage or network implementation.
package controlplane

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	CanonicalDBPath             string  `json:"canonical_db_path"`
	RegistryID                  string  `json:"registry_id"`
	DatabaseID                  string  `json:"database_id"`
	SchemaFamily                string  `json:"schema_family"`
	InstanceRole                string  `json:"instance_role"`
	SchemaVersion               int     `json:"schema_version"`
	MigrationGaps               []int   `json:"migration_gaps,omitempty"`
	MigrationChecksumMismatches []int   `json:"migration_checksum_mismatches,omitempty"`
	Checks                      []Check `json:"checks"`
	Assets                      int64   `json:"assets"`
	Transcripts                 int64   `json:"transcripts"`
	Descriptions                int64   `json:"descriptions"`
	Jobs                        int64   `json:"jobs"`
	PendingOutbox               int64   `json:"pending_outbox"`
	DeadOutbox                  int64   `json:"dead_outbox"`
	CASObjects                  int64   `json:"cas_objects"`
	CASOrphans                  int64   `json:"cas_orphans"`
	BrokenCASLinks              int64   `json:"broken_cas_links"`
	RegistrySeq                 int64   `json:"registry_seq"`
	ProjectionSeq               int64   `json:"projection_seq"`
	ProjectionDrift             int64   `json:"projection_drift"`
	ProjectionState             string  `json:"projection_state"`
	PerformanceRuns             int64   `json:"performance_runs"`
	UncorrelatedPerformanceRuns int64   `json:"uncorrelated_performance_runs"`
	Status                      string  `json:"status"`
}

func (r Report) Healthy() bool { return r.Status == "HEALTHY" }
