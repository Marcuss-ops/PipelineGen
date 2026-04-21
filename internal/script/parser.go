// Package script fornisce parser per convertire script testuali in struttura JSON
package script

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"velox/go-master/internal/nlp"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Parser converte script testuali in StructuredScript
type Parser struct {
	targetDuration int
	language       string
}

// NewParser crea un nuovo parser
func NewParser(targetDuration int, language string) *Parser {
	return &Parser{
		targetDuration: targetDuration,
		language:       language,
	}
}

// Parse converte testo script in StructuredScript
func (p *Parser) Parse(text, title, tone, model string) (*StructuredScript, error) {
	logger.Info("Parsing script into structured format",
		zap.String("title", title),
		zap.Int("target_duration", p.targetDuration),
	)

	// Divide in scene
	scenes := p.splitIntoScenes(text)

	// Estrae keywords ed entità per ogni scena
	for i := range scenes {
		p.extractSceneMetadata(&scenes[i])
	}

	// Calcola durata stimata per scena
	p.estimateSceneDurations(scenes)

	// Crea script strutturato
	scriptID := fmt.Sprintf("script_%d", time.Now().Unix())
	wordCount := countWords(text)

	structuredScript := StructuredScript{
		ID:             scriptID,
		Title:          title,
		Language:       p.language,
		Tone:           tone,
		TargetDuration: p.targetDuration,
		WordCount:      wordCount,
		Scenes:         scenes,
		Metadata: ScriptMetadata{
			Tags:             p.extractGlobalTags(text),
			Category:         p.detectCategory(text),
			KeyMessages:      p.extractKeyMessages(text),
			SEOKeywords:      p.extractSEOKeywords(text),
			RequiresApproval: true,
			TotalClipsNeeded: len(scenes),
		},
		CreatedAt: time.Now(),
		Model:     model,
	}

	logger.Info("Script parsed successfully",
		zap.String("script_id", scriptID),
		zap.Int("scenes", len(scenes)),
		zap.Int("word_count", wordCount),
	)

	return &structuredScript, nil
}

// splitIntoScene divide il testo in scene basandosi su euristica
func (p *Parser) splitIntoScenes(text string) []Scene {
	var scenes []Scene

	// Metodo 1: Cerca marker di sezione espliciti
	sections := p.findExplicitSections(text)
	if len(sections) > 1 {
		for i, section := range sections {
			sceneType := p.determineSceneType(section.Text, i, len(sections))
			scenes = append(scenes, Scene{
				SceneNumber: i + 1,
				Type:        sceneType,
				Title:       section.Title,
				Text:        section.Text,
				Status:      ScenePending,
			})
		}
		return scenes
	}

	// Metodo 2: Divide per paragrafi/significative pause
	paragraphs := p.splitByParagraphs(text)
	if len(paragraphs) > 1 {
		for i, para := range paragraphs {
			sceneType := p.determineSceneType(para, i, len(paragraphs))
			scenes = append(scenes, Scene{
				SceneNumber: i + 1,
				Type:        sceneType,
				Title:       fmt.Sprintf("Scene %d", i+1),
				Text:        para,
				Status:      ScenePending,
			})
		}
		return scenes
	}

	// Metodo 3: Se non riesce a dividere, crea una scena singola
	scenes = append(scenes, Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "Main Content",
		Text:        text,
		Status:      ScenePending,
	})

	return scenes
}

// section rappresenta una sezione del testo
type section struct {
	Title string
	Text  string
}

// findExplicitSections cerca sezioni esplicite con marker
func (p *Parser) findExplicitSections(text string) []section {
	var sections []section

	// Pattern per marker come "===SEZIONE: Titolo===" o "## Titolo"
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^===\s*SEZIONE:\s*(.+?)\s*===\s*\n`),
		regexp.MustCompile(`(?m)^##\s+(.+?)\s*\n`),
		regexp.MustCompile(`(?m)^#\s+(.+?)\s*\n`),
		regexp.MustCompile(`(?m)^(INTRODUZIONE|CONTENUTO|CONCLUSIONE|HOOK|TRANSIZIONE):`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindAllStringSubmatchIndex(text, -1)
		if len(matches) > 1 {
			// Trovato sezioni multiple
			for i, match := range matches {
				titleStart := match[2]
				titleEnd := match[3]
				title := strings.TrimSpace(text[titleStart:titleEnd])

				// Estrae testo fino alla prossima sezione
				var textEnd int
				if i+1 < len(matches) {
					textEnd = matches[i+1][0]
				} else {
					textEnd = len(text)
				}

				sectionText := strings.TrimSpace(text[match[1]:textEnd])
				if sectionText == "" {
					continue
				}

				sections = append(sections, section{
					Title: title,
					Text:  sectionText,
				})
			}
			return sections
		}
	}

	return nil
}

