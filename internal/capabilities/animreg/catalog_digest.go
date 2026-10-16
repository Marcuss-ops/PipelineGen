// Package animreg — catalog_digest.go: registry view helpers that live beside
// the embedded JSON without enlarging animation_registry.go past the strict
// 600-line gate.
//
// P0-3 (piano unificazione animazioni): la vista capacità eseguibile portata
// dal registry è identificata da un digest versionato dell'JSON incorporato.
// I piani sigillati la salvano; un worker con digest diverso rifiuta il job
// invece di interpretarlo diversamente. L'algoritmo è delegato all'SSOT
// internal/kernel/digest (godlike/06): nessun import crypto/sha256 fuori dal
// kernel, nessuna seconda lista manuale delle capacità.
package animreg

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

// FamiliesOf returns the registry families of one animation id. The second
// result is false when the id is not an entry of the registry. Callers must
// treat the returned slice as read-only: the backing entry is never exposed.
func (r *AnimationRegistry) FamiliesOf(id string) ([]string, bool) {
	e, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	out := make([]string, len(e.Families))
	copy(out, e.Families)
	return out, true
}

// CatalogDigest è la vista capacità eseguibile versionata (P0-3):
// sha256 dell'animation_registry.v1.json incorporato + versione schema.
// I piani sigillati la salvano; worker con digest diverso rifiutano il job
// invece di interpretarlo diversamente.
func CatalogDigest() string {
	return "sha256:" + digest.SHA256Bytes(animationRegistryJSON) + ":v1"
}
