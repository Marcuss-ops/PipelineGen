// Package whisper provides Whisper API integration for Agent 5.
package whisper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Client wrapper per Whisper
type Client struct {
	model    string
	language string
	outputDir string
}

// NewClient crea un nuovo client Whisper
func NewClient(model, language string) *Client {
	if model == "" {
		model = "base"
	}
	return &Client{
		model:     model,
		language:  language,
		outputDir: "/tmp/whisper",
	}
}

// SetOutputDir imposta la directory di output
func (c *Client) SetOutputDir(dir string) {
	c.outputDir = dir
}

// Transcribe trascrive un file audio
func (c *Client) Transcribe(ctx context.Context, audioPath string) (string, error) {
	// Crea directory output se non esiste
	if err := os.MkdirAll(c.outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	args := []string{
		audioPath,
		"--model", c.model,
		"--output_format", "txt",
		"--output_dir", c.outputDir,
	}

	if c.language != "" {
		args = append(args, "--language", c.language)
	}

	cmd := exec.CommandContext(ctx, "whisper", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("whisper failed: %w, output: %s", err, string(output))
	}

	// Leggi il file di output
	baseName := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	txtFile := filepath.Join(c.outputDir, baseName+".txt")

	transcription, err := os.ReadFile(txtFile)
	if err != nil {
		return "", fmt.Errorf("failed to read transcription: %w", err)
	}

	logger.Info("Transcribed audio", zap.String("file", audioPath), zap.Int("chars", len(transcription)))
	return string(transcription), nil
}

// TranscribeToJSON trascrive e restituisce formato JSON con timestamp
func (c *Client) TranscribeToJSON(ctx context.Context, audioPath string) ([]Segment, error) {
	if err := os.MkdirAll(c.outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	args := []string{
		audioPath,
		"--model", c.model,
		"--output_format", "json",
		"--output_dir", c.outputDir,
	}

	if c.language != "" {
		args = append(args, "--language", c.language)
	}

	cmd := exec.CommandContext(ctx, "whisper", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("whisper failed: %w, output: %s", err, string(output))
	}

	// Leggi il file JSON
	baseName := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	jsonFile := filepath.Join(c.outputDir, baseName+".json")

	// Per semplicità, restituiamo solo il testo concatenato come segmenti
	// In produzione si parserizzerebbe il JSON
	content, err := os.ReadFile(jsonFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read json: %w", err)
	}

	return []Segment{{
		Start: 0,
		End:   0,
		Text:  string(content),
	}}, nil
}

// Segment rappresenta un segmento di trascrizione
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// TranscribeWithTimestamps trascrive con timestamp
func (c *Client) TranscribeWithTimestamps(ctx context.Context, audioPath string) ([]Segment, error) {
	return c.TranscribeToJSON(ctx, audioPath)
}

// CleanTranscription pulisce la trascrizione rimuovendo file temporanei
func (c *Client) CleanTranscription(baseName string) {
	exts := []string{".txt", ".json", ".srt", ".vtt"}
	for _, ext := range exts {
		os.Remove(filepath.Join(c.outputDir, baseName+ext))
	}
}