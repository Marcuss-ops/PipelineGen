package embedding

// Component names used in MismatchError.Component.
const (
	// ComponentSidecar is the embedding sidecar runtime leg.
	ComponentSidecar = "embedding_sidecar_runtime"
	// ComponentQuery is the query-embedder leg.
	ComponentQuery = "query_embedder"
)

// Verify is the boot-time handshake. It fails closed with a MismatchError
// (code EMBEDDING_CONTRACT_MISMATCH) unless all three legs agree:
//
//	canonical contract == sidecar runtime == query embedder
//
// The former vector engine active-collection leg was deleted with the legacy vector engine
// on 2026-11-17; pgvector stores the vectors, so there is no collection
// metadata left to compare.
//
// Comparison strictness per leg:
//
//   - sidecar: full equality — the sidecar reports every field via /contract.
//   - query: partial — the query-embedder config only exposes a model id, so
//     only the populated fields are asserted. When a full runtime contract is
//     available, the extra fields are checked too.
func Verify(canonical, sidecar, query Contract) error {
	if !canonical.Equal(sidecar) {
		return &MismatchError{Component: ComponentSidecar, Expected: canonical, Got: sidecar}
	}
	if !canonical.MatchesPartial(query) {
		return &MismatchError{Component: ComponentQuery, Expected: canonical, Got: query}
	}
	return nil
}