// splitByParagraphs divide il testo in paragrafi significativi
func (p *Parser) splitByParagraphs(text string) []string {
	// Divide per doppi newline (paragrafi)
	paragraphs := strings.Split(text, "\n\n")

	var validParagraphs []string
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if len(para) > 50 { // Ignora paragrafi troppo corti
			validParagraphs = append(validParagraphs, para)
		}
	}

	// Se troppi paragrafi piccoli, raggruppa
	if len(validParagraphs) > 10 {
		return p.groupParagraphs(validParagraphs, 3)
	}

	return validParagraphs
}

// groupParagraphs raggruppa paragrafi per creare scene più grandi
func (p *Parser) groupParagraphs(paragraphs []string, groupSize int) []string {
	var groups []string

	for i := 0; i < len(paragraphs); i += groupSize {
		var group strings.Builder
		end := i + groupSize
		if end > len(paragraphs) {
			end = len(paragraphs)
		}

		for j := i; j < end; j++ {
			group.WriteString(paragraphs[j])
			if j < end-1 {
				group.WriteString("\n\n")
			}
		}

		groups = append(groups, group.String())
	}

	return groups
}

// determineSceneType determina il tipo di scena in base a posizione e contenuto
func (p *Parser) determineSceneType(text string, index, total int) SceneType {
	textLower := strings.ToLower(text)

	// Prima scena
	if index == 0 {
		if strings.Contains(textLower, "hook") || strings.Contains(textLower, "attaccare") {
			return SceneHook
		}
		return SceneIntro
	}

	// Ultima scena
	if index == total-1 {
		return SceneConclusion
	}

	// Controlla per transizioni
	if strings.Contains(textLower, "transizione") || strings.Contains(textLower, "intanto") ||
		strings.Contains(textLower, "nel frattempo") || len(text) < 100 {
		return SceneTransition
	}

	// Default: content
	return SceneContent
}

// extractSceneMetadata estrae keywords ed entità da una scena
func (p *Parser) extractSceneMetadata(scene *Scene) {
	// Estrae keywords usando TF-IDF
	keywords := nlp.ExtractKeywords(scene.Text, 10)
	for _, kw := range keywords {
		scene.Keywords = append(scene.Keywords, kw.Word)
	}

	// Estrae entità (semplificato - in produzione usare Ollama)
	scene.Entities = p.extractEntities(scene.Text)

	// Estrae emozioni (semplificato)
	scene.Emotions = p.detectEmotions(scene.Text)

	// Estrae suggerimenti visivi
	scene.VisualCues = p.extractVisualCues(scene.Text)

	// Conta parole
	scene.WordCount = countWords(scene.Text)
}

// extractEntities estrae entità dal testo (versione semplificata)
func (p *Parser) extractEntities(text string) []SceneEntity {
	var entities []SceneEntity

	// Cerca nomi propri (parole capitalizzate)
	words := strings.Fields(text)
	for i, word := range words {
		cleaned := strings.Trim(word, ".,!?;:\"'()[]{}")

		if len(cleaned) > 2 && cleaned[0] >= 'A' && cleaned[0] <= 'Z' {
			// Controlla se è parte di un nome composto
			entityText := cleaned
			if i+1 < len(words) {
				nextWord := strings.Trim(words[i+1], ".,!?;:\"'()[]{}")
				if len(nextWord) > 1 && nextWord[0] >= 'A' && nextWord[0] <= 'Z' {
					entityText = cleaned + " " + nextWord
				}
			}

			entities = append(entities, SceneEntity{
				Text:      entityText,
				Type:      "PERSON_OR_PLACE",
				Relevance: 0.7,
			})
		}
	}

	return entities
}

