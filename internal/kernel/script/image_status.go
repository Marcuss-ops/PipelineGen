package script

// Semantic candidate verdict vocabulary shared by the image gate, the person
// image scorer and the entity catalog. The catalog re-exports these values so
// there is one spelling per verdict.
const (
	CandidateSemanticUnknown  = "unknown"
	CandidateSemanticAccepted = "accepted"
	CandidateSemanticRejected = "rejected"
)
