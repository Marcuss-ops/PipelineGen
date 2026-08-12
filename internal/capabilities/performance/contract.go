package performance

import "context"

type Run struct {
	RunID, JobID, RootJobID, VideoID                              string
	WorkloadID, WorkloadVersion, GitSHA, WorkerID, HostID         string
	Status                                                        string
	WallMS, CPUUserMS, CPUSystemMS, PeakRSSBytes                  int64
	DiskReadBytes, DiskWriteBytes, NetworkRXBytes, NetworkTXBytes int64
	MetadataJSON, StartedAt, CompletedAt                          string
}

type Step struct {
	StepID, RunID, JobID, Name, Status                           string
	DurationMS, InputCount, OutputCount, InputBytes, OutputBytes int64
	CacheHits, CacheMisses                                       int64
	MetadataJSON, StartedAt, CompletedAt                         string
}

type Artifact struct {
	ArtifactID, RunID, Kind, SHA256, URI, CreatedAt string
	SizeBytes                                       int64
}

type Workload struct {
	WorkloadID, Version, InputManifestSHA256, ParametersJSON, ExpectedOutputSHA256, CreatedAt string
}

type Registry interface {
	RecordRun(context.Context, Run) error
	RecordStep(context.Context, Step) error
	RecordArtifact(context.Context, Artifact) error
	RegisterWorkload(context.Context, Workload) error
}
