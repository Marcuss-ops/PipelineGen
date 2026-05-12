package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

const apiBase = "http://127.0.0.1:8080"
const searchTerm = "nature"
const searchLimit = 3

type artlistClip struct {
	ClipID string `json:"clip_id"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Name   string `json:"name"`
}

type searchLiveResponse struct {
	OK    bool          `json:"ok"`
	Term  string        `json:"term"`
	Clips []artlistClip `json:"clips"`
}

type runResponse struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

type statusResponse struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	Found     int    `json:"found"`
	Processed int    `json:"processed"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
}

func main() {
	fmt.Println("=== Artlist Pipeline Verification Tool ===")
	fmt.Println()

	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	deps, err := bootstrap.WireServices(cfg, log, "")
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		os.Exit(1)
	}
	if deps.Cleanup != nil {
		defer deps.Cleanup()
	}

	allOK := true

	// Step 1: Live Search
	fmt.Print("Step 1: Live Artlist Search... ")
	clips, err := doLiveSearch()
	if err != nil {
		fmt.Printf("FAILED: %v\n", err)
		allOK = false
	} else {
		fmt.Printf("OK (%d clips found)\n", len(clips))
		for _, c := range clips {
			id := c.ClipID
			if id == "" {
				id = c.ID
			}
			name := c.Title
			if name == "" {
				name = c.Name
			}
			fmt.Printf("  - %s: %s\n", id, name)
		}
	}

	if !allOK {
		fmt.Println("\n❌ Verification FAILED at search step")
		os.Exit(1)
	}

	// Step 2: Run Pipeline
	fmt.Print("Step 2: Starting Artlist pipeline run... ")
	runID, err := startRun()
	if err != nil {
		fmt.Printf("FAILED: %v\n", err)
		allOK = false
	} else {
		fmt.Printf("OK (run_id=%s)\n", runID)
	}

	if !allOK {
		fmt.Println("\n❌ Verification FAILED at run step")
		os.Exit(1)
	}

	// Step 3: Poll until completion
	fmt.Println("Step 3: Waiting for job completion...")
	status, err := pollRunStatus(runID)
	if err != nil {
		fmt.Printf("  FAILED: %v\n", err)
		allOK = false
	} else {
		fmt.Printf("  Status: %s (found=%d, processed=%d, skipped=%d, failed=%d)\n",
			status.Status, status.Found, status.Processed, status.Skipped, status.Failed)
	}

	if !allOK {
		fmt.Println("\n❌ Verification FAILED at polling step")
		os.Exit(1)
	}

	if status.Status != "completed" {
		fmt.Printf("  ❌ Job did not complete successfully (status=%s, error=%s)\n", status.Status, status.Error)
		allOK = false
	} else {
		fmt.Println("  ✅ Job completed successfully")
	}

	// Step 4: Verify job in DB
	fmt.Print("Step 4: Verifying job in database... ")
	if err := verifyJobInDB(runID, cfg); err != nil {
		fmt.Printf("FAILED: %v\n", err)
		allOK = false
	} else {
		fmt.Println("✅ OK")
	}

	// Step 5: Verify clip indexing (if clips were processed)
	if status.Processed > 0 {
		fmt.Print("Step 5: Verifying clip indexing... ")
		if err := verifyClipIndexing(cfg); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			allOK = false
		} else {
			fmt.Println("✅ OK")
		}
	} else {
		fmt.Printf("Step 5: Skipping clip indexing verification (no new clips processed)\n")
	}

	fmt.Println()
	if allOK {
		fmt.Println("✅ ALL CHECKS PASSED - Artlist pipeline is working correctly")
		os.Exit(0)
	} else {
		fmt.Println("❌ SOME CHECKS FAILED")
		os.Exit(1)
	}
}

func doLiveSearch() ([]artlistClip, error) {
	// Try posting
	payload := fmt.Sprintf(`{"term":"%s","limit":%d}`, searchTerm, searchLimit)
	req, err := http.NewRequest("POST", apiBase+"/api/artlist/search/live", bytes.NewBufferString(payload))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		// Fallback: also check with query params
		qreq, qErr := http.NewRequest("POST", fmt.Sprintf("%s/api/artlist/search/live?term=%s&limit=%d", apiBase, searchTerm, searchLimit), nil)
		if qErr != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		resp, qErr = client.Do(qreq)
		if qErr != nil {
			return nil, fmt.Errorf("request failed (both methods): %w / %w", err, qErr)
		}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result searchLiveResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode failed: %w (body: %s)", err, string(body))
	}
	return result.Clips, nil
}

