package jobs

import (
	"sort"
)

// AutomationCapability is the agent-facing projection of a runnable job.
// Internal implementation jobs remain out of this projection by default.
type AutomationCapability struct {
	Type                   string   `json:"type"`
	Version                string   `json:"version"`
	Description            string   `json:"description"`
	AgentVisible           bool     `json:"agent_visible"`
	M2MSubmittable         bool     `json:"m2m_submittable"`
	InputSchema            string   `json:"input_schema,omitempty"`
	ResultSchema           string   `json:"result_schema,omitempty"`
	ArtifactKinds          []string `json:"artifact_kinds,omitempty"`
	RequiredScope          string   `json:"required_scope,omitempty"`
	RequiredCapabilities   []string `json:"required_capabilities,omitempty"`
	EstimatedResourceClass string   `json:"estimated_resource_class,omitempty"`
}

// AutomationCatalog is the single agent-facing capability catalog. It is
// constructed from the canonical job registry and filtered by the live
// handler set, so policy-only or orphaned jobs cannot be submitted remotely.
type AutomationCatalog struct {
	items map[string]AutomationCapability
}

// NewAutomationCatalog builds the catalog from the canonical registry and
// the process' live handlers. The allow-list is intentionally explicit:
// internal maintenance, child, and persistence jobs never become agent tools
// merely because a handler happens to be registered.
func NewAutomationCatalog(reg *Registry, liveTypes []string) *AutomationCatalog {
	live := make(map[string]struct{}, len(liveTypes))
	for _, typ := range liveTypes {
		live[typ] = struct{}{}
	}
	catalog := &AutomationCatalog{items: make(map[string]AutomationCapability)}
	if reg == nil {
		return catalog
	}

	// These are the stable external capabilities currently implemented by
	// PipelineGen. video.create/assemble are deliberately absent until their
	// durable handlers exist; advertising them would create fake availability.
	safe := map[string]AutomationCapability{
		TypeScriptGenerate: {
			Type: TypeScriptGenerate, Version: "v1", Description: "Generate a script and editorial plan",
			InputSchema: "script.generate.v1", ResultSchema: "script.generate.result.v1", ArtifactKinds: []string{"script"}, EstimatedResourceClass: "llm",
		},
		TypeVoiceoverGenerate: {
			Type: TypeVoiceoverGenerate, Version: "v1", Description: "Generate voiceover audio",
			InputSchema: "voiceover.generate.v1", ResultSchema: "voiceover.generate.result.v1", ArtifactKinds: []string{"audio"}, EstimatedResourceClass: "audio",
		},
		TypeMediaStock: {
			Type: TypeMediaStock, Version: "v1", Description: "Acquire stock media",
			InputSchema: "media.stock.v1", ResultSchema: "media.stock.result.v1", ArtifactKinds: []string{"video", "image"}, EstimatedResourceClass: "network",
		},
		TypeYouTubeClipExtract: {
			Type: TypeYouTubeClipExtract, Version: "v1", Description: "Extract a YouTube clip",
			InputSchema: "youtube_clip.extract.v1", ResultSchema: "youtube_clip.extract.result.v1", ArtifactKinds: []string{"video"}, EstimatedResourceClass: "media",
		},
		TypeImageGenerateGoogle: {
			Type: TypeImageGenerateGoogle, Version: "v1", Description: "Generate an image",
			InputSchema: "image.generate.google.v1", ResultSchema: "image.generate.google.result.v1", ArtifactKinds: []string{"image"}, EstimatedResourceClass: "gpu",
		},
		TypeClipRender: {
			Type: TypeClipRender, Version: "v1", Description: "Render a localized clip",
			InputSchema: "clip.render.v1", ResultSchema: "clip.render.result.v1", ArtifactKinds: []string{"video"}, EstimatedResourceClass: "render",
		},
	}
	for typ, item := range safe {
		if _, registered := live[typ]; !registered {
			continue
		}
		if _, registered := reg.Get(typ); !registered {
			continue
		}
		item.AgentVisible = true
		item.M2MSubmittable = true
		item.RequiredScope = ScopeJobsSubmit
		catalog.items[typ] = item
	}
	return catalog
}

// List returns a stable, defensive snapshot for GET /api/v1/jobs/types.
func (c *AutomationCatalog) List() []AutomationCapability {
	if c == nil {
		return []AutomationCapability{}
	}
	keys := make([]string, 0, len(c.items))
	for typ := range c.items {
		keys = append(keys, typ)
	}
	sort.Strings(keys)
	out := make([]AutomationCapability, 0, len(keys))
	for _, typ := range keys {
		item := c.items[typ]
		item.ArtifactKinds = append([]string{}, item.ArtifactKinds...)
		item.RequiredCapabilities = append([]string{}, item.RequiredCapabilities...)
		out = append(out, item)
	}
	return out
}

// Allows reports whether a type is agent-visible and M2M-submittable.
func (c *AutomationCatalog) Allows(typ string) bool {
	if c == nil {
		return false
	}
	item, ok := c.items[typ]
	return ok && item.AgentVisible && item.M2MSubmittable
}
