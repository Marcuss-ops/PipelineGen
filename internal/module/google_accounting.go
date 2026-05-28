package module

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"

	gaHandler "velox/go-master/internal/api/handlers/google_accounting"
	"velox/go-master/internal/config"
)

func NewGoogleAccountingModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *gaHandler.Handler,
) *RouteModule {
	var cmd *exec.Cmd

	return NewRouteModule(
		"google-accounting",
		func(cfg *config.Config) bool { return cfg.Features.GoogleAccountingEnabled },
		"/google-accounting",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			if !cfg.GoogleAccounting.Enabled {
				return nil
			}
			if checkGAServer(cfg.GoogleAccounting.ServerURL) {
				log.Info("google-accounting server already running")
				return nil
			}

			cmd = exec.Command("uvicorn", "main:app", "--port", "8000")
			cmd.Dir = "google-accounting"
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				log.Warn("google-accounting server start failed (non-fatal, external server may be running)", zap.Error(err))
				return nil
			}
			log.Info("google-accounting server started", zap.Int("pid", cmd.Process.Pid))

			// Watchdog: restart if it crashes
			go func() {
				ticker := time.NewTicker(1 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if !checkGAServer(cfg.GoogleAccounting.ServerURL) {
							log.Warn("google-accounting server down, restarting...")
							cmd = exec.Command("uvicorn", "main:app", "--port", "8000")
							cmd.Dir = "google-accounting"
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr
							if err := cmd.Start(); err != nil {
								log.Error("watchdog restart failed", zap.Error(err))
							} else {
								log.Info("google-accounting server restarted", zap.Int("pid", cmd.Process.Pid))
							}
						}
					}
				}
			}()

			return nil
		}),
		WithStop(func(ctx context.Context) error {
			if cmd != nil && cmd.Process != nil {
				log.Info("stopping google-accounting server")
				if err := cmd.Process.Kill(); err != nil {
					return fmt.Errorf("google-accounting server kill failed: %w", err)
				}
				cmd.Wait()
				log.Info("google-accounting server stopped")
			}
			return nil
		}),
	)
}

func checkGAServer(serverURL string) bool {
	url := strings.TrimRight(serverURL, "/") + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
