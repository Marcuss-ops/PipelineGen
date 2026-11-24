package contract

// LocalFinalVideoArtifact is the job-owned final MP4 assembled from the
// certified per-item overlay renders. It is distinct from RemoteFinalVideo:
// that one is a Master render receipt, this one was joined on the job's own
// timeline from the clips this run rendered.
type LocalFinalVideoArtifact struct {
	Path       string
	SizeBytes  int64
	SHA256     string
	DurationUS int64
	Width      int
	Height     int
	FPS        int
	// JoinedItems is how many certified item renders were placed on the
	// timeline; PlanItems is how many the plan carried. They differ when an
	// item produced no render (cache miss, unsupported item), and the pair is
	// the observable difference between "joined everything" and "joined what
	// exists".
	JoinedItems int
	PlanItems   int
}
