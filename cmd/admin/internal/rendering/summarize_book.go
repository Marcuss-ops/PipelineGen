package rendering

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func parseDriveFileID(input string) string {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "/") {
		return input // raw ID
	}
	// Try parsing /file/d/FILE_ID/
	if idx := strings.Index(input, "/file/d/"); idx != -1 {
		parts := strings.Split(input[idx+8:], "/")
		if len(parts) > 0 {
			return strings.Split(parts[0], "?")[0]
		}
	}
	// Try open?id=FILE_ID
	if idx := strings.Index(input, "id="); idx != -1 {
		return strings.Split(input[idx+3:], "&")[0]
	}
	return input
}

func RunSummarizeBook(args []string) error {
	fs := flag.NewFlagSet("summarize-book", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	filePath := fs.String("file", "", "Path or Google Drive URL/ID of the PDF or EPUB book file (required)")
	model := fs.String("model", "gemma", "Ollama model name (e.g. gemma, gemma2, llama3)")
	chunkSize := fs.Int("chunk-size", 24000, "Maximum characters per chunk (default: 24000)")
	ollamaURL := fs.String("ollama-url", "http://127.0.0.1:11434", "Ollama API endpoint URL")
	pushDocs := fs.Bool("push-docs", false, "Upload summary to Google Docs")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for Docs upload")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *filePath == "" {
		return fmt.Errorf("missing required flag: --file")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, coreCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize core services", zap.Error(err))
	}
	defer coreCleanup()

	ctx := cli.CmdContext()

	// 1. Detect and handle Google Drive URL/ID
	isDriveFile := false
	fileID := ""
	if strings.Contains(*filePath, "drive.google.com") || len(*filePath) > 20 && !strings.Contains(*filePath, ".") && !strings.Contains(*filePath, string(filepath.Separator)) {
		isDriveFile = true
		fileID = parseDriveFileID(*filePath)
	}

	targetFilePath := *filePath

	if isDriveFile {
		if root.Drive == nil || root.Drive.Reader == nil {
			return fmt.Errorf("google drive reader port is not authenticated or configured. Please authenticate first")
		}

		fmt.Printf("Detecting Google Drive File ID: %s...\n", fileID)

		// Get metadata to resolve original filename
		meta, err := root.Drive.Reader.GetFileMeta(ctx, fileID)
		if err != nil {
			return fmt.Errorf("failed to retrieve Google Drive file metadata: %w", err)
		}

		fmt.Printf("File Name: %s (%s)\n", meta.Name, meta.MimeType)

		// Create downloads folder
		downloadsDir := filepath.Join(cfg.Storage.DataDir, "downloads")
		if err := os.MkdirAll(downloadsDir, 0755); err != nil {
			return fmt.Errorf("failed to create downloads directory: %w", err)
		}

		targetFilePath = filepath.Join(downloadsDir, meta.Name)
		fmt.Printf("Downloading file to: %s...\n", targetFilePath)

		// Perform Download
		res, _, err := root.Drive.Reader.DownloadFile(ctx, fileID)
		if err != nil {
			return fmt.Errorf("failed to initiate Google Drive download: %w", err)
		}
		defer res.Close()

		out, err := os.Create(targetFilePath)
		if err != nil {
			return fmt.Errorf("failed to create local file: %w", err)
		}

		_, err = io.Copy(out, res)
		out.Close() // Close before using
		if err != nil {
			return fmt.Errorf("failed to write file content: %w", err)
		}
		fmt.Println("✅ Download complete!")
	}

	// 2. Locate python script relative to paths
	scriptsDir := cfg.Paths.PythonScriptsDir
	if scriptsDir == "" {
		scriptsDir = "scripts"
	}
	scriptPath := filepath.Join(scriptsDir, "book_summarizer.py")

	absScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for script: %w", err)
	}

	absFilePath, err := filepath.Abs(targetFilePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for file: %w", err)
	}

	// Python binary defaults from config
	pythonBin := cfg.ClipIndexer.PythonBin
	if pythonBin == "" {
		pythonBin = "python3"
	}

	fmt.Printf("\n=== Book Summarizer Admin Tool ===\n")
	fmt.Printf("File:         %s\n", absFilePath)
	fmt.Printf("Model:        %s\n", *model)
	fmt.Printf("Chunk Size:   %d characters\n", *chunkSize)
	fmt.Printf("Ollama URL:   %s\n", *ollamaURL)
	fmt.Println()

	cmdArgs := []string{
		absScriptPath,
		"--file", absFilePath,
		"--model", *model,
		"--chunk-size", strconv.Itoa(*chunkSize),
		"--ollama-url", *ollamaURL,
	}

	cmd := exec.CommandContext(ctx, pythonBin, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = filepath.Dir(absScriptPath)

	log.Info("executing book summarizer python script", zap.String("file", absFilePath), zap.String("model", *model))

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run book summarizer script: %w", err)
	}

	if *pushDocs {
		if root.Drive.DocClient == nil {
			return fmt.Errorf("google docs client not initialized. check credentials")
		}

		summaryPath := strings.TrimSuffix(absFilePath, filepath.Ext(absFilePath)) + "_summary.txt"
		content, err := os.ReadFile(summaryPath)
		if err != nil {
			return fmt.Errorf("failed to read generated summary: %w", err)
		}

		fmt.Printf("\nUploading summary to Google Docs...\n")
		docTitle := fmt.Sprintf("Summary: %s", filepath.Base(absFilePath))
		doc, err := root.Drive.DocClient.CreateDoc(ctx, docTitle, string(content), *driveFolderID)
		if err != nil {
			return fmt.Errorf("failed to create Google Doc: %w", err)
		}

		fmt.Printf("✅ Summary uploaded successfully!\n")
		fmt.Printf("📄 Google Doc URL: %s\n", doc.URL)
	}

	return nil
}
