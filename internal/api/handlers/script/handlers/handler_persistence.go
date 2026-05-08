package handlers

import (
	"context"
	"strings"

	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/artlist"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

// saveScriptToDB saves the generated script to the database
func (h *ScriptDocsHandler) saveScriptToDB(ctx context.Context, req script.ScriptDocsRequest, document *script.ScriptDocument) {
	sections := make([]scripts.ScriptSectionRecord, 0, len(document.Sections))
	for i, sec := range document.Sections {
		if sec.Title == "🧾 Metadata" {
			continue
		}
		sections = append(sections, scripts.ScriptSectionRecord{
			SectionType:  sec.Title,
			SectionTitle: sec.Title,
			Content:      sec.Body,
			SortOrder:    i,
		})
	}

	scriptRec := &scripts.ScriptRecord{
		Topic:          req.Topic,
		Duration:       req.Duration,
		Language:       req.Language,
		Template:       req.Template,
		Mode:           "modular",
		NarrativeText:  document.Content,
		TimelineJSON:   "",
		EntitiesJSON:   "",
		MetadataJSON:   "",
		FullDocument:   document.Content,
		ModelUsed:      "",
		OllamaBaseURL:  "",
		Version:        1,
		ParentScriptID: nil,
		IsDeleted:      false,
	}
	if client := h.generator.GetClient(); client != nil {
		scriptRec.ModelUsed = client.Model()
		scriptRec.OllamaBaseURL = client.BaseURL()
	}

	scriptID, err := h.scriptsRepo.SaveScript(scriptRec, sections, nil)
	if err != nil {
		zap.L().Error("Failed to save script to database", zap.Error(err))
		return
	}

	zap.L().Info("Script saved to database", zap.Int64("script_id", scriptID), zap.String("topic", req.Topic))
}

// triggerBackgroundHarvest enqueues jobs for background harvesting based on search suggestions
func (h *ScriptDocsHandler) triggerBackgroundHarvest(ctx context.Context, document *script.ScriptDocument) {
	zap.L().Info("triggerBackgroundHarvest called",
		zap.Bool("artlistService_nil", h.artlistService == nil),
		zap.Bool("jobsService_nil", h.jobsService == nil),
		zap.Bool("document_nil", document == nil),
	)

	if h.artlistService == nil || h.jobsService == nil || document == nil || document.Timeline == nil {
		zap.L().Warn("triggerBackgroundHarvest: early return, prerequisites not met")
		return
	}

	uniqueTags := make(map[string]struct{})
	for _, seg := range document.Timeline.Segments {
		zap.L().Info("triggerBackgroundHarvest: checking segment",
			zap.Int("segment_index", seg.Index),
			zap.Int("search_suggestions_count", len(seg.SearchSuggestions)),
			zap.Strings("suggestions", seg.SearchSuggestions),
		)
		for _, tag := range seg.SearchSuggestions {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				uniqueTags[tag] = struct{}{}
			}
		}
	}

	zap.L().Info("triggerBackgroundHarvest: unique tags collected",
		zap.Int("unique_tag_count", len(uniqueTags)),
		zap.Any("unique_tags", uniqueTags),
	)

	if len(uniqueTags) == 0 {
		zap.L().Warn("triggerBackgroundHarvest: no unique tags, returning")
		return
	}

	zap.L().Info("enqueueing background harvest jobs for suggestions", zap.Int("tag_count", len(uniqueTags)))

	jobCodec := artlist.JobCodec{}
	for tag := range uniqueTags {
		zap.L().Info("triggerBackgroundHarvest: about to enqueue job",
			zap.String("tag", tag),
			zap.String("strategy", "verify"),
			zap.Int("limit", 3),
		)

		req := &artlist.RunTagRequest{
			Term:         tag,
			Limit:        3,
			Strategy:     "verify",
			ClipDuration: 7,
			Width:        1920,
			Height:       1080,
			FPS:          30,
		}
		payload := jobCodec.PayloadFromRequest(req)

		job, err := h.jobsService.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:     models.JobTypeArtlistRun,
			Payload:  payload,
			Priority: 5, // Lower priority for background tasks
		})
		if err != nil {
			zap.L().Error("triggerBackgroundHarvest: FAILED to enqueue job",
				zap.String("tag", tag),
				zap.Error(err),
			)
		} else {
			zap.L().Info("triggerBackgroundHarvest: SUCCESS enqueued job",
				zap.String("tag", tag),
				zap.String("job_id", job.ID),
			)
		}
	}
}
