package clipindexer

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// Config holds clipindexer configuration
type Config struct {
	Enabled               bool   `yaml:"enabled"`
	ServerURL             string `yaml:"server_url"`
	ScriptPath            string `yaml:"script_path"`
	PythonBin             string `yaml:"python_bin"`
	DBPath                string `yaml:"db_path"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist"`
	// MaxConcurrentIndexing limits parallel Python subprocesses for clip indexing.
	MaxConcurrentIndexing int `yaml:"max_concurrent_indexing"`
}

// DefaultConfig returns default clipindexer config
func DefaultConfig() *Config {
	return &Config{
		Enabled:               true,
		ServerURL:             "http://127.0.0.1:8001",
		ScriptPath:            "scripts/start_embedding_server.sh",
		PythonBin:             "python3",
		AutoIndexAfterArtlist: true,
		MaxConcurrentIndexing: 10,
	}
}

// Service provides clip indexing functionality
//
// PG-016 typed-handle migration (June 2026): the embedded *sql.DB handle
// is now reached via *storage.SQLiteDB; method promotion (db.QueryContext,
// db.ExecContext, db.BeginTx) resolves cleanly because *storage.SQLiteDB
// embeds *sql.DB. This closes the last intentional *sql.DB escape hatch
// inside internal/app/ test code (worker_registry_e2e_test.go).
type Service struct {
	db          *storage.SQLiteDB
	dbPath      string
	cfg         *Config
	log         *zap.Logger
	scriptPath  string
	vectorStore VectorStoreIndexer
}

// NewService constructs a clip indexer bound to a database path and script directory.
//
// PG-016 typed-handle migration (June 2026): db is now *storage.SQLiteDB
// (see Service.db doc comment); body unchanged because method promotion
// resolves all *sql.DB methods transparently.
func NewService(cfg *Config, db *storage.SQLiteDB, dbPath string, log *zap.Logger) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Service{
		db:         db,
		dbPath:     dbPath,
		cfg:        cfg,
		log:        log,
		scriptPath: cfg.ScriptPath,
	}
}

// Compile-time assertions: Service satisfies the canonical indexing ports.
var _ MediaIndexer = (*Service)(nil)
var _ IndexEligibilityResolver = (*Service)(nil)

// Eligibility resolves the searchability decision for an asset via the
// canonical mediaregistry resolver. A missing row or an empty taxonomy
// resolves to REGISTERED (fail-closed: an asset is only SEARCHABLE once
// explicitly classified as video/image).
func (s *Service) Eligibility(ctx context.Context, assetID string) (capregistry.IndexEligibility, error) {
	return capregistry.ResolveIndexEligibility(ctx, s.db, assetID)
}

func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}

// VectorStore returns the configured vector store indexer, if any.
func (s *Service) VectorStore() VectorStoreIndexer {
	return s.vectorStore
}

// StartServer starts the Python embedding server as a background process
func (s *Service) StartServer(ctx context.Context) error {
	if !s.cfg.Enabled || s.cfg.ServerURL == "" {
		return nil
	}

	// Check if server is already running
	if s.checkServer(ctx) {
		s.log.Info("embedding server already running")
		return nil
	}

	// Start the compute-only sidecar launcher. The launcher is not a clip
	// indexing bridge and has no database access.
	serverScript, err := filepath.Abs(s.scriptPath)
	if err != nil {
		return fmt.Errorf("resolve embedding launcher: %w", err)
	}
	s.log.Info("starting embedding server", zap.String("script", serverScript))

	cmd := exec.CommandContext(ctx, serverScript)
	cmd.Dir = filepath.Dir(serverScript)

	// Start the process in the background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start embedding server: %w", err)
	}

	s.log.Info("embedding server process started", zap.Int("pid", cmd.Process.Pid))
	return nil
}

// StartWatchdog starts a background goroutine to monitor and restart the server if it fails
func (s *Service) StartWatchdog(ctx context.Context) {
	if !s.cfg.Enabled || s.cfg.ServerURL == "" {
		return
	}

	concurrent.SafeGo("clipindexer-watchdog", func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.checkServer(ctx) {
					s.log.Warn("embedding server health check failed, restarting...")
					if err := s.StartServer(ctx); err != nil {
						s.log.Error("watchdog failed to restart server", zap.Error(err))
					}
				}
			}
		}
	})
}

func (s *Service) checkServer(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/health", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
