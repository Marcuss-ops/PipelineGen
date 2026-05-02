package jobs

import "fmt"

type ArtlistRunPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Term        string `json:"term"`
	Limit       int    `json:"limit"`
	Strategy    string `json:"strategy"`
	DryRun      bool   `json:"dry_run"`
}

func (p *ArtlistRunPayload) Validate() error {
	if p.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if p.Term == "" {
		return fmt.Errorf("term is required")
	}
	return nil
}

type YouTubeClipExtractPayload struct {
	WorkspaceID string           `json:"workspace_id"`
	ProjectID   string           `json:"project_id"`
	URL         string           `json:"url"`
	Segments    []YouTubeSegment `json:"segments"`
	UploadDrive bool             `json:"upload_drive"`
	Normalize   bool             `json:"normalize"`
}

type YouTubeSegment struct {
	Name  string   `json:"name"`
	Start string   `json:"start"`
	End   string   `json:"end"`
	Tags  []string `json:"tags"`
}

func (p *YouTubeClipExtractPayload) Validate() error {
	if p.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if p.URL == "" {
		return fmt.Errorf("url is required")
	}
	if len(p.Segments) == 0 {
		return fmt.Errorf("at least one segment is required")
	}
	return nil
}

type ScriptGeneratePayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Topic       string `json:"topic"`
	Style       string `json:"style"`
	Language    string `json:"language"`
}

func (p *ScriptGeneratePayload) Validate() error {
	if p.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if p.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	return nil
}

type ScriptPublishPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	ScriptID    string `json:"script_id"`
	Target      string `json:"target"`
}

func (p *ScriptPublishPayload) Validate() error {
	if p.ScriptID == "" {
		return fmt.Errorf("script_id is required")
	}
	if p.Target == "" {
		return fmt.Errorf("target is required")
	}
	return nil
}

type VoiceoverGeneratePayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	ScriptID    string `json:"script_id"`
	Voice       string `json:"voice"`
}

func (p *VoiceoverGeneratePayload) Validate() error {
	if p.ScriptID == "" {
		return fmt.Errorf("script_id is required")
	}
	return nil
}

type MediaMatchPayload struct {
	WorkspaceID  string   `json:"workspace_id"`
	ProjectID    string   `json:"project_id"`
	ScriptID     string   `json:"script_id"`
	MaxPerSegment int     `json:"max_per_segment"`
	Sources      []string `json:"sources"`
}

func (p *MediaMatchPayload) Validate() error {
	if p.ScriptID == "" {
		return fmt.Errorf("script_id is required")
	}
	return nil
}

type MediaImportPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Source      string `json:"source"`
	DryRun      bool   `json:"dry_run"`
}

func (p *MediaImportPayload) Validate() error {
	if p.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if p.Source == "" {
		return fmt.Errorf("source is required")
	}
	return nil
}
