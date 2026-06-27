// Package scripts: section_regen is the "regenerate a single section" use
// case. Pre-PR4.F (June 2026) the logic lived inline in
// api/script/handler_flow_ops.go::RegenerateSection, mixing HTTP parsing,
// prompt construction, Ollama invocation, persistence, doc re-upload,
// and serialization all in one 120-line Gin handler. Moving the use case
// here makes the orchestrator unit-testable without an HTTP context and
// removes the imperative business branch from the handler.
//
// API surface:
//
//   type SectionRegenerator struct { Repo, Generator, DocClient, Cfg, Log }
//   func (r *SectionRegenerator) Regenerate(ctx, req) (*Result, error)
//   func BuildSectionDocHTML(title, sectionTitles, sectionContents, noChapters, language) string
package scripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Public typed errors so the HTTP transport can map them to specific
// status codes without leaking infrastructure details.
var (
	ErrSectionNotFound       = errors.New("section not found")
	ErrSectionScriptMismatch = errors.New("section does not belong to the specified script")
	ErrScriptNotFound        = errors.New("script not found")
	ErrEmptyGeneratorOutput  = errors.New("received empty response from generator")
)

// SectionRegenRequest is the input for the regenerate-section use case.
type SectionRegenRequest struct {
	ScriptID    int64
	SectionID   int64
	Instruction string
	Model       string
}

// SectionRegenResult is the output of the regenerate-section use case.
type SectionRegenResult struct {
	SectionID int64
	Title     string
	Content   string
}

// SectionRegenerator orchestrates the regenerate-section flow.
type SectionRegenerator struct {
	Repo      ScriptRepository
	Generator *ollama.Generator
	DocClient drive.DocClient
	Cfg       *config.Config
	Log       *zap.Logger
}

// NewSectionRegenerator creates a SectionRegenerator.
func NewSectionRegenerator(
	repo ScriptRepository,
	gen *ollama.Generator,
	docClient drive.DocClient,
	cfg *config.Config,
	log *zap.Logger,
) *SectionRegenerator {
	return &SectionRegenerator{
		Repo:      repo,
		Generator: gen,
		DocClient: docClient,
		Cfg:       cfg,
		Log:       log,
	}
}

