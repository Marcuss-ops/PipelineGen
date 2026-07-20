package main

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runCheckDBCartoonFiles(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()

	ctx := context.Background()

	query := `SELECT id, name, filename, category, group_name, parent_folder_id, metadata_json 
	          FROM media_assets 
	          WHERE filename LIKE '%yeet%' OR filename LIKE '%among%' OR filename LIKE '%disappointed%' 
	             OR filename LIKE '%auggh%' OR filename LIKE '%awkward%' OR filename LIKE '%bonk%' 
	             OR filename LIKE '%bruh%' OR filename LIKE '%core%'`

	rows, err := root.DB.DB.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("=== Matching Files in SQLite Database ===")
	for rows.Next() {
		var id, name, filename, category, groupName, parentFolderID, metadataJSON string
		if err := rows.Scan(&id, &name, &filename, &category, &groupName, &parentFolderID, &metadataJSON); err != nil {
			return err
		}
		fmt.Printf("DB File: %q (ID: %s, Name: %s, Category: %s, Group: %s, ParentFolder: %s)\n", filename, id, name, category, groupName, parentFolderID)
	}

	return nil
}
