package scriptdoc

import (
	"context"
	"errors"
	"time"

	coreScriptdoc "velox/go-master/internal/core/scriptdoc"
)

type Usecase struct {
	scriptGenerator coreScriptdoc.ScriptGenerator
	scriptRepo      coreScriptdoc.ScriptRepository
	assetMatcher    coreScriptdoc.AssetMatcher
	docPublisher    coreScriptdoc.DocumentPublisher
	voiceover       coreScriptdoc.VoiceoverGenerator
}

func NewUsecase(
	scriptGenerator coreScriptdoc.ScriptGenerator,
	scriptRepo coreScriptdoc.ScriptRepository,
	assetMatcher coreScriptdoc.AssetMatcher,
	docPublisher coreScriptdoc.DocumentPublisher,
	voiceover coreScriptdoc.VoiceoverGenerator,
) *Usecase {
	return &Usecase{
		scriptGenerator: scriptGenerator,
		scriptRepo:      scriptRepo,
		assetMatcher:    assetMatcher,
		docPublisher:    docPublisher,
		voiceover:       voiceover,
	}
}

func (u *Usecase) Preview(ctx context.Context, req coreScriptdoc.PreviewRequest) (*coreScriptdoc.PreviewResponse, error) {
	if req.Topic == "" {
		return nil, errors.New("topic is required")
	}

	input := coreScriptdoc.GenerationInput{
		Topic:    req.Topic,
		Style:    req.Style,
		Language: req.Language,
	}

	script, err := u.scriptGenerator.Generate(ctx, input)
	if err != nil {
		return nil, err
	}

	timeline, _ := BuildTimeline(script)

	return &coreScriptdoc.PreviewResponse{
		Title:    script.Title,
		Content:  script.Content,
		Timeline: timeline,
	}, nil
}

func (u *Usecase) Generate(ctx context.Context, req coreScriptdoc.GenerateRequest) (*coreScriptdoc.GenerateResponse, error) {
	if req.Topic == "" {
		return nil, errors.New("topic is required")
	}

	input := coreScriptdoc.GenerationInput{
		Topic:    req.Topic,
		Style:    req.Style,
		Language: req.Language,
	}

	generated, err := u.scriptGenerator.Generate(ctx, input)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	script := &coreScriptdoc.Script{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Title:       generated.Title,
		Content:     generated.Content,
		Style:       req.Style,
		Language:    req.Language,
		Status:      "draft",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := u.scriptRepo.Create(ctx, script); err != nil {
		return nil, err
	}

	return &coreScriptdoc.GenerateResponse{
		ScriptID: script.ID,
		Title:    script.Title,
		Content:  script.Content,
	}, nil
}

func (u *Usecase) Publish(ctx context.Context, req coreScriptdoc.PublishRequest) (*coreScriptdoc.PublishResponse, error) {
	script, err := u.scriptRepo.GetByID(ctx, req.ScriptID)
	if err != nil {
		return nil, err
	}

	doc := coreScriptdoc.Document{
		Title:   script.Title,
		Content: script.Content,
	}

	published, err := u.docPublisher.Publish(ctx, doc)
	if err != nil {
		return nil, err
	}

	script.DocURL = published.URL
	_ = u.scriptRepo.Update(ctx, script)

	return &coreScriptdoc.PublishResponse{
		DocURL: published.URL,
	}, nil
}

func (u *Usecase) MatchAssets(ctx context.Context, req coreScriptdoc.MatchAssetsRequest) (*coreScriptdoc.MatchAssetsResponse, error) {
	return u.assetMatcher.Match(ctx, req)
}

func (u *Usecase) GenerateVoiceover(ctx context.Context, scriptID string, voice string) (*coreScriptdoc.VoiceoverResult, error) {
	script, err := u.scriptRepo.GetByID(ctx, scriptID)
	if err != nil {
		return nil, err
	}

	input := coreScriptdoc.VoiceoverInput{
		ScriptID: scriptID,
		Text:     script.Content,
		Voice:    voice,
	}

	return u.voiceover.Generate(ctx, input)
}

func BuildTimeline(script *coreScriptdoc.GeneratedScript) ([]coreScriptdoc.TimelineSegment, error) {
	return nil, nil
}