func startRun() (string, error) {
	payload := fmt.Sprintf(`{"term":"%s","limit":%d}`, searchTerm, searchLimit)
	req, err := http.NewRequest("POST", apiBase+"/api/artlist/run", bytes.NewBufferString(payload))
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result runResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode failed: %w (body: %s)", err, string(body))
	}

	if !result.OK {
		return "", fmt.Errorf("run not OK: %s (error: %s)", result.Status, result.Error)
	}
	return result.RunID, nil
}

func pollRunStatus(runID string) (*statusResponse, error) {
	deadline := time.Now().Add(10 * time.Minute)
	client := &http.Client{Timeout: 10 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/artlist/runs/%s", apiBase, runID))
		if err != nil {
			return nil, fmt.Errorf("fetch status failed: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result statusResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode failed: %w (body: %s)", err, string(body))
		}

		fmt.Printf("  Poll: status=%s found=%d processed=%d failed=%d\n",
			result.Status, result.Found, result.Processed, result.Failed)

		if result.Status == "completed" || strings.HasSuffix(result.Status, "completed") {
			return &result, nil
		}
		if result.Status == "failed" || result.Status == "cancelled" {
			return &result, fmt.Errorf("job ended with status=%s: %s", result.Status, result.Error)
		}

		time.Sleep(5 * time.Second)
	}

	return nil, fmt.Errorf("timeout waiting for job completion (10 min)")
}

func verifyJobInDB(runID string, cfg *config.Config) error {
	dbPath := filepath.Join(cfg.Storage.DataDir, "velox", "velox.db.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		// Try alternate path
		dbPath = filepath.Join(cfg.Storage.DataDir, "velox.db.sqlite")
		if _, err := os.Stat(dbPath); err != nil {
			return fmt.Errorf("cannot find velox db at %s/velox/velox.db.sqlite or %s/velox.db.sqlite: %w", cfg.Storage.DataDir, cfg.Storage.DataDir, err)
		}
	}

	cmd := exec.Command("sqlite3", dbPath,
		fmt.Sprintf("SELECT status FROM jobs WHERE id='%s'", runID))
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sqlite query failed: %w", err)
	}

	status := strings.TrimSpace(string(output))
	if status != "completed" {
		return fmt.Errorf("job status is '%s', expected 'completed'", status)
	}
	return nil
}

func verifyClipIndexing(cfg *config.Config) error {
	artlistDB := filepath.Join(cfg.Storage.DataDir, "artlist", "artlist.db.sqlite")
	if _, err := os.Stat(artlistDB); err != nil {
		artlistDB = filepath.Join(cfg.Storage.DataDir, "artlist.db.sqlite")
		if _, err := os.Stat(artlistDB); err != nil {
			return fmt.Errorf("cannot find artlist db: %w", err)
		}
	}

	// Get latest clip ID that was processed
	cmd := exec.Command("sqlite3", artlistDB,
		"SELECT id FROM clips WHERE search_terms IS NOT NULL ORDER BY updated_at DESC LIMIT 1")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sqlite query for latest clip failed: %w", err)
	}

	clipID := strings.TrimSpace(string(output))
	if clipID == "" {
		return fmt.Errorf("no clips found in artlist database")
	}

	// Run the index_clips.py script against this clip
	scriptsDir := cfg.Paths.PythonScriptsDir
	if scriptsDir == "" {
		scriptsDir = "scripts"
	}
	scriptPath := filepath.Join(scriptsDir, "index_clips.py")

	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("index_clips.py not found at %s: %w", scriptPath, err)
	}

	pythonCmd := exec.Command("python3", scriptPath, "--db", artlistDB, "--clip-id", clipID)
	pythonOutput, err := pythonCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("index_clips.py failed: %w (output: %s)", err, string(pythonOutput))
	}

	// Verify search_text was updated
	verifyCmd := exec.Command("sqlite3", artlistDB,
		fmt.Sprintf("SELECT search_text FROM clips WHERE id='%s'", clipID))
	verifyOutput, err := verifyCmd.Output()
	if err != nil {
		return fmt.Errorf("verify search_text failed: %w", err)
	}

	searchText := strings.TrimSpace(string(verifyOutput))
	if searchText == "" || searchText == "(null)" {
		return fmt.Errorf("search_text is empty for clip %s - indexing may have failed", clipID)
	}

	fmt.Printf("  Clip %s indexed: search_text present (%d chars)\n", clipID, len(searchText))
	return nil
}
