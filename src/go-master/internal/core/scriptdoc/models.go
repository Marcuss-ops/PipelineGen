package scriptdoc

import (
	"context"
	"time"
)

type Script struct {
	ID           string
	WorkspaceID  string
	ProjectID    string
	Title        string
	Content      string
	Style        string
	Language     string
	Status       string
	DocURL       string
	VoiceoverID  string
	MetadataJSON string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type GeneratedScript struct {
	Title   string
	Content string
}

type TimelineSegment struct {
	Name   string
	Start  string
	End    string
	Tags   []string
	Assets []AssetSuggestion
}

type AssetSuggestion struct {
	MediaItemID string
	Title       string
	SourceKind  string
	Confidence  float64
}

type PreviewRequest struct {
	WorkspaceID string
	ProjectID   string
	Topic       string
	Style       string
	Language    string
}

type PreviewResponse struct {
	Title    string
	Content  string
	Timeline []TimelineSegment
}

type GenerateRequest struct {
	WorkspaceID string
	ProjectID   string
	Topic       string
	Style       string
	Language    string
}

type GenerateResponse struct {
	ScriptID string
	Title    string
	Content  string
}

type PublishRequest struct {
	WorkspaceID string
	ProjectID   string
	ScriptID    string
	Target      string
}

type PublishResponse struct {
	DocURL string
}

type MatchAssetsRequest struct {
	WorkspaceID   string
	ProjectID     string
	ScriptID      string
	MaxPerSegment int
	Sources       []string
}

type MatchAssetsResponse struct {
	ScriptID string
	Segments []TimelineSegment
}

type ScriptRepository interface {
	Create(ctx context.Context, script *Script) error
	Update(ctx context.Context, script *Script) error
	GetByID(ctx context.Context, id string) (*Script, error)
	ListByProject(ctx context.Context, projectID string) ([]*Script, error)
}

type ScriptGenerator interface {
	Generate(ctx context.Context, input GenerationInput) (*GeneratedScript, error)
}

type DocumentPublisher interface {
	Publish(ctx context.Context, doc Document) (*PublishedDocument, error)
}

type AssetMatcher interface {
	Match(ctx context.Context, req MatchAssetsRequest) (*MatchAssetsResponse, error)
}

type VoiceoverGenerator interface {
	Generate(ctx context.Context, input VoiceoverInput) (*VoiceoverResult, error)
}

type GenerationInput struct {
	Topic    string
	Style    string
	Language string
}

type Document struct {
	Title   string
	Content string
}

type PublishedDocument struct {
	URL string
}

type VoiceoverInput struct {
	ScriptID string
	Text     string
	Voice    string
}

type VoiceoverResult struct {
	FileURL string
}
