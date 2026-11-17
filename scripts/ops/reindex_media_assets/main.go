// Command reindex_media_assets enqueues the canonical asset.index.requested
// event for every asset of an ops job state file.
//
// It deliberately bypasses the operator HTTP reindex surface: that handler
// re-upserts the asset through CommitAndIndex with a partial request (zero
// duration/tags/metadata), which would overwrite the committed row. The
// ReindexRequester used here only enqueues the event; PostgresIndexWorker
// re-embeds the LIVE search_text and stamps the indexed snapshot.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type stateFile struct {
	Videos map[string]struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	} `json:"videos"`
}

func main() {
	statePath := flag.String("state", "", "ops job state.json containing videos[].items[].id (required)")
	dsnFlag := flag.String("dsn", "", "media PostgreSQL DSN (default: PIPELINEGEN_MEDIA_POSTGRES_DSN or --env-file)")
	envFile := flag.String("env-file", ".env", "env file to read the DSN from when the environment is unset")
	filter := flag.String("filter", "", "only reindex asset ids containing this substring")
	dryRun := flag.Bool("dry-run", false, "print the selected ids without enqueuing")
	limit := flag.Int("limit", 0, "maximum number of assets to enqueue (0 = all)")
	flag.Parse()

	if strings.TrimSpace(*statePath) == "" {
		fatal("--state is required")
	}
	raw, err := os.ReadFile(*statePath)
	if err != nil {
		fatal("read state: %v", err)
	}
	var state stateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		fatal("decode state: %v", err)
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, 256)
	for _, video := range state.Videos {
		for _, item := range video.Items {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			if *filter != "" && !strings.Contains(id, *filter) {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if *limit > 0 && len(ids) > *limit {
		ids = ids[:*limit]
	}
	if len(ids) == 0 {
		fatal("no asset ids selected from %s", *statePath)
	}
	fmt.Printf("selected %d assets\n", len(ids))
	if *dryRun {
		for _, id := range ids {
			fmt.Println(id)
		}
		return
	}

	dsn := strings.TrimSpace(*dsnFlag)
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN"))
	}
	if dsn == "" {
		dsn = readEnvValue(*envFile, "PIPELINEGEN_MEDIA_POSTGRES_DSN")
	}
	if dsn == "" {
		fatal("media PostgreSQL DSN not found (use --dsn or PIPELINEGEN_MEDIA_POSTGRES_DSN)")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fatal("open media postgres: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fatal("ping media postgres: %v", err)
	}

	requester := pgmedia.NewReindexRequester(db)
	if requester == nil {
		fatal("media reindex requester is not wired")
	}
	queued, failed := 0, 0
	for i, id := range ids {
		if err := requester.RequestIndex(ctx, id); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  %s: %v\n", id, err)
		} else {
			queued++
		}
		if (i+1)%20 == 0 {
			fmt.Printf("  enqueued %d/%d\n", i+1, len(ids))
		}
	}
	fmt.Printf("done: queued=%d failed=%d\n", queued, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func readEnvValue(path, key string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, key+"=") {
			continue
		}
		value := strings.TrimPrefix(line, key+"=")
		value = strings.Trim(value, `"'`)
		return strings.TrimSpace(value)
	}
	return ""
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
