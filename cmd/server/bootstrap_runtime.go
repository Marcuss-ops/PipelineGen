package main

import (
	"os"
	"strings"

	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/queue"
	"velox/go-master/internal/storage/jsondb"
	pgstorage "velox/go-master/internal/storage/postgres"
	"velox/go-master/pkg/config"
)

// runtimeStorage is the minimal combined storage contract required by job + worker services.
type runtimeStorage interface {
	job.StorageInterface
	worker.StorageInterface
	Close() error
}

func selectStorageBackend(cfg *config.Config) string {
	if v := strings.TrimSpace(os.Getenv("VELOX_STORAGE_BACKEND")); v != "" {
		return strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("VELOX_DB_DSN")); v != "" {
		return "postgres"
	}
	return "json"
}

func buildRuntimeStorage(cfg *config.Config) (runtimeStorage, error) {
	switch selectStorageBackend(cfg) {
	case "postgres":
		return pgstorage.NewStorage(strings.TrimSpace(os.Getenv("VELOX_DB_DSN")))
	case "json", "":
		fallthrough
		default:
		return jsondb.NewStorage(cfg.Storage.DataDir)
	}
}

func selectQueueBackend() queue.Backend {
	if v := strings.TrimSpace(os.Getenv("VELOX_QUEUE_BACKEND")); v != "" {
		switch strings.ToLower(v) {
		case "redis", "redis-streams":
			return queue.BackendRedisStreams
		case "nats":
			return queue.BackendNATS
		case "json":
			return queue.BackendJSON
		case "postgres":
			return queue.BackendPostgres
		}
	}
	// Default to postgres if DSN is present but backend not specified
	if strings.TrimSpace(os.Getenv("VELOX_DB_DSN")) != "" {
		return queue.BackendPostgres
	}
	return queue.BackendNoop
}

func buildQueueBackend(s runtimeStorage) queue.Queue {
	backend := selectQueueBackend()
	
	if backend == queue.BackendPostgres {
		if pg, ok := s.(*pgstorage.Storage); ok {
			return queue.NewPostgresQueue(pg.GetDB())
		}
	}

	// Real transports will replace this switch incrementally.
	return queue.NewNoopQueue()
}
