package main

import (
	"context"
	"fmt"
	"log"

	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Get()
	logger, _ := zap.NewDevelopment()

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), logger)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer sqliteDB.Close()

	store := qdrant.NewSQLiteAssetStore(sqliteDB.DB)
	ids, err := store.ListAllAssetIDs(context.Background())
	if err != nil {
		log.Fatalf("list ids: %v", err)
	}

	mapper := qdrant.NewPayloadMapper(store, logger)
	schema := qdrant.DefaultV3Schema()

	failedCount := 0
	for _, id := range ids {
		asset, err := mapper.FetchAsset(context.Background(), id)
		if err != nil {
			fmt.Printf("FetchAsset failed for %s: %v\n", id, err)
			failedCount++
			continue
		}
		_, err = mapper.AssetToPoint(asset, schema)
		if err != nil {
			fmt.Printf("AssetToPoint failed for %s: %v\n", id, err)
			failedCount++
		}
	}
	fmt.Printf("Scan complete. Total assets: %d, Failed: %d\n", len(ids), failedCount)
}
