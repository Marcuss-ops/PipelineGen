package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN")
	if dsn == "" {
		fmt.Println("PIPELINEGEN_MEDIA_POSTGRES_DSN is not set")
		os.Exit(2)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, id := range strings.Split(os.Getenv("OVL06_IDS"), ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		var localPath, driveFileID string
		err := db.QueryRowContext(ctx, `SELECT COALESCE(local_path,''), COALESCE(drive_file_id,'') FROM media_assets WHERE id=$1`, id).Scan(&localPath, &driveFileID)
		if err != nil {
			fmt.Printf("%s ERR %v\n", id, err)
			continue
		}
		fmt.Printf("%s | local=%s | drive=%s\n", id, localPath, driveFileID)
	}
}
