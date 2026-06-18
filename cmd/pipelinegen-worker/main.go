package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
	"github.com/Marcuss-ops/PipelineGen/internal/logger"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log := logger.Get()
	defer logger.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	brokerURL := strings.TrimSpace(os.Getenv("VELOX_BROKER_URL"))
	if brokerURL == "" {
		brokerURL = "http://127.0.0.1:18080"
	}
	token := os.Getenv("VELOX_WORKER_TOKEN")
	broker := jobbrokerclient.New(brokerURL, token)

	workerID := envOr("VELOX_WORKER_ID", hostnameFallback())
	workerName := envOr("VELOX_WORKER_NAME", workerID)
	version := envOr("VELOX_WORKER_VERSION", "dev")
	caps := parseCaps(os.Getenv("VELOX_WORKER_CAPABILITIES"))

	workspaceRoot := filepath.Join(os.TempDir(), "pipelinegen", "jobs")
	_ = os.MkdirAll(workspaceRoot, 0755)
	log.Info("worker workspace ready", zap.String("workspace_root", workspaceRoot))

	session, err := broker.RegisterWorker(ctx, job.RegisterWorkerCommand{
		WorkerID:     workerID,
		Name:         workerName,
		Version:      version,
		Hostname:     hostnameFallback(),
		Capabilities: caps,
		SessionTTL:   90 * time.Second,
	})
	if err != nil {
		log.Fatal("failed to register worker", zap.Error(err))
	}
	log.Info("worker registered", zap.String("worker_id", session.WorkerID), zap.String("session_id", session.SessionID))

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := broker.Heartbeat(ctx, job.HeartbeatCommand{
				WorkerID:        workerID,
				WorkerSessionID: session.SessionID,
				SessionTTL:      90 * time.Second,
			}); err != nil {
				log.Warn("heartbeat failed", zap.Error(err))
			}
		}
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func hostnameFallback() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func parseCaps(raw string) job.WorkerCapabilities {
	if strings.TrimSpace(raw) == "" {
		return job.WorkerCapabilities{}
	}
	var caps job.WorkerCapabilities
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		return job.WorkerCapabilities{}
	}
	return caps
}
