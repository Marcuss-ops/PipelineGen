package models_test

// ── Text-model freeze guard ─────────────────────────────────────────────
//
// The canonical text embedding model is FROZEN to intfloat/multilingual-e5-base
// (768 dims, L2-normalized, Cosine, query:/passage: prefixes — the E5
// asymmetric-retrieval contract). This is a deliberate godlike/06 SSOT
// decision, NOT an accident of the current config:
//
//   - E5-base is MIT-licensed, multilingual, and produces 768-dim vectors
//     that the DefaultV3Schema "text" + "transcript" channels, the Qdrant
//     collection media_assets_v3_e5_768_siglip_768, and every indexed
//     vector already assume.
//   - A switch to intfloat/multilingual-e5-large-instruct (1024 dims) — or
//     ANY other text model — changes the vector space. That is a schema
//     migration: new contract hash → new Qdrant collection → full
//     re-embedding + reindex → blue/green alias switch. It is FORBIDDEN
//     without an explicitly documented migration.
//
// Two tests enforce this:
//
//   1. TestCanonicalTextModelFrozenToE5Base  — pins id / dims / revision /
//      enabled so any drift fails loudly with the migration message.
//   2. TestTextModelMigrationRequiresDocumentation — even if a developer
//      changes the values, the drift is rejected unless
//      textModelMigrationDocumentRef points to a committed migration
//      document that actually exists in the repository.
//
// To deliberately migrate the text model: write the migration document
// (new contract, new collection, reembed + reindex plan, blue/green
// switch), set textModelMigrationDocumentRef to its path, and update the
// frozen values in the test below TOGETHER with the migration review.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// Frozen canonical text-embedding contract. Do NOT edit these values:
// editing them is the vector-space migration that requires the documented
// gate below.
const (
	frozenTextModelID   = "intfloat/multilingual-e5-base"
	frozenTextModelDims = 768
	// frozenTextModelRevision pins the loader-reported model revision. The
	// registry is the owner of this fact; keep it in sync with
	// models.CanonicalTextModelRevision.
	frozenTextModelRevision = "2026-06-26-v1"
)

// textModelMigrationDocumentRef is the ONLY documented escape hatch for
// changing the frozen text model. Keep empty while the model is frozen.
// A drift in TestTextModelMigrationRequiresDocumentation forces setting
// this to the path (relative to the repository root) of a committed
// migration document describing the vector-space change. The referenced
// file must exist — documentation is enforced, not assumed.
const textModelMigrationDocumentRef = ""

// TestCanonicalTextModelFrozenToE5Base pins the canonical text embedding
// model to E5-base. Any deviation (especially intfloat/multilingual-e5-
// large-instruct, 1024 dims) is a vector-space change that invalidates
// every indexed vector and the Qdrant collection shape; it must go
// through the documented migration gate.
func TestCanonicalTextModelFrozenToE5Base(t *testing.T) {
	if models.E5.ID != frozenTextModelID {
		t.Fatalf(
			"canonical text model drifted from frozen %q to %q. Changing the text embedding model (e.g. to intfloat/multilingual-e5-large-instruct, 1024d) is a vector-space migration: new contract hash → new Qdrant collection → full re-embedding + reindex → blue/green alias switch. Set textModelMigrationDocumentRef and document the migration before proceeding.",
			frozenTextModelID, models.E5.ID,
		)
	}
	if models.E5.Dimensions != frozenTextModelDims {
		t.Fatalf(
			"canonical text dimension drifted from %d to %d. A dimension change (e.g. e5-large-instruct 1024d) changes the vector space and invalidates every indexed vector: new contract hash → new Qdrant collection → full re-embedding + reindex → blue/green alias switch. Set textModelMigrationDocumentRef and document the migration before proceeding.",
			frozenTextModelDims, models.E5.Dimensions,
		)
	}
	if models.E5.Revision != frozenTextModelRevision {
		t.Fatalf(
			"canonical text revision drifted from %q to %q (registry owner: models.CanonicalTextModelRevision). A revision change is a schema migration; document it.",
			frozenTextModelRevision, models.E5.Revision,
		)
	}
	if !models.E5.Enabled {
		t.Fatalf("canonical text model %q must stay enabled (CORE set)", models.E5.ID)
	}
}

// TestTextModelMigrationRequiresDocumentation is the migration gate: when
// the frozen E5-base contract is changed, the change is rejected unless a
// committed migration document is referenced and actually exists. This
// makes the required migration explicit and reviewed instead of a silent
// value edit.
func TestTextModelMigrationRequiresDocumentation(t *testing.T) {
	frozen := models.E5.ID == frozenTextModelID && models.E5.Dimensions == frozenTextModelDims
	if frozen {
		return
	}

	if textModelMigrationDocumentRef == "" {
		t.Fatalf(
			"canonical text model drifted from frozen E5-base (%s/%d) to %s/%d WITHOUT a documented migration. This changes the vector space (e.g. e5-large-instruct = 1024 dims): new contract hash → new Qdrant collection → full re-embedding + reindex → blue/green alias switch. Set textModelMigrationDocumentRef to a committed migration document before changing the text model.",
			frozenTextModelID, frozenTextModelDims, models.E5.ID, models.E5.Dimensions,
		)
	}

	docPath := filepath.Join(repositoryRoot(t), textModelMigrationDocumentRef)
	if _, err := os.Stat(docPath); err != nil {
		t.Fatalf(
			"text model migration document %q (resolved to %q) does not exist: %v. The migration must be documented and committed before the text model can change.",
			textModelMigrationDocumentRef, docPath, err,
		)
	}
}