// Regenerate runs the use case. Steps:
//
//  1. lookup target section; verify (scriptID, sectionID) match.
//  2. lookup script + all sections + adjacent sections for prompt context.
//  3. build the regeneration prompt and resolve the model.
//  4. invoke Ollama with the proportional-duration context split.
//  5. persist the new section content.
//  6. best-effort re-upload of the full document HTML to the linked Drive
//     doc (only fires when a google_doc_id is present in MetadataJSON).
//
// Doc re-upload failures are logged, not returned: the DB write has
// already succeeded and the caller can re-trigger the doc sync via
// the cache-evict / regenerate-from-clips path.
func (r *SectionRegenerator) Regenerate(ctx context.Context, req SectionRegenRequest) (*SectionRegenResult, error) {
	if r.Repo == nil {
		return nil, fmt.Errorf("section regenerator: repo not configured")
	}
	if r.Generator == nil {
		return nil, fmt.Errorf("section regenerator: ollama generator not configured")
	}

	section, err := r.Repo.GetSectionByID(ctx, req.SectionID)
	if err != nil {
		if r.Log != nil {
			r.Log.Error("regenerate section: get section failed",
				zap.Int64("section_id", req.SectionID), zap.Error(err))
		}
		return nil, fmt.Errorf("%w: get section: %v", ErrSectionNotFound, err)
	}
	if section.ScriptID != req.ScriptID {
		return nil, ErrSectionScriptMismatch
	}

	scriptRecord, allSections, _, err := r.Repo.GetScriptByID(req.ScriptID)
	if err != nil {
		return nil, fmt.Errorf("%w: get script: %v", ErrScriptNotFound, err)
	}

	prev, next, adjErr := r.Repo.GetAdjacentSections(ctx, req.ScriptID, section.SortOrder)
	if adjErr != nil && r.Log != nil {
		r.Log.Warn("regenerate section: adjacent sections lookup failed (continuing without context)",
			zap.Int64("script_id", req.ScriptID),
			zap.Int("sort_order", section.SortOrder),
			zap.Error(adjErr))
	}

	prompt := buildRegenSectionPrompt(section, scriptRecord, prev, next, req.Instruction)

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(scriptRecord.ModelUsed)
	}
	if model == "" && r.Cfg != nil {
		model = strings.TrimSpace(r.Cfg.External.OllamaModel)
	}

	if r.Log != nil {
		r.Log.Info("regenerate section: dispatching to ollama",
			zap.Int64("section_id", req.SectionID),
			zap.String("model", model),
			zap.Int("prompt_chars", len(prompt)))
	}

	// duration proportional to the section count, with a 1-section
	// floor so single-section scripts do not divide-by-zero.
	sectionDur := scriptRecord.Duration
	if n := len(allSections); n > 0 {
		sectionDur = scriptRecord.Duration / n
	}
	res, err := r.Generator.GenerateScript(ctx, ollamatypes.TextGenerationRequest{
		Language: scriptRecord.Language,
		Duration: sectionDur,
		Tone:     scriptRecord.Template,
		Model:    model,
		Prompt:   prompt,
	})
	if err != nil {
		if r.Log != nil {
			r.Log.Error("regenerate section: ollama call failed",
				zap.Int64("section_id", req.SectionID), zap.Error(err))
		}
		return nil, fmt.Errorf("regenerate section: ollama: %w", err)
	}

	newContent := strings.TrimSpace(res.Script)
	if newContent == "" {
		return nil, ErrEmptyGeneratorOutput
	}

	if err := r.Repo.UpdateSectionContent(ctx, req.SectionID, newContent); err != nil {
		if r.Log != nil {
			r.Log.Error("regenerate section: db update failed",
				zap.Int64("section_id", req.SectionID), zap.Error(err))
		}
		return nil, fmt.Errorf("regenerate section: update db: %w", err)
	}

	r.maybeUpdateGoogleDoc(ctx, scriptRecord)

	return &SectionRegenResult{
		SectionID: req.SectionID,
		Title:     section.SectionTitle,
		Content:   newContent,
	}, nil
}

// maybeUpdateGoogleDoc re-renders the document HTML with the freshly
// updated sections and pushes it back to the linked Google Doc. Failure
// is logged, never returned: the caller has already received a successful
// section regeneration and we do not want to surface drive-only errors
// to the user. The next regenerate cycle (or a manual doc refresh) will
// naturally pick up the divergence.
func (r *SectionRegenerator) maybeUpdateGoogleDoc(ctx context.Context, scriptRecord *ScriptRecord) {
	if r.DocClient == nil || scriptRecord == nil || strings.TrimSpace(scriptRecord.MetadataJSON) == "" {
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(scriptRecord.MetadataJSON), &meta); err != nil {
		return
	}
	docID, _ := meta["google_doc_id"].(string)
	if strings.TrimSpace(docID) == "" {
		return
	}

	_, updatedSections, _, err := r.Repo.GetScriptByID(scriptRecord.ID)
	if err != nil {
		if r.Log != nil {
			r.Log.Warn("regenerate section: failed to reload sections for doc re-upload",
				zap.Int64("script_id", scriptRecord.ID), zap.Error(err))
		}
		return
	}
	titles := make([]string, len(updatedSections))
	contents := make([]string, len(updatedSections))
	for idx, s := range updatedSections {
		titles[idx] = s.SectionTitle
		contents[idx] = s.Content
	}
	noChapters, _ := meta["no_chapters"].(bool)
	language := strings.TrimSpace(scriptRecord.Language)
	if language == "" {
		language = "en"
	}
	htmlBody := BuildSectionDocHTML(scriptRecord.Topic, titles, contents, noChapters, language)

	if r.Log != nil {
		r.Log.Info("regenerate section: pushing updated doc",
			zap.String("doc_id", docID), zap.Int("sections", len(updatedSections)))
	}
	if err := r.DocClient.UpdateDoc(ctx, docID, scriptRecord.Topic, htmlBody); err != nil {
		if r.Log != nil {
			r.Log.Error("regenerate section: doc update failed",
				zap.String("doc_id", docID), zap.Error(err))
		}
		return
	}
	if r.Log != nil {
		r.Log.Info("regenerate section: google doc updated",
			zap.String("doc_id", docID))
	}
}

