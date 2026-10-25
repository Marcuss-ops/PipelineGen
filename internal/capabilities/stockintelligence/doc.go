// Package stockintelligence owns the local stock-intelligence resolver
// (Fase 5). It is the LOCAL FIRST PROVIDER SECOND layer that sits between
// the QueryPlanner and the MediaSampler.
//
// Architecture (the package certifies the same invariant MediaCert does):
//
//	SQLite = truth
//	Qdrant = search projection
//
// Pipeline:
//
//	        ↓
//	AssetCommitter
//	        ↓
//	SQLite
//	        ↓
//	Outbox
//	        ↓
//	Qdrant
//
//	────────────────────────
//	runtime search
//
//	SceneIR
//	   ↓
//	embedding/query
//	   ↓
//	Qdrant local search
//	   ↓
//	SQLite hydrate
//	   ↓
//	MediaSampler
//	   ↓
//	winner
//
//
//	local_candidates < threshold
//
//	OR
//
//	best_score < minimum_quality
//
// So LOCAL FIRST, PROVIDER SECOND. The package depends only on the
// adapters are wired at the composition root and satisfy the ports
// declared here.
package stockintelligence
