package scriptdocs

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/sources/artlist"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
)

type PersistenceService struct {
	repo        *scripts.ScriptRepository
	jobsService *jobservice.Service
	log         *zap.Logger
}

func NewPersistenceService(repo *scripts.ScriptRepository, jobsService *jobservice.Service, log *zap.Logger) *PersistenceService {
	return &PersistenceService{
		repo:        repo,
		jobsService: jobsService,
		log:         log,
	}
}

// SaveScriptToDB saves the generated script to the database.
func (s *PersistenceService) SaveScriptToDB(ctx context.Context, req script.ScriptDocsRequest, document *script.ScriptDocument, modelName, baseURL string) (int64, error) {
	var timelineJSON string
	if document != nil && document.Timeline != nil {
		if data, err := json.Marshal(document.Timeline); err == nil {
			timelineJSON = string(data)
		}
	}

	var metadataJSON string
	if document != nil && document.Metadata != nil {
		if data, err := json.Marshal(document.Metadata); err == nil {
			metadataJSON = string(data)
		}
	}

	var entitiesJSON string
	if document != nil && document.Metadata != nil {
		entities := map[string]any{}
		for _, key := range []string{"special_names", "important_phrases", "important_words", "artlist_phrases"} {
			if value, ok := document.Metadata[key]; ok {
				entities[key] = value
			}
		}
		if len(entities) > 0 {
			if data, err := json.Marshal(entities); err == nil {
				entitiesJSON = string(data)
			}
		}
	}

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
		TimelineJSON:   timelineJSON,
		EntitiesJSON:   entitiesJSON,
		MetadataJSON:   metadataJSON,
		FullDocument:   document.Content,
		ModelUsed:      modelName,
		OllamaBaseURL:  baseURL,
		Version:        1,
		ParentScriptID: nil,
		IsDeleted:      false,
	}

	scriptID, err := s.repo.SaveScript(scriptRec, sections, nil)
	if err != nil {
		s.log.Error("Failed to save script to database", zap.Error(err))
		return 0, err
	}

	s.log.Info("Script saved to database", zap.Int64("script_id", scriptID), zap.String("topic", req.Topic))
	return scriptID, nil
}

// TriggerBackgroundHarvest enqueues jobs for background harvesting based on search suggestions.
func (s *PersistenceService) TriggerBackgroundHarvest(ctx context.Context, document *script.ScriptDocument) []string {
	if s.jobsService == nil || document == nil || document.Timeline == nil {
		return nil
	}

	uniqueTags := make(map[string]struct{})
	for _, seg := range document.Timeline.Segments {
		for _, tag := range seg.SearchSuggestions {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				uniqueTags[tag] = struct{}{}
			}
		}
	}

	if len(uniqueTags) == 0 {
		return nil
	}

	s.log.Info("enqueueing background harvest jobs for suggestions", zap.Int("tag_count", len(uniqueTags)))

	var jobIDs []string
	jobCodec := artlist.JobCodec{}
	for tag := range uniqueTags {
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

		job, err := s.jobsService.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:     models.JobTypeArtlistRun,
			Payload:  payload,
			Priority: 5,
		})
		if err != nil {
			s.log.Error("Failed to enqueue harvest job", zap.String("tag", tag), zap.Error(err))
		} else {
			s.log.Info("Successfully enqueued harvest job", zap.String("tag", tag), zap.String("job_id", job.ID))
			jobIDs = append(jobIDs, job.ID)
		}
	}
	return jobIDs
}
