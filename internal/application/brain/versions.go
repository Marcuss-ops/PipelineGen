package brain

// Canonical version stamps for the Brain capability. These constants
// are the single source of truth for every version that participates
// in the resolution decision fingerprint. Bump a constant whenever
// the corresponding algorithm, schema or registry changes so old
// exact-memory hits are invalidated.
const (
	// BrainVersion is the canonical orchestrator version.
	BrainVersion = "brain-v1"

	// EmbeddingVersion identifies the embedding model / schema used
	// by the semantic search path consumed by the Brain.
	EmbeddingVersion = "multilingual-e5-v1"

	// DiversityPolicyVersion is the canonical anti-repetition /
	// diversity policy applied by the ranker and planner.
	DiversityPolicyVersion = "diversity-policy-v1"

	// LexiconVersion identifies the linguistic lexicon used by intent
	// resolvers and entity filters. It is loaded from config/lexicons/
	// by the linguistics.LexiconRegistry at bootstrap. Bump this
	// constant whenever stopword lists, verb suffixes, function-word
	// profiles or entity blocklists change in the config directory.
	LexiconVersion = "lexicon-v1"
)
