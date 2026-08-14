package embedding

// Component names used in MismatchError.Component.
const (
	// ComponentSidecar is the embedding sidecar runtime leg.
	ComponentSidecar = "embedding_sidecar_runtime"
	// ComponentQdrant is the Qdrant active-collection metadata leg.
	ComponentQdrant = "qdrant_active_collection"
	// ComponentQuery is the query-embedder leg.
	ComponentQuery = "query_embedder"
)

// Verify is the boot-time handshake. It fails closed with a MismatchError
// (code QDRANT_EMBEDDING_CONTRACT_MISMATCH) unless all four legs agree:
//
//	canonical contract == sidecar runtime == Qdrant collection == query embedder
//
// Comparison strictness per leg:
//
//   - sidecar: full equality — the sidecar reports every field via /contract.
//   - qdrant: partial — Qdrant collection metadata only exposes dimension +
//     distance, so only the populated fields are asserted.
//   - query: partial — the query-embedder config only exposes a model id, so
//     only the populated fields are asserted. When a full runtime contract is
//     available, the extra fields are checked too.
func Verify(canonical, sidecar, qdrant, query Contract) error {
	if !canonical.Equal(sidecar) {
		return &MismatchError{Component: ComponentSidecar, Expected: canonical, Got: sidecar}
	}
	if !canonical.MatchesPartial(qdrant) {
		return &MismatchError{Component: ComponentQdrant, Expected: canonical, Got: qdrant}
	}
	if !canonical.MatchesPartial(query) {
		return &MismatchError{Component: ComponentQuery, Expected: canonical, Got: query}
	}
	return nil
}
