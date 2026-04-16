// Package tts provides text-to-speech functionality.
package tts

import "context"

// VoiceoverGenerator interfaccia per generazione voiceover
type VoiceoverGenerator interface {
	Generate(ctx context.Context, text string, lang string) (*GenerationResult, error)
	GenerateWithVoice(ctx context.Context, text string, voice string) (*GenerationResult, error)
	ListLanguages() []Language
	GetDefaultVoice(lang string) string
}

// GenerationResult risultato generazione voiceover
type GenerationResult struct {
	FilePath  string `json:"file_path"`
	FileName  string `json:"file_name"`
	Duration  int    `json:"duration"`  // stima in secondi
	WordCount int    `json:"word_count"`
	VoiceUsed string `json:"voice_used"`
	Language  string `json:"language"`
}

// Language rappresenta una lingua con le sue voci
type Language struct {
	Code   string   `json:"code"`
	Name   string   `json:"name"`
	Voices []string `json:"voices"`
}

// GenerateRequest request per generazione voiceover
type GenerateRequest struct {
	Text     string `json:"text" binding:"required,min=1"`
	Language string `json:"language" default:"it"`
	Voice    string `json:"voice,omitempty"`
}

// GenerateResponse risposta generazione voiceover
type GenerateResponse struct {
	OK        bool   `json:"ok"`
	FileName  string `json:"file_name,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	WordCount int    `json:"word_count,omitempty"`
	Voice     string `json:"voice,omitempty"`
	Language  string `json:"language,omitempty"`
	Error     string `json:"error,omitempty"`
}