// buildRegenSectionPrompt composes the Italian-language prompt sent to
// Ollama. Kept identical to the previous inline version so existing
// regeneration behaviour is preserved byte-for-byte (only moved).
func buildRegenSectionPrompt(
	section *ScriptSectionRecord,
	scriptRecord *ScriptRecord,
	prev, next *ScriptSectionRecord,
	instruction string,
) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Devi rigenerare la sezione intitolata '%s' per il libro o script sul tema '%s'.\n",
		section.SectionTitle, scriptRecord.Topic))
	b.WriteString("Il testo precedente e successivo sono forniti di seguito solo come contesto per garantire la fluidità del testo, evitare ripetizioni e garantire transizioni naturali.\n\n")

	if prev != nil {
		b.WriteString(fmt.Sprintf("--- CONTESTO SEZIONE PRECEDENTE (%s) ---\n%s\n\n",
			prev.SectionTitle, prev.Content))
	}
	b.WriteString(fmt.Sprintf("--- SEZIONE CORRENTE DA RIGENERARE (usa come base) ---\n%s\n\n",
		section.Content))
	if next != nil {
		b.WriteString(fmt.Sprintf("--- CONTESTO SEZIONE SUCCESSIVA (%s) ---\n%s\n\n",
			next.SectionTitle, next.Content))
	}

	b.WriteString(fmt.Sprintf("Istruzione specifica di rigenerazione:\n\"%s\"\n\n", instruction))
	b.WriteString("Rispondi DIRETTAMENTE ed ESCLUSIVAMENTE con il nuovo testo per questa sezione. Non includere preamboli, note o etichette come 'Ecco il testo rigenerato:' o markdown di intestazione per il capitolo.")
	return b.String()
}

// BuildSectionDocHTML renders the Google Doc HTML body for a list of
// sections. Public: lives in the application layer so use cases
// (regenerate, batch re-render, future cache-and-rehydrate) can
// share it. Previously was the buildSectionDocHTML helper inside
// api/script/handler_flow_ops.go.
//
// Localised "Chapter" / "Capitolo" / "Chapitre" etc. mirrors the
// batch pipeline's HTML rendering so docs regenerated from section
// regen look identical to docs created by batch generation.
func BuildSectionDocHTML(title string, sectionTitles []string, sectionContents []string, noChapters bool, language string) string {
	cl := "Chapter"
	switch language {
	case "it":
		cl = "Capitolo"
	case "fr":
		cl = "Chapitre"
	case "es":
		cl = "Capítulo"
	case "de":
		cl = "Kapitel"
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(strings.TrimSpace(title))))

	if !noChapters {
		b.WriteString(fmt.Sprintf("<h2>%s</h2>", html.EscapeString("Table of Contents")))
		b.WriteString("<ol>")
		for idx := range sectionTitles {
			b.WriteString(fmt.Sprintf("<li><a href=\"#ch-%d\">%s %d: %s</a></li>",
				idx+1, cl, idx+1, html.EscapeString(strings.TrimSpace(sectionTitles[idx]))))
		}
		b.WriteString("</ol>")
		b.WriteString("<hr>")
	}
	for idx := range sectionTitles {
		if !noChapters {
			b.WriteString(fmt.Sprintf("<section id=\"ch-%d\">", idx+1))
			b.WriteString(fmt.Sprintf("<h2>%s %d: %s</h2>", cl, idx+1, html.EscapeString(strings.TrimSpace(sectionTitles[idx]))))
		}
		for _, para := range strings.Split(sectionContents[idx], "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			para = textutil.CleanForVoiceover(para)
			para = html.EscapeString(para)
			para = strings.ReplaceAll(para, "\n", "<br>")
			b.WriteString(fmt.Sprintf("<p>%s</p>", para))
		}
		if !noChapters {
			b.WriteString("</section>")
			if idx < len(sectionTitles)-1 {
				b.WriteString("<hr>")
			}
		} else {
			b.WriteString("<br>")
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}
