// Package tts provides edge-tts wrapper for TTS.
package tts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// EdgeTTS wrapper per edge-tts CLI
type EdgeTTS struct {
	outputDir string
}

// NewEdgeTTS crea un nuovo wrapper edge-tts
func NewEdgeTTS(outputDir string) *EdgeTTS {
	if outputDir == "" {
		outputDir = "/tmp/voiceovers"
	}
	os.MkdirAll(outputDir, 0755)
	return &EdgeTTS{outputDir: outputDir}
}

// Generate genera voiceover usando la voce di default per la lingua
func (e *EdgeTTS) Generate(ctx context.Context, text string, lang string) (*GenerationResult, error) {
	voice := GetDefaultVoice(lang)
	return e.GenerateWithVoice(ctx, text, voice)
}

// GenerateWithVoice genera voiceover usando una voce specifica
func (e *EdgeTTS) GenerateWithVoice(ctx context.Context, text string, voice string) (*GenerationResult, error) {
	// Crea file temporaneo per il testo
	textFile := filepath.Join(e.outputDir, fmt.Sprintf("temp_text_%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(textFile, []byte(text), 0644); err != nil {
		return nil, fmt.Errorf("failed to write text file: %w", err)
	}
	defer os.Remove(textFile)

	// Genera nome file output
	fileName := fmt.Sprintf("voiceover_%d_%s.mp3", time.Now().Unix(), strings.ReplaceAll(voice, "-", "_"))
	outputPath := filepath.Join(e.outputDir, fileName)

	// Costruisci comando edge-tts
	args := []string{
		"--file", textFile,
		"--voice", voice,
		"--write-media", outputPath,
	}

	logger.Info("Running edge-tts",
		zap.String("voice", voice),
		zap.String("output", outputPath),
	)

	cmd := exec.CommandContext(ctx, "edge-tts", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("edge-tts failed: %w, output: %s", err, string(output))
	}

	// Verifica che il file sia stato creato
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("output file not created: %s", outputPath)
	}

	wordCount := len(strings.Fields(text))
	lang := GetLanguageFromVoice(voice)

	logger.Info("Voiceover generated",
		zap.String("file", fileName),
		zap.Int("words", wordCount),
		zap.String("voice", voice),
	)

	return &GenerationResult{
		FilePath:  outputPath,
		FileName:  fileName,
		WordCount: wordCount,
		Duration:  estimateDuration(wordCount),
		VoiceUsed: voice,
		Language:  lang,
	}, nil
}

// GenerateFromScript genera voiceover da uno script (più paragrafi)
func (e *EdgeTTS) GenerateFromScript(ctx context.Context, script string, lang string) (*GenerationResult, error) {
	// Pulisci lo script
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, fmt.Errorf("script is empty")
	}

	return e.Generate(ctx, script, lang)
}

// GetOutputDir restituisce la directory di output
func (e *EdgeTTS) GetOutputDir() string {
	return e.outputDir
}

// estimateDuration stima la durata in secondi basata sul word count
func estimateDuration(wordCount int) int {
	// ~140 parole al minuto (speech rate medio)
	return (wordCount * 60) / 140
}

// CheckEdgeTTSAvailable verifica se edge-tts è installato
func CheckEdgeTTSAvailable() bool {
	cmd := exec.Command("edge-tts", "--version")
	err := cmd.Run()
	return err == nil
}

// ListAvailableVoices elenca le voci disponibili (richiede edge-tts)
func ListAvailableVoices(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "edge-tts", "--list-voices")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list voices: %w", err)
	}

	// Parse output
	lines := strings.Split(string(output), "\n")
	var voices []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Name") {
			voices = append(voices, line)
		}
	}

	return voices, nil
}