// detectEmotions rileva emozioni dal testo
func (p *Parser) detectEmotions(text string) []string {
	var emotions []string

	textLower := strings.ToLower(text)

	emotionWords := map[string][]string{
		"joy":       {"felice", "gioia", "great", "fantastico", "awesome"},
		"sadness":   {"triste", "dolore", "sad", "purtroppo", "sfortunatamente"},
		"anger":     {"rabbia", "arrabbiato", "angry", "furioso"},
		"fear":      {"paura", "timore", "afraid", "preoccupazione"},
		"surprise":  {"sorpreso", "incredibile", "amazing", "wow"},
		"trust":     {"fiducia", "speranza", "hope", "credere"},
		"anticipation": {"futuro", "prossimo", "will", "going to"},
	}

	for emotion, words := range emotionWords {
		for _, word := range words {
			if strings.Contains(textLower, word) {
				emotions = append(emotions, emotion)
				break
			}
		}
	}

	return emotions
}

// extractVisualCues estrae suggerimenti visivi
func (p *Parser) extractVisualCues(text string) []string {
	var cues []string

	textLower := strings.ToLower(text)

	// Pattern per suggerimenti visivi
	visualPatterns := map[string]string{
		"mostra":     "show/demonstrate",
		"vediamo":    "we see",
		"appare":     "appears",
		"cambia":     "scene change",
		"transizione": "transition",
		"zoom":       "zoom",
		"panoramica": "pan shot",
	}

	for pattern, cue := range visualPatterns {
		if strings.Contains(textLower, pattern) {
			cues = append(cues, cue)
		}
	}

	return cues
}

// extractGlobalTags estrae tags globali dallo script
func (p *Parser) extractGlobalTags(text string) []string {
	keywords := nlp.ExtractKeywords(text, 15)
	var tags []string
	for _, kw := range keywords {
		tags = append(tags, kw.Word)
	}
	return tags
}

// detectCategory rileva la categoria dello script
func (p *Parser) detectCategory(text string) string {
	textLower := strings.ToLower(text)

	categories := map[string][]string{
		"tech":      {"tecnologia", "tech", "ai", "人工智能", "software", "computer"},
		"business":  {"business", "azienda", "marketing", "vendite", "soldi"},
		"interview": {"intervista", "intervista", "intervista", "domande", "risposte"},
		"education": {"imparare", "educazione", "scuola", "insegnare"},
		"news":      {"notizie", "news", "ultim'ora", "cronaca"},
	}

	for category, keywords := range categories {
		for _, keyword := range keywords {
			if strings.Contains(textLower, keyword) {
				return category
			}
		}
	}

	return "general"
}

// extractKeyMessages estrae i messaggi chiave
func (p *Parser) extractKeyMessages(text string) []string {
	// Semplificato: estrae le frasi più lunghe come messaggi chiave
	sentences := strings.Split(text, ".")
	var messages []string

	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if len(sentence) > 100 && len(sentence) < 300 {
			messages = append(messages, sentence+".")
			if len(messages) >= 3 {
				break
			}
		}
	}

	return messages
}

// extractSEOKeywords estrae keywords per SEO
func (p *Parser) extractSEOKeywords(text string) []string {
	keywords := nlp.ExtractKeywords(text, 20)
	var seoKeywords []string
	for _, kw := range keywords {
		if kw.Score > 0.3 { // Solo keywords con score alto
			seoKeywords = append(seoKeywords, kw.Word)
		}
	}
	return seoKeywords
}

// estimateSceneDurations stima la durata di ogni scena
func (p *Parser) estimateSceneDurations(scenes []Scene) {
	totalWords := 0
	for _, scene := range scenes {
		totalWords += scene.WordCount
	}

	if totalWords == 0 {
		return
	}

	// Distribuisce durata proporzionalmente
	for i := range scenes {
		proportion := float64(scenes[i].WordCount) / float64(totalWords)
		scenes[i].Duration = int(proportion * float64(p.targetDuration))
	}
}

// countWords conta le parole nel testo
func countWords(text string) int {
	return len(strings.Fields(text))
}
