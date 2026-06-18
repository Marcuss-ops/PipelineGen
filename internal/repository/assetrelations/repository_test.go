package assetrelations

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS asset_relations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    source_asset_id TEXT NOT NULL,
    target_asset_id TEXT NOT NULL,
    relation_type   TEXT NOT NULL,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (source_asset_id, target_asset_id, relation_type),
    CHECK (source_asset_id != target_asset_id)
);
`

func newTestRepo(t *testing.T) (*Repository, func()) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema)
	return NewRepository(db), func() { db.Close() }
}

func TestCreateAndGetBySource(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	rel := Relation{
		SourceAssetID: "script_1",
		TargetAssetID: "video_1",
		RelationType:  RelationDerivedFrom,
		MetadataJSON:  `{"scene":3}`,
	}
	if err := repo.Create(ctx, rel); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	rels, err := repo.GetBySource(ctx, "script_1")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].TargetAssetID != "video_1" {
		t.Errorf("target: got %s", rels[0].TargetAssetID)
	}
	if rels[0].RelationType != RelationDerivedFrom {
		t.Errorf("type: got %s", rels[0].RelationType)
	}
	if rels[0].CreatedAt.IsZero() {
		t.Error("created_at should be populated")
	}
}

func TestCreateFailsForSelfRelation(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "same_asset",
		TargetAssetID: "same_asset",
		RelationType:  RelationPartOf,
	})
	if err == nil {
		t.Fatal("expected error for self-relation")
	}
}

func TestCreateFailsForInvalidType(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "a",
		TargetAssetID: "b",
		RelationType:  "invalid_type_xyz",
	})
	if err == nil {
		t.Fatal("expected error for invalid relation type")
	}
}

func TestCreateFailsForDuplicate(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Relation{
		SourceAssetID: "a",
		TargetAssetID: "b",
		RelationType:  RelationDerivedFrom,
	})
	err := repo.Create(ctx, Relation{
		SourceAssetID: "a",
		TargetAssetID: "b",
		RelationType:  RelationDerivedFrom,
	})
	if err == nil {
		t.Fatal("expected error for duplicate relation")
	}
}

func TestValidRelationTypes(t *testing.T) {
	types := ValidRelationTypes()
	if len(types) != 5 {
		t.Fatalf("expected 5 relation types, got %d: %v", len(types), types)
	}

	for _, rt := range types {
		if !IsValidRelationType(rt) {
			t.Errorf("IsValidRelationType should return true for %q", rt)
		}
	}
	if IsValidRelationType("") {
		t.Error("IsValidRelationType should return false for empty string")
	}
	if IsValidRelationType("invalid") {
		t.Error("IsValidRelationType should return false for 'invalid'")
	}
}

func TestGetByTarget(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Relation{SourceAssetID: "clip_a", TargetAssetID: "video_x", RelationType: RelationPartOf})
	repo.Create(ctx, Relation{SourceAssetID: "clip_b", TargetAssetID: "video_x", RelationType: RelationPartOf})

	rels, err := repo.GetByTarget(ctx, "video_x")
	if err != nil {
		t.Fatalf("GetByTarget: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(rels))
	}
}

func TestGetByType(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	now := time.Now()
	repo.Create(ctx, Relation{SourceAssetID: "a", TargetAssetID: "b", RelationType: RelationDerivedFrom, CreatedAt: now})
	repo.Create(ctx, Relation{SourceAssetID: "c", TargetAssetID: "d", RelationType: RelationDerivedFrom, CreatedAt: now})
	repo.Create(ctx, Relation{SourceAssetID: "e", TargetAssetID: "f", RelationType: RelationReplaces, CreatedAt: now})

	rels, err := repo.GetByType(ctx, RelationDerivedFrom)
	if err != nil {
		t.Fatalf("GetByType: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 derived_from relations, got %d", len(rels))
	}
}

func TestGetByTypeInvalid(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.GetByType(ctx, "bogus_type")
	if err == nil {
		t.Fatal("expected error for GetByType with invalid type")
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Relation{SourceAssetID: "a", TargetAssetID: "b", RelationType: RelationDerivedFrom})

	if err := repo.Delete(ctx, "a", "b", RelationDerivedFrom); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	rels, _ := repo.GetBySource(ctx, "a")
	if len(rels) != 0 {
		t.Fatalf("expected 0 relations after delete, got %d", len(rels))
	}
}

func TestDeleteAllForSource(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Relation{SourceAssetID: "a", TargetAssetID: "b", RelationType: RelationUsedIn})
	repo.Create(ctx, Relation{SourceAssetID: "a", TargetAssetID: "c", RelationType: RelationUsedIn})

	if err := repo.DeleteAllForSource(ctx, "a"); err != nil {
		t.Fatalf("DeleteAllForSource: %v", err)
	}

	rels, _ := repo.GetBySource(ctx, "a")
	if len(rels) != 0 {
		t.Fatalf("expected 0 after DeleteAllForSource, got %d", len(rels))
	}
}

func TestDeleteAllForTarget(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Relation{SourceAssetID: "x", TargetAssetID: "z", RelationType: RelationPartOf})
	repo.Create(ctx, Relation{SourceAssetID: "y", TargetAssetID: "z", RelationType: RelationPartOf})

	if err := repo.DeleteAllForTarget(ctx, "z"); err != nil {
		t.Fatalf("DeleteAllForTarget: %v", err)
	}

	rels, _ := repo.GetByTarget(ctx, "z")
	if len(rels) != 0 {
		t.Fatalf("expected 0 after DeleteAllForTarget, got %d", len(rels))
	}
}

func TestCreate_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "script_x",
		TargetAssetID: "video_y",
		RelationType:  RelationDerivedFrom,
		MetadataJSON:  "not valid json",
	})
	if err == nil {
		t.Fatal("expected error for invalid metadata_json")
	}
}

func TestCreate_ValidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "script_v",
		TargetAssetID: "video_w",
		RelationType:  RelationSourceOf,
		MetadataJSON:  `{"pipeline":"v2","scene":5}`,
	})
	if err != nil {
		t.Fatalf("expected success for valid metadata_json, got: %v", err)
	}

	rels, _ := repo.GetBySource(ctx, "script_v")
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].MetadataJSON != `{"pipeline":"v2","scene":5}` {
		t.Errorf("metadata_json mismatch: got %s", rels[0].MetadataJSON)
	}
}

func TestCreate_EmptyJSONAllowed(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "script_e",
		TargetAssetID: "video_f",
		RelationType:  RelationDerivedFrom,
		MetadataJSON:  "",
	})
	if err != nil {
		t.Fatalf("empty metadata_json should be allowed: %v", err)
	}
}

func TestCreate_EmptyObjectJSONAllowed(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Relation{
		SourceAssetID: "script_o",
		TargetAssetID: "video_p",
		RelationType:  RelationDerivedFrom,
		MetadataJSON:  "{}",
	})
	if err != nil {
		t.Fatalf(`"{}" metadata_json should be allowed: %v`, err)
	}
}
