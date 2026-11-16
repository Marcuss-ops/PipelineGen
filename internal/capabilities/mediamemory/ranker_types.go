// Package mediamemory — ranker_types.go holds the two envelope types the
// production build still needs around the ranking boundary:
//
//   - RankingVerdict: the closed set the ranker answers with (accept /
//     downrank / drop). PoolRankScore carries it, so the resolver can surface
//     the ranker's own verdict without parsing a string.
//   - FilteredCandidate: the pre-rank candidate shape the cascade produces.
//     It carries the MediaCandidate plus the originating binding envelope so
//     nothing is lost on the way to the ranker.
//
// RankingInput and RankingOutput were the Go ranker's internal scoring
// surfaces; they moved to ranker_oracle_test.go with the rest of the frozen
// oracle, because no production code computes a seat score any more. The
// canonical formula lives in `mediasampler::rank`.
//
// File split (godlike/06 single canonical home per layer):
//   - pool_ranker.go          : PoolRanker port + pool DTOs
//   - ranker_types.go         : RankingVerdict + FilteredCandidate  ← this file
//   - ranker_oracle_test.go   : RankingInput + RankingOutput + the frozen Go ranker (tests only)
package mediamemory

// RankingVerdict is the canonical post-rank verdict (closed set).
// Verdict == Drop MUST short-circuit the resolver: no SceneVisualPlan layer
// for this candidate.
type RankingVerdict string

const (
	VerdictAccept   RankingVerdict = "accept"
	VerdictDownrank RankingVerdict = "downrank"
	VerdictDrop     RankingVerdict = "drop"
)

// FilteredCandidate is the pre-rank input shape produced by the cascade.
//
// godlike/06 SSOT extension (binding envelope): the Binding field carries the
// originating binding alongside the candidate so the ranker can pull
// operator-curated scores (ManualScore, SemanticScore, QualityScore,
// SuccessScore) and the binding window (StartMs, EndMs) without losing
// information at the projection boundary.
//
// IsDuplicate / MissingRights / AspectMismatch / Contaminated are the resolver-era
// gate flags. Production leaves them zero: the canonical gates now run inside
// the ranker. They survive because the test oracle reads them.
type FilteredCandidate struct {
	Candidate      MediaCandidate
	Binding        MediaBinding
	IsDuplicate    bool
	MissingRights  bool
	AspectMismatch bool
	Contaminated   bool
